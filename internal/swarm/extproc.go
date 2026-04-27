package swarm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/baphled/flowstate/internal/gates"
)

// ExtGateRequest is the host-side input shape forwarded to a v0 ext
// gate. JSON-marshalled onto the gate's stdin by subprocessRunner.
type ExtGateRequest struct {
	Kind     string         `json:"kind"`
	MemberID string         `json:"member_id"`
	When     string         `json:"when"`
	Payload  []byte         `json:"payload"`
	Policy   map[string]any `json:"policy,omitempty"`
}

// ExtGateResponse is the host-side output shape parsed from the gate's
// stdout JSON. Pass is treated as the canonical pass/fail bit; missing
// Pass is normalised to false in subprocessRunner.
type ExtGateResponse struct {
	Pass     bool              `json:"pass"`
	Reason   string            `json:"reason,omitempty"`
	Evidence []ExtGateEvidence `json:"evidence,omitempty"`
}

// ExtGateEvidence is one item in ExtGateResponse.Evidence.
type ExtGateEvidence struct {
	Source     string  `json:"source,omitempty"`
	Snippet    string  `json:"snippet,omitempty"`
	Similarity float64 `json:"similarity,omitempty"`
}

// ExtGateRunner is the dispatcher-facing interface every v0 gate
// implementation satisfies. The subprocessRunner wraps a Manifest +
// fork/exec; ExtGateFunc wraps a Go function for tests.
type ExtGateRunner interface {
	Evaluate(ctx context.Context, req ExtGateRequest) (ExtGateResponse, error)
}

// ExtGateFunc adapts a Go function so it satisfies ExtGateRunner. Used
// by RegisterExtGateFunc.
type ExtGateFunc func(ctx context.Context, req ExtGateRequest) (ExtGateResponse, error)

// Evaluate satisfies ExtGateRunner.
func (f ExtGateFunc) Evaluate(ctx context.Context, req ExtGateRequest) (ExtGateResponse, error) {
	return f(ctx, req)
}

var (
	extGateRegistryMu sync.RWMutex
	extGateRegistry   = map[string]ExtGateRunner{}
)

// RegisterExtGateFunc registers a Go function as a v0 gate runner.
// Used by tests to skip the filesystem path; the runtime treats
// func-registered and file-discovered runners identically.
func RegisterExtGateFunc(name string, fn ExtGateFunc) error {
	if name == "" {
		return fmt.Errorf("ext gate registration: empty name")
	}
	if fn == nil {
		return fmt.Errorf("ext gate registration %q: nil function", name)
	}
	return registerRunner(name, fn)
}

// RegisterExtGateFromManifest registers a subprocess-backed runner
// derived from m. Validates that the executable exists and is
// executable; rejects at registration time so boot fails fast.
func RegisterExtGateFromManifest(m gates.Manifest) error {
	runner, err := newSubprocessRunner(m)
	if err != nil {
		return err
	}
	return registerRunner(m.Name, runner)
}

// LookupExtGate returns the registered runner for name plus a found bit.
func LookupExtGate(name string) (ExtGateRunner, bool) {
	extGateRegistryMu.RLock()
	defer extGateRegistryMu.RUnlock()
	r, ok := extGateRegistry[name]
	return r, ok
}

// ResetExtGateRegistryForTest clears the registry. Test-only.
func ResetExtGateRegistryForTest() {
	extGateRegistryMu.Lock()
	defer extGateRegistryMu.Unlock()
	extGateRegistry = map[string]ExtGateRunner{}
}

// DispatchExt resolves an ext:<name> against the registry and forwards
// the request. Pass:false maps to *GateError{Reason}; runner errors
// (subprocess crash, JSON parse, timeout) map to *GateError{Cause}.
func DispatchExt(ctx context.Context, kind string, req ExtGateRequest) error {
	if !strings.HasPrefix(kind, gateKindExtPrefix) {
		return fmt.Errorf("DispatchExt: kind %q must start with %s", kind, gateKindExtPrefix)
	}
	short := strings.TrimPrefix(kind, gateKindExtPrefix)
	runner, ok := LookupExtGate(short)
	if !ok {
		return fmt.Errorf("ext gate %q is not registered", kind)
	}
	req.Kind = short
	resp, err := runner.Evaluate(ctx, req)
	if err != nil {
		return &GateError{GateKind: kind, MemberID: req.MemberID, When: req.When, Cause: err}
	}
	if !resp.Pass {
		return &GateError{
			GateKind:    kind,
			MemberID:    req.MemberID,
			When:        req.When,
			Reason:      resp.Reason,
			ExtEvidence: resp.Evidence,
		}
	}
	return nil
}

