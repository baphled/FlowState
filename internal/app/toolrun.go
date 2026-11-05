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
// builds the registry (echo plus fs.read/fs.write confined to the
// session workdir), gates through the allowlist and records events.
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
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewEcho()); err != nil {
		return nil, err
	}
	// --workdir defaults to the store's session directory (decision
	// per principal verdict: acceptable for M2 while tools run only
	// within the CLI process lifetime; a persisted schema field
	// becomes mandatory once tools outlive the process).
	workdir := req.Workdir
	if workdir == "" {
		workdir = filepath.Join(SessionWorkdirRoot(dbPath), req.SessionID)
	}
	for _, t := range []tool.Tool{tool.NewFSRead(workdir), tool.NewFSWrite(workdir)} {
		if err := registry.Register(t); err != nil {
			return nil, err
		}
	}
	loop, err := tool.NewLoop(registry, w, allowlist)
	if err != nil {
		return nil, err
	}
	result, invokeErr := loop.Invoke(ctx, req.Tool, json.RawMessage(req.ToolArg))
	if invokeErr != nil {
		// Gate failures (unknown tool, deny, usage) map to usage for
		// the CLI; anything else keeps the generic mapping.
		if errors.Is(invokeErr, tool.ErrUnknownTool) ||
			errors.Is(invokeErr, tool.ErrDenied) ||
			errors.Is(invokeErr, tool.ErrEmptyName) {
			invokeErr = fmt.Errorf("%w: %v", session.ErrUsage, invokeErr)
		}
		return nil, invokeErr
	}
	return &ToolOutcome{SessionID: req.SessionID, Tool: req.Tool, Result: result}, nil
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
