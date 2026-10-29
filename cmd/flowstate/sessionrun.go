package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/registry"
	"github.com/baphled/flowstate/internal/runtime"
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

// errBudgetExceeded marks budget-cap stops (code budget_exceeded,
// exit 5 per CLI-AND-API-CONTRACT §1).
var errBudgetExceeded = errors.New("budget_exceeded")

// errSessionCancelled marks interrupt-driven cancellation (code
// session_cancelled, exit 130).
var errSessionCancelled = errors.New("session_cancelled")

// cmdRun implements `flowstate session run <id>`: runs one bounded
// provider turn, projects the persisted events as JSONL on stdout and
// ends with a terminal event envelope. Exit 0 completed; 1 unknown
// session; 2 usage; 3 provider error; 5 budget exceeded; 6 already
// terminal; 130 interrupted.
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
	if sess.Status == "paused" {
		if rerr := resumeSession(ctx, st, id); rerr != nil {
			writeError(stderr, rerr)
			return exitCodeFor(rerr)
		}
		sess.Status = "active"
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
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	res, err := runtime.TurnWithOptions(sctx, st, sess, streamer, runtime.TurnOptions{Budget: fl.budget})
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
		if sctx.Err() != nil {
			return cancelOnInterrupt(ctx, st, id)
		}
		writeError(stderr, fmt.Errorf("%w: %v", errProvider, err))
		return exitProviderError
	}
	if res != nil && res.BudgetExceeded {
		writeError(stderr, errBudgetExceeded)
		return exitBudgetExceeded
	}
	return exitOK
}

// resumeSession persists session.resumed (actor user) and flips the
// session back to active before any new turn work events.
func resumeSession(ctx context.Context, st *store.Store, id string) error {
	w, err := st.NewEventWriter(ctx, id)
	if err != nil {
		return err
	}
	defer w.Close()
	return w.Resume(ctx)
}

// cancelOnInterrupt handles SIGINT during `session run`: it cancels
// the session (session.cancelled, status cancelled) and returns the
// interrupt exit code; a terminal session maps to exit 6.
func cancelOnInterrupt(ctx context.Context, st *store.Store, id string) int {
	if _, err := session.Close(ctx, st, id); err != nil {
		writeError(os.Stderr, err)
		return exitCodeFor(err)
	}
	writeError(os.Stderr, errSessionCancelled)
	return exitInterrupted
}
