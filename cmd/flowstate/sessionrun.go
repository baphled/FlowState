package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// providerResolver is the CLI's provider factory seam; tests swap it
// for an httptest-backed adapter, production uses the registry.
var providerResolver app.ProviderResolver = app.ResolveProvider

// errProvider marks provider failures (code provider_error, exit 3
// per CLI-AND-API-CONTRACT §1).
var errProvider = fmt.Errorf("provider_error")

// exitProviderError is the stable provider-failure exit code.
const exitProviderError = 3

// cmdRun implements `flowstate session run <id>`: runs one bounded
// provider turn, projects the persisted events as JSONL on stdout and
// ends with a terminal event envelope. Exit 0 completed; 1 unknown
// session; 2 usage; 3 provider error; 6 already terminal.
func cmdRun(ctx context.Context, st *app.Store, id string, fl *flags, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", app.ErrUsage))
		return exitUsage
	}
	if fl.budget < 0 || fl.maxTokens < 0 {
		writeError(stderr, fmt.Errorf("%w: --budget and --max-tokens must not be negative", app.ErrUsage))
		return exitUsage
	}
	sess, err := st.GetSession(ctx, id)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if sess.Status != "active" && sess.Status != "paused" {
		writeError(stderr, fmt.Errorf("%w: session %s is %s", app.ErrTerminal, id, sess.Status))
		return exitTerminal
	}
	p, err := providerResolver(sess.Provider)
	if err != nil {
		if errors.Is(err, app.ErrSessionCancelled) {
			writeError(stderr, fmt.Errorf("%w: interrupted; session cancelled", app.ErrSessionCancelled))
			return exitSigint
		}
		writeError(stderr, fmt.Errorf("%w: %v", errProvider, err))
		return exitProviderError
	}
	s, ok := p.(app.Streamer)
	if !ok {
		writeError(stderr, fmt.Errorf("%w: provider %s does not support streaming turns", errProvider, sess.Provider))
		return exitProviderError
	}
	res, err := app.TurnWithOptions(ctx, st, sess, s, app.TurnOptions{
		Budget:    fl.budget,
		MaxTokens: fl.maxTokens,
	})
	// Replay on a fresh context: after SIGINT the cancel path still
	// needs to project the persisted events before exiting 130.
	evts, rerr := st.Replay(context.WithoutCancel(ctx), id)
	if rerr != nil {
		writeError(stderr, rerr)
		return exitCodeFor(rerr)
	}
	for _, e := range evts {
		if err := writeJSON(stdout, map[string]any{
			"session_id":     e.SessionID,
			"seq":            e.Seq,
			"created_at_utc": e.CreatedAt.UTC().Format(timeFormat),
			"actor":          e.Actor,
			"type":           e.Type,
			"payload":        json.RawMessage(e.PayloadJSON),
			"payload_sha256": e.PayloadSHA,
		}); err != nil {
			writeError(stderr, err)
			return exitCodeFor(err)
		}
	}
	return finishRun(err, res, fl, stderr)
}

// finishRun maps the turn outcome onto its exit code: SIGINT
// cancellation exits 130, provider failures 3, budget stops 5.
func finishRun(err error, res *app.TurnResult, fl *flags, stderr *os.File) int {
	if err != nil {
		if errors.Is(err, app.ErrSessionCancelled) {
			writeError(stderr, fmt.Errorf("%w: interrupted; session cancelled", app.ErrSessionCancelled))
			return exitSigint
		}
		writeError(stderr, fmt.Errorf("%w: %v", errProvider, err))
		return exitProviderError
	}
	if res != nil && res.BudgetExceeded {
		writeError(stderr, fmt.Errorf("%w: budget of %d exceeded after %d tokens; session paused", app.ErrBudgetExceeded, fl.budget, res.ConsumedBudget))
		return exitBudget
	}
	return exitOK
}
