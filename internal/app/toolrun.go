package app

// CLI-facing tool invocation use-case for `flowstate session tool`:
// executes one tool inside a session through the M1-TOOLS allowlist
// gate (deny by default) and records tool.call / tool.result events.
// The CLI layer owns flag parsing and exit-code mapping only.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// RunSessionToolFromPath opens the store at dbPath and runs
// RunSessionTool; the CLI adapter uses this to avoid importing the
// store package directly.
func RunSessionToolFromPath(ctx context.Context, dbPath string, req ToolRequest) (*ToolOutcome, error) {
	st, err := OpenStore(dbPath)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	return RunSessionTool(ctx, st, dbPath, req)
}

// RunSessionTool validates the request against the session state,
// builds the session builtin registry, gates through the allowlist and records
// events. Filesystem tools remain confined to the session workdir.
// Usage/terminal/denied failures wrap session sentinel errors.
func RunSessionTool(ctx context.Context, st *store.Store, dbPath string, req ToolRequest) (*ToolOutcome, error) {
	if req.SessionID == "" {
		return nil, fmt.Errorf("%w: session id required", session.ErrUsage)
	}
	if req.Tool == "" {
		return nil, fmt.Errorf("%w: --tool is required", session.ErrUsage)
	}
	allowlist, err := loadToolAllowlist(req)
	if err != nil {
		return nil, err
	}
	sess, err := st.GetSession(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	if toolTerminalStatus(sess.Status) {
		return nil, fmt.Errorf("%w: session %s is %s", session.ErrTerminal, req.SessionID, sess.Status)
	}
	w, err := st.NewEventWriter(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	loop, err := newSessionToolLoop(w, dbPath, req, allowlist)
	if err != nil {
		return nil, err
	}
	result, invokeErr := loop.Invoke(ctx, req.Tool, json.RawMessage(req.ToolArg))
	if invokeErr != nil {
		return nil, wrapGateFailure(invokeErr)
	}
	return &ToolOutcome{SessionID: req.SessionID, Tool: req.Tool, Result: result}, nil
}

// newSessionToolLoop selects the session workdir and composes the shared builtin
// registry with the allowlist-gated loop.
func newSessionToolLoop(w tool.EventSink, dbPath string, req ToolRequest, allowlist []string) (*tool.Loop, error) {
	// --workdir defaults to the store's session directory (decision
	// per principal verdict: acceptable for M2 while tools run only
	// within the CLI process lifetime; a persisted schema field
	// becomes mandatory once tools outlive the process).
	workdir := req.Workdir
	if workdir == "" {
		workdir = filepath.Join(SessionWorkdirRoot(dbPath), req.SessionID)
	}
	registry, err := NewSessionToolRegistry(workdir)
	if err != nil {
		return nil, err
	}
	return tool.NewLoop(registry, w, allowlist)
}

// wrapGateFailure maps allowlist-gate failures (unknown tool, deny,
// usage) to session usage errors for the CLI; anything else passes
// through unchanged.
func wrapGateFailure(err error) error {
	if errors.Is(err, tool.ErrUnknownTool) ||
		errors.Is(err, tool.ErrDenied) ||
		errors.Is(err, tool.ErrEmptyName) {
		return fmt.Errorf("%w: %v", session.ErrUsage, err)
	}
	return err
}

// SessionWorkdirRoot returns the per-session working-directory root
// beside the store database (same parent directory as the db file).
func SessionWorkdirRoot(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "sessions")
}

// toolTerminalStatus reports whether a session status is terminal
// (DATA-MODEL §7); mirrors session's terminal set.
func toolTerminalStatus(status string) bool {
	switch status {
	case "completed", "cancelled", "failed":
		return true
	}
	return false
}

// loadToolAllowlist combines the manifest file (a JSON array of tool
// names) with inline allow entries; the union is the per-session
// allowlist. An empty allowlist denies everything.
func loadToolAllowlist(req ToolRequest) ([]string, error) {
	allow := append([]string{}, req.Allow...)
	if req.AllowFile == "" {
		return allow, nil
	}
	b, err := os.ReadFile(req.AllowFile)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read allowlist manifest %q: %v", session.ErrUsage, req.AllowFile, err)
	}
	var fromFile []string
	if err := json.Unmarshal(b, &fromFile); err != nil {
		return nil, fmt.Errorf("%w: allowlist manifest %q is not a JSON array: %v", session.ErrUsage, req.AllowFile, err)
	}
	return append(allow, fromFile...), nil
}
