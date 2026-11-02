package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/registry"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// providerResolver is the CLI's provider factory seam; tests swap it
// for an httptest-backed adapter, production uses the registry.
var providerResolver = defaultProviderResolver

// defaultProviderResolver resolves a provider by name through the
// registry (the only place a concrete adapter is selected).
func defaultProviderResolver(name string) (provider.Provider, error) {
	return registry.Resolve(name)
}

// errProvider marks provider failures (code provider_error, exit 3
// per CLI-AND-API-CONTRACT §1).
var errProvider = fmt.Errorf("provider_error")

// exitProviderError is the stable provider-failure exit code.
const exitProviderError = 3

// exitBudgetStop is the stable budget-pause exit code (contract §1:
// `session run` exits 5 on a budget stop, which is not terminal).
const exitBudgetStop = 5

// errBudgetExceeded marks budget-stop failures (code budget_exceeded
// of the closed error set in the contract §2).
var errBudgetExceeded = fmt.Errorf("budget_exceeded")

// budgetCap resolves the effective turn cap from the --budget and
// --max-tokens flags: both bound the turn, so when both are set the
// tighter (minimum) applies; <=0 means unset (unlimited).
func budgetCap(fl *flags) int64 {
	c := fl.budget
	if fl.maxTokens > 0 && (c <= 0 || fl.maxTokens < c) {
		c = fl.maxTokens
	}
	return c
}

// cmdRun implements `flowstate session run <id>`: runs one bounded
// provider turn through the app turn loop (budget-aware), projects
// the persisted events as JSONL on stdout and ends with a terminal
// event envelope. Exit 0 completed; 1 unknown session; 2 usage;
// 3 provider error; 5 budget stop (session.paused, not terminal);
// 6 already terminal.
func cmdRun(ctx context.Context, st *store.Store, id string, fl *flags, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", session.ErrUsage))
		return exitUsage
	}
	sess, err := st.GetSession(ctx, id)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if sess.Status != "active" && sess.Status != "paused" {
		writeError(stderr, fmt.Errorf("%w: session %s is %s", session.ErrTerminal, id, sess.Status))
		return exitTerminal
	}
	p, err := providerResolver(sess.Provider)
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: %v", errProvider, err))
		return exitProviderError
	}
	streamer, ok := p.(provider.Streamer)
	if !ok {
		writeError(stderr, fmt.Errorf("%w: provider %s does not support streaming turns", errProvider, sess.Provider))
		return exitProviderError
	}
	res, err := app.TurnWithOptions(ctx, st, sess, streamer, app.TurnOptions{Budget: budgetCap(fl)})
	_ = res
	evts, rerr := st.Replay(ctx, id)
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
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: %v", errProvider, err))
		return exitProviderError
	}
	if res != nil && res.BudgetExceeded {
		writeError(stderr, fmt.Errorf("%w: budget stop after %d units; session paused, resume with session run %s", errBudgetExceeded, res.ConsumedBudget, id))
		return exitBudgetStop
	}
	return exitOK
}