// registerRunner is the shared insertion path for both Func and
// Manifest registrations. Enforces no-double-register.
func registerRunner(name string, runner ExtGateRunner) error {
	extGateRegistryMu.Lock()
	defer extGateRegistryMu.Unlock()
	if _, exists := extGateRegistry[name]; exists {
		return fmt.Errorf("ext gate %q already registered", name)
	}
	extGateRegistry[name] = runner
	return nil
}

// subprocessRunner forks the manifest's executable on each Evaluate
// call and exchanges JSON over stdin/stdout. Stderr is captured into a
// bounded buffer for diagnostics.
type subprocessRunner struct {
	manifest gates.Manifest
}

// newSubprocessRunner constructs a runner and validates the
// executable's resolved path is loadable (file exists, executable bit
// set). Filesystem-level validation here prevents a swarm dispatch
// from being the first failure point.
func newSubprocessRunner(m gates.Manifest) (*subprocessRunner, error) {
	abs := m.AbsoluteExecPath()
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("ext gate %q exec %s: %w", m.Name, abs, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("ext gate %q exec %s is a directory", m.Name, abs)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("ext gate %q exec %s is not executable", m.Name, abs)
	}
	return &subprocessRunner{manifest: m}, nil
}

// Evaluate forks the gate, marshals the request, parses the response,
// and enforces the manifest's timeout. Output errors carry the gate
// name + a short stderr excerpt.
func (s *subprocessRunner) Evaluate(ctx context.Context, req ExtGateRequest) (ExtGateResponse, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, s.manifest.Timeout)
	defer cancel()

	req.Policy = mergePolicy(s.manifest.Policy, req.Policy)

	body, err := json.Marshal(req)
	if err != nil {
		return ExtGateResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	cmd := exec.CommandContext(timeoutCtx, s.manifest.AbsoluteExecPath())
	cmd.Dir = s.manifest.Dir
	cmd.Env = append(cmd.Env, "FLOWSTATE_GATE_NAME="+s.manifest.Name)
	cmd.Stdin = bytes.NewReader(body)
	var stdout bytes.Buffer
	stderr := newBoundedBuffer(stderrLimitBytes)
	cmd.Stdout = &stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			return ExtGateResponse{}, fmt.Errorf("gate %q timeout after %s", s.manifest.Name, s.manifest.Timeout)
		}
		return ExtGateResponse{}, fmt.Errorf("gate %q exited (stderr: %q): %w", s.manifest.Name, stderr.String(), err)
	}

	var resp ExtGateResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return ExtGateResponse{}, fmt.Errorf("decode gate response from %q: %w", s.manifest.Name, err)
	}
	return resp, nil
}

// mergePolicy returns a fresh map combining manifest defaults with
// per-request overrides. Per-request keys win on collision.
func mergePolicy(manifest, request map[string]any) map[string]any {
	out := make(map[string]any, len(manifest)+len(request))
	for k, v := range manifest {
		out[k] = v
	}
	for k, v := range request {
		out[k] = v
	}
	return out
}

const stderrLimitBytes = 8 * 1024

// boundedBuffer is a bytes.Buffer with a hard cap. Writes past the cap
// are silently truncated so a flooding stderr cannot OOM the host.
type boundedBuffer struct {
	cap int
	buf bytes.Buffer
}

func newBoundedBuffer(cap int) *boundedBuffer {
	return &boundedBuffer{cap: cap}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.cap - b.buf.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	return b.buf.Write(p)
}

func (b *boundedBuffer) String() string { return b.buf.String() }
