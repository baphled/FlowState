package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// cmdTool implements `flowstate session tool <id>`: executes one tool
// inside the session through the M1-TOOLS allowlist gate (deny by
// default) and records tool.call / tool.result events.
//
// Exit codes: 0 success or a recorded tool error outcome; 1 unknown
// session; 2 usage (unknown tool, denied tool, bad manifest); 6
// terminal session.
func cmdTool(ctx context.Context, st *store.Store, fl *flags, id string, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", session.ErrUsage))
		return exitUsage
	}
	if fl.tool == "" {
		writeError(stderr, fmt.Errorf("%w: --tool is required", session.ErrUsage))
		return exitUsage
	}
	allowlist, err := loadAllowlist(fl)
	if err != nil {
		writeError(stderr, err)
		return exitUsage
	}
	sess, err := st.GetSession(ctx, id)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if terminalStatus(sess.Status) {
		writeError(stderr, fmt.Errorf("%w: session %s is %s", session.ErrTerminal, id, sess.Status))
		return exitTerminal
	}
	w, err := st.NewEventWriter(ctx, id)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	defer w.Close()
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewEcho()); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	loop, err := tool.NewLoop(registry, w, allowlist)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	result, invokeErr := loop.Invoke(ctx, fl.tool, json.RawMessage(fl.toolArg))
	if invokeErr != nil {
		// Gate failures (unknown tool, deny, usage) map to the usage
		// exit; anything else keeps the generic mapping.
		if errors.Is(invokeErr, tool.ErrUnknownTool) ||
			errors.Is(invokeErr, tool.ErrDenied) ||
			errors.Is(invokeErr, tool.ErrEmptyName) {
			invokeErr = fmt.Errorf("%w: %v", session.ErrUsage, invokeErr)
		}
		writeError(stderr, invokeErr)
		return exitCodeFor(invokeErr)
	}
	out := map[string]any{"session_id": id, "tool": fl.tool, "ok": true, "result": result}
	if err := writeJSON(stdout, out); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}

// terminalStatus reports whether a session status is terminal
// (DATA-MODEL §7); mirrors session's terminal set.
func terminalStatus(status string) bool {
	switch status {
	case "completed", "cancelled", "failed":
		return true
	}
	return false
}

// loadAllowlist combines the manifest file (--allow-file, a JSON
// array of tool names) with repeated --allow flags; the union is the
// per-session allowlist. An empty allowlist denies everything.
func loadAllowlist(fl *flags) ([]string, error) {
	allow := append([]string{}, fl.allow...)
	if fl.allowFile == "" {
		return allow, nil
	}
	b, err := os.ReadFile(fl.allowFile)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read allowlist manifest %q: %v", session.ErrUsage, fl.allowFile, err)
	}
	var fromFile []string
	if err := json.Unmarshal(b, &fromFile); err != nil {
		return nil, fmt.Errorf("%w: allowlist manifest %q is not a JSON array: %v", session.ErrUsage, fl.allowFile, err)
	}
	return append(allow, fromFile...), nil
}
