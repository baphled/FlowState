package tool

// exec.run session tool (M2-P3): runs owner-allow-listed binaries
// with deny-by-default subcommand gating, always in the session
// workdir, under a hard timeout, with a scrubbed child environment
// and byte-capped output capture.
//
// SECURITY POSTURE — honest limitations: network access is NOT
// enforced (git push/fetch, gh, go mod download may reach the
// network; enforcement is argv0 + subcommand allow-listing plus
// timeout only); no binary hash pinning, so a hostile replacement
// of a listed path on disk would execute; interpreter/shell
// entrypoints would bypass subcommand gating and are therefore not
// allow-listed by default.
//
// Double gate: usable only when FLOWSTATE_EXEC_ENABLED is set at
// loop construction AND at each Invoke, plus the session allowlist.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ExecEnabledEnv is the env opt-in gate for exec.run.
const ExecEnabledEnv = "FLOWSTATE_EXEC_ENABLED"

// ExecEnabled is the env opt-in gate: true only for "1"/"true"
// (case-insensitive); unset or anything else is disabled.
func ExecEnabled() bool {
	switch strings.ToLower(os.Getenv(ExecEnabledEnv)) {
	case "1", "true":
		return true
	}
	return false
}

var (
	// ErrExecNotEnabled indicates that the explicit environment opt-in is absent.
	ErrExecNotEnabled = errors.New("tool: exec disabled (FLOWSTATE_EXEC_ENABLED not set)")
	// ErrExecNotAllowed indicates a binary missing from the resolved allow-list.
	ErrExecNotAllowed = errors.New("tool: binary not on exec allowlist")
	// ErrExecDeniedSubcommand indicates an unapproved or malformed argument vector.
	ErrExecDeniedSubcommand = errors.New("tool: subcommand not allowed")
	// ErrExecTimeout indicates that the child exceeded its deadline.
	ErrExecTimeout = errors.New("tool: exec timed out")
	// ErrExecOversize indicates that combined child output exceeded its cap.
	ErrExecOversize = errors.New("tool: exec output exceeds size cap")
)

// lookPathAbs resolves name to an absolute executable path via PATH
// lookup; a relative path that cannot be resolved is an error.
func lookPathAbs(name string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

// defaultExecAllowlist is the OWNER POLICY deny-by-default
// allow-list; networked entries are an owner decision (see banner).
func defaultExecAllowlist() map[string]ExecBinary {
	return map[string]ExecBinary{
		"git":  {Subcommands: []string{"worktree", "switch", "checkout", "add", "status", "diff", "log", "commit", "push", "fetch", "pull"}},
		"gh":   {Subcommands: []string{"pr", "run"}},
		"go":   {Subcommands: []string{"test", "build", "vet", "fmt", "mod", "tool"}},
		"make": {},
	}
}

// DefaultExecAllowlist returns the owner-policy allow-list with
// paths resolved via LookPath; missing binaries get an empty Path
// and are denied with exec_not_allowed at invocation time.
func DefaultExecAllowlist() map[string]ExecBinary {
	out := map[string]ExecBinary{}
	for name, b := range defaultExecAllowlist() {
		if p, err := lookPathAbs(name); err == nil {
			b.Path = p
		}
		out[name] = b
	}
	return out
}

// NewExecRun returns an exec.run tool rooted at workdir with
// DefaultExecAllowlist() unless overridden.
func NewExecRun(workdir string, opts ...ExecOption) Tool {
	o := execOptions{
		binaries:  DefaultExecAllowlist(),
		timeout:   DefaultExecTimeout,
		maxOutput: DefaultMaxExecOutputBytes,
	}
	for _, opt := range opts {
		opt(&o)
	}
	binaries := map[string]ExecBinary{}
	for name, b := range o.binaries {
		if p, err := lookPathAbs(b.Path); err == nil {
			b.Path = p
			binaries[name] = b
		}
	}
	return &execTool{workdir: workdir, binaries: binaries, timeout: o.timeout, maxOut: o.maxOutput}
}

// WithExecBinaries replaces the default resolved executable allow-list.
func WithExecBinaries(m map[string]ExecBinary) ExecOption {
	return func(o *execOptions) { o.binaries = m }
}

// WithExecTimeout sets the child execution deadline.
func WithExecTimeout(d time.Duration) ExecOption {
	return func(o *execOptions) { o.timeout = d }
}

// WithMaxExecOutputBytes sets the combined stdout/stderr capture limit.
func WithMaxExecOutputBytes(n int64) ExecOption {
	return func(o *execOptions) { o.maxOutput = n }
}

// Name implements Tool.
func (t *execTool) Name() string { return ExecRunName }

// allowed applies the executable's subcommand gate before forwarding
// literal argv (never a shell string). Bare binaries are zero-arg only.
func allowed(b ExecBinary, args []string) bool {
	if len(b.Subcommands) == 0 {
		return len(args) == 0
	}
	if len(args) == 0 || len(args) > 32 || !slices.Contains(b.Subcommands, args[0]) {
		return false
	}
	for _, arg := range args {
		if arg == "" || len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return false
		}
	}
	return true
}

