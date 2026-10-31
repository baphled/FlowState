package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// providerResolver is the CLI's provider factory seam; tests swap it
// for an httptest-backed adapter, production uses the registry.
var providerResolver = app.ResolveProvider

// cmdRun implements `flowstate session run <id>`: runs one bounded
// provider turn, projects the persisted events as JSONL on stdout and
// ends with a terminal event envelope. Exit 0 completed; 1 unknown
// session; 2 usage; 3 provider error; 5 budget exceeded; 6 already
// terminal; 130 interrupted.
func cmdRun(ctx context.Context, st *app.Store, id string, fl *flags, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", app.ErrUsage))
		return exitUsage
	}
	outcome, err := app.RunTurn(ctx, st, id, fl.budget, providerResolver)
	if outcome != nil {
		for _, e := range outcome.Events {
			if werr := writeJSON(stdout, map[string]any{
				"session_id":     e.SessionID,
				"seq":            e.Seq,
				"created_at_utc": e.CreatedAt.UTC().Format(timeFormat),
				"actor":          e.Actor,
				"type":           e.Type,
				"payload":        json.RawMessage(e.PayloadJSON),
				"payload_sha256": e.PayloadSHA,
			}); werr != nil {
				writeError(stderr, werr)
				return exitCodeFor(werr)
			}
		}
	}
	if err != nil {
		if outcome != nil && outcome.Interrupted {
			return cancelOnInterrupt(ctx, st, id)
		}
		if app.ErrorCode(err) == app.CodeProvider {
			writeError(stderr, err)
			return exitProviderError
		}
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if outcome != nil && outcome.Result != nil && outcome.Result.BudgetExceeded {
		writeError(stderr, app.ErrBudgetExceeded)
		return exitBudgetExceeded
	}
	return exitOK
}

// cancelOnInterrupt handles SIGINT during `session run`: it cancels
// the session (session.cancelled, status cancelled) and returns the
// interrupt exit code; a terminal session maps to exit 6.
func cancelOnInterrupt(ctx context.Context, st *app.Store, id string) int {
	if err := app.CancelSession(ctx, st, id); err != nil {
		writeError(os.Stderr, err)
		return exitCodeFor(err)
	}
	return exitInterrupted
}
