package app

// The tool.before gate of ADR-012 packet 3a and the hooks.loaded record. A hook never runs on replay: the
// engine writes hook.invoked and hook.result as data, and rebuild reads them as data.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// Hook event types (ADR-012, The event log).
const (
	// TypeHooksLoaded is the once-per-root-run record of the effective hook set.
	TypeHooksLoaded = "hooks.loaded"
	// TypeHookInvoked is written before a hook runs.
	TypeHookInvoked = "hook.invoked"
	// TypeHookResult is written after a hook returns.
	TypeHookResult = "hook.result"
)

// hookStreamCap bounds the stdout and stderr copied into hook.result (2 KiB each).
const hookStreamCap = 2 << 10

// hookSetHooks is the hooks a HookSet carries; a set that is not a LoadedHooks (a spec fake with only a
// Digest) carries none.
func hookSetHooks(set HookSet) []Hook {
	switch v := set.(type) {
	case LoadedHooks:
		return v.Hooks
	case *LoadedHooks:
		return v.Hooks
	}
	return nil
}

// newHookGate builds the tool.before gate of one call, or nil when the run has no tool.before hook.
func newHookGate(cfg ExecLoopConfig, call provider.ToolCall, events tool.EventSink) tool.CallGate {
	var hooks []Hook
	for _, h := range hookSetHooks(cfg.Hooks) {
		if h.Point == HookToolBefore {
			hooks = append(hooks, h)
		}
	}
	if len(hooks) == 0 {
		return nil
	}
	return &hookGate{hooks: hooks, events: events, input: hookInput{
		session: cfg.Session.ID, runID: strconv.FormatInt(cfg.Lease.Token(), 10), agent: cfg.Agent, depth: cfg.Depth,
		cwd: cfg.Workdir, callID: call.ID,
	}}
}

// Before runs the tool.before hooks that match the tool, sequentially in file order. The first deny ends the
// chain and its hook id is returned; a failure is decided by the hook's on_failure.
func (g *hookGate) Before(ctx context.Context, name string, args json.RawMessage) (string, error) {
	stdin, ierr := g.document(name, args)
	for _, h := range g.hooks {
		if !hookMatches(h, name) {
			continue
		}
		deny, err := g.invoke(ctx, h, stdin, ierr)
		if err != nil || deny {
			return h.ID, err
		}
	}
	return "", nil
}

// invoke records hook.invoked, runs one hook and records hook.result.
func (g *hookGate) invoke(ctx context.Context, h Hook, stdin []byte, inputErr error) (bool, error) {
	sum := sha256.Sum256(stdin)
	if err := g.append(ctx, TypeHookInvoked, map[string]any{
		"point": string(h.Point), "id": h.ID, "kind": string(h.Kind), "input_sha256": hex.EncodeToString(sum[:]),
		"tool_call_id": g.input.callID,
	}); err != nil {
		return false, err
	}
	res := g.run(ctx, h, stdin, inputErr)
	deny := res.Verdict == tool.HookDeny || res.Verdict == tool.HookFailure && h.OnFailure == HookOnFailureDeny
	return deny, g.append(ctx, TypeHookResult, hookResultPayload(h, res, g.input.callID))
}

// run starts the hook, or reports why it cannot: an unreadable stdin document, a handler kind this build does
// not run.
func (g *hookGate) run(ctx context.Context, h Hook, stdin []byte, inputErr error) tool.HookResult {
	switch {
	case inputErr != nil:
		return tool.HookResult{Verdict: tool.HookFailure, Failure: "invalid_input", ExitCode: -1}
	case h.Kind != HookKindCommand:
		return tool.HookResult{Verdict: tool.HookFailure, Failure: "kind_unsupported", ExitCode: -1}
	}
	argv := append([]string{h.Executable}, h.Argv[1:]...)
	return g.runner.Run(ctx, tool.HookCommand{Argv: argv, Dir: g.input.cwd, EnvNames: h.Env,
		Timeout: time.Duration(h.TimeoutMS) * time.Millisecond}, stdin)
}

// document is the JSON a hook reads on stdin (ADR-012, Handler kinds).
func (g *hookGate) document(name string, args json.RawMessage) ([]byte, error) {
	if len(args) == 0 {
		args = json.RawMessage("null")
	}
	return json.Marshal(map[string]any{
		"version": 1, "point": string(HookToolBefore), "session_id": g.input.session, "run_id": g.input.runID,
		"agent": g.input.agent, "depth": g.input.depth, "cwd": g.input.cwd,
		"tool": map[string]any{"name": name, "call_id": g.input.callID, "args": args},
	})
}