// childEnv builds the minimal scrubbed environment: PATH only, all
// FLOWSTATE_* stripped so a child cannot re-enter the harness.
func childEnv() []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	return []string{"PATH=" + path}
}

// cappedExecOutput discards bytes after the limit without growing memory.
type cappedExecOutput struct {
	bytes.Buffer
	limit    int64
	overflow bool
}

func (b *cappedExecOutput) Write(p []byte) (int, error) {
	remaining := b.limit - int64(b.Len())
	if remaining > 0 {
		keep := len(p)
		if int64(keep) > remaining {
			keep = int(remaining)
		}
		_, _ = b.Buffer.Write(p[:keep])
	}
	if int64(len(p)) > remaining {
		b.overflow = true
	}
	return len(p), nil
}

// waitExec honours the caller's deadline and reaps the child after a kill.
func waitExec(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		return ctx.Err()
	}
}

// run uses an absolute path resolved from trusted constructor policy,
// never argv0 supplied by the tool caller. It does not invoke a shell.
func (t *execTool) run(ctx context.Context, path string, args []string) (map[string]any, error) {
	cctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	cwd, err := filepath.EvalSymlinks(t.workdir)
	if err != nil {
		return nil, err
	}
	buf := &cappedExecOutput{limit: t.maxOut}
	cmd := &exec.Cmd{Path: path, Args: append([]string{path}, args...),
		Dir: cwd, Env: childEnv(), Stdout: buf, Stderr: buf}
	runErr := waitExec(cctx, cmd)
	if errors.Is(runErr, context.DeadlineExceeded) {
		return nil, fmt.Errorf("%w after %s", ErrExecTimeout, t.timeout)
	}
	if buf.overflow {
		return nil, fmt.Errorf("%w: exceeded %d bytes", ErrExecOversize, t.maxOut)
	}
	if errors.Is(runErr, context.Canceled) {
		return nil, runErr
	}
	exitCode, cerr := exitCodeFor(runErr)
	if cerr != nil {
		return nil, cerr
	}
	return map[string]any{"stdout": buf.String(), "exit_code": exitCode}, nil
}

// exitCodeFor maps a cmd.Run error to the child exit code; non-Exit
// errors (spawn failures) are returned as-is.
func exitCodeFor(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return 0, err
}

// Invoke reads {cmd,args} and runs the allow-listed binary in the
// session workdir; re-checks the env double gate (arm 2).
func (t *execTool) Invoke(ctx context.Context, args json.RawMessage) (any, error) {
	if !ExecEnabled() {
		return nil, ErrExecNotEnabled
	}
	var a execArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("tool: exec.run args: %w", err)
	}
	if a.Cmd == "" {
		return nil, fmt.Errorf("tool: exec.run cmd must be non-empty: %w", ErrExecNotAllowed)
	}
	b, ok := t.binaries[a.Cmd]
	if !ok || b.Path == "" {
		return nil, fmt.Errorf("%w: %s", ErrExecNotAllowed, a.Cmd)
	}
	if !allowed(b, a.Args) {
		return nil, fmt.Errorf("%w: %s %v", ErrExecDeniedSubcommand, a.Cmd, a.Args)
	}
	return t.run(ctx, b.Path, a.Args)
}
