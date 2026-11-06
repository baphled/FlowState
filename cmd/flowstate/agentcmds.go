package main

// agentcmds.go: CLI adapter for `flowstate session agent <id>` — a
// bounded multi-round tool-driving agent turn (M2 SDLC surface).
// The CLI parses flags and maps exit codes; behaviour lives in
// internal/app.AgentTurnWithOptions.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// agentAllowFileDefault is the allowlist manifest the agent turn
// uses when --allow-file is not supplied: the four SDLC verbs plus
// exec.run, deny-by-default for anything else.
var agentAllowFileDefault = []string{"fs.read", "fs.write", "fs.edit", "exec.run"}

// cmdAgent implements `flowstate session agent <id>`: resolves the
// provider, runs one bounded agent turn with the allowlisted tool
// surface and projects the persisted events as JSONL.
//
// Exit codes: 0 completed; 1 unknown session; 2 usage; 3 provider
// error; 4 gates closed (live not opted in); 5 budget exceeded; 6
// terminal session.
func cmdAgent(ctx context.Context, dbPath string, fl *flags, id string, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session agent requires a session id", app.ErrUsage))
		return exitUsage
	}
	if !liveGate() {
		writeError(stderr, fmt.Errorf("%w: agent turn requires both ZAI_API_KEY and FLOWSTATE_ALLOW_LIVE=1", app.ErrConfigInvalid))
		return exitConfigInvalid
	}
	allow := fl.allow
	if len(allow) == 0 && fl.allowFile == "" {
		allow = agentAllowFileDefault
	}
	st, err := app.OpenStore(dbPath)
	if err != nil {
		writeError(stderr, err)
		return exitConfigInvalid
	}
	defer st.Close()
	sess, err := st.GetSession(ctx, id)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if sess.Status != "active" && sess.Status != "paused" {
		writeError(stderr, fmt.Errorf("%w: session %s is %s", app.ErrTerminal, id, sess.Status))
		return exitTerminal
	}
	p, perr := providerResolver(sess.Provider)
	if perr != nil {
		writeError(stderr, fmt.Errorf("%w: %v", errProvider, perr))
		return exitProviderError
	}
	s, ok := p.(app.Streamer)
	if !ok {
		writeError(stderr, fmt.Errorf("%w: provider %s does not support streaming turns", errProvider, sess.Provider))
		return exitProviderError
	}
	budget := fl.budget
	if budget == 0 {
		budget = agentDefaultBudget
	}
	res, terr := app.AgentTurnWithOptions(ctx, st, sess, s, app.AgentTurnOptions{
		Allow:     allow,
		Workdir:   fl.workdir,
		DBPath:    dbPath,
		Budget:    budget,
		MaxTokens: fl.maxTokens,
		ModelID:   fl.model,
	})
	if terr != nil && !errors.Is(terr, app.ErrBudgetExceeded) {
		writeError(stderr, fmt.Errorf("%w: %v", errProvider, terr))
		return exitProviderError
	}
	if res != nil && res.BudgetExceeded {
		writeError(stderr, fmt.Errorf("%w: agent budget of %d exceeded after %d bytes", app.ErrBudgetExceeded, budget, res.Consumed))
		return exitBudget
	}
	payload := map[string]any{"session_id": res.SessionID, "final_content": res.FinalContent, "rounds": res.Rounds, "tool_calls": res.ToolCalls}
	if err := writeJSON(stdout, payload); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}

// agentDefaultBudget is the hard cap applied when --budget is not
// supplied on an agent turn: agent turns may never run unlimited.
const agentDefaultBudget = 50000