// append writes one hook event through the parent writer; a failure ends the run.
func (g *hookGate) append(ctx context.Context, typ string, payload map[string]any) error {
	if _, err := g.events.Append(ctx, session.ActorEngine, typ, payload); err != nil {
		return fmt.Errorf("%w: %s: %w", tool.ErrEventAppend, typ, err)
	}
	return nil
}

// hookMatches reports whether the hook's matchers select the tool; no matcher selects every tool.
func hookMatches(h Hook, name string) bool {
	if len(h.Match) == 0 {
		return true
	}
	for _, pattern := range h.Match {
		if ok, err := path.Match(pattern, name); err == nil && ok {
			return true
		}
	}
	return false
}

// hookResultPayload is the hook.result payload: the verdict, and for a failure the code and the policy that
// was applied. stdout and stderr are already stripped of control characters by the runner.
func hookResultPayload(h Hook, r tool.HookResult, callID string) map[string]any {
	payload := map[string]any{
		"point": string(h.Point), "id": h.ID, "decision": hookDecisionName(r.Verdict),
		"duration_ms": r.Duration.Milliseconds(), "exit_code": r.ExitCode, "tool_call_id": callID,
		"stdout": truncateText(r.Stdout, hookStreamCap), "stderr": truncateText(r.Stderr, hookStreamCap),
	}
	if r.Reason != "" {
		payload["reason"] = r.Reason
	}
	if r.Verdict == tool.HookFailure {
		payload["failure"], payload["on_failure"] = r.Failure, h.OnFailure
	}
	return payload
}

// hookDecisionName maps the runner's verdict to the decision names of hook.result.
func hookDecisionName(v tool.HookVerdict) string {
	if v == tool.HookFailure {
		return "failed"
	}
	return string(v)
}

// truncateText cuts s to at most limit bytes on a character boundary.
func truncateText(s string, limit int) string {
	for len(s) > limit {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}

// loadRunHooks resolves the hook set of a root run: the set already carried by the request, else the set the
// request's scope selects (never loaded for an unset scope), found under the project root. loaded is true when
// this call read the files, which is when hooks.loaded is owed.
func loadRunHooks(carried HookSet, scope HookScope, projectRoot string) (set HookSet, loaded bool, err error) {
	if carried = emptyHookSet(carried); carried != nil || scope == "" {
		return carried, false, nil
	}
	l, err := LoadHooks(HooksRequest{Scope: scope, ProjectRoot: projectRoot, Accepted: AcceptedHooks()})
	if err != nil {
		return nil, false, err
	}
	return l, true, nil
}

// bindRunHooks resolves the hook set of a root run and, when it read the files, records hooks.loaded.
func bindRunHooks(ctx context.Context, st *store.Store, sessionID string, req SessionExecLoopRequest) (HookSet, error) {
	set, loaded, err := loadRunHooks(req.Hooks, req.HooksScope, HooksProjectRoot(req))
	if err != nil || !loaded {
		return set, err
	}
	return set, recordHooksLoaded(ctx, st, sessionID, set)
}

// recordHooksLoaded writes hooks.loaded, the record of the set a root run applies, through a writer of its own.
func recordHooksLoaded(ctx context.Context, st *store.Store, sessionID string, set HookSet) error {
	l, _ := set.(LoadedHooks)
	w, err := st.NewEventWriter(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("hooks.loaded writer: %w", err)
	}
	defer func() { _ = w.Close() }()
	if _, err := w.Append(ctx, session.ActorEngine, TypeHooksLoaded, hooksLoadedPayload(l)); err != nil {
		return fmt.Errorf("record hooks.loaded: %w", err)
	}
	return nil
}

// hooksLoadedPayload is the hooks.loaded payload: the scope, the set digest, the ids, and per hook its
// source, declaring file, resolved executable and the executable's sha256. skipped and inert are the lists
// of ADR-012; both are empty until profile hooks and run-level points exist.
func hooksLoadedPayload(l LoadedHooks) map[string]any {
	ids, hooks := make([]string, 0, len(l.Hooks)), make([]map[string]any, 0, len(l.Hooks))
	for _, h := range l.Hooks {
		ids = append(ids, h.ID)
		hooks = append(hooks, map[string]any{"id": h.ID, "source": string(h.Source), "file": h.Origin,
			"path": h.Executable, "sha256": fileSHA256(h.Executable)})
	}
	return map[string]any{"scope": string(l.Scope), "set_sha256": l.Digest(), "ids": ids, "hooks": hooks,
		"skipped": []any{}, "inert": []any{}}
}

// fileSHA256 is the hex sha256 of a file, or "" when it cannot be read.
func fileSHA256(p string) string {
	root, err := os.OpenRoot(filepath.Dir(p))
	if err != nil {
		return ""
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(filepath.Base(p))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}
