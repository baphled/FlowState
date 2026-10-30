package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/registry"
	"github.com/baphled/flowstate/internal/runtime"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// ErrConfigInvalid marks configuration failures (code
// config_invalid); the cmd adapter wraps database-open failures in it.
var ErrConfigInvalid = errors.New("config_invalid")

// ResolveProvider resolves a provider by name through the registry
// (the only place a concrete adapter is selected).
func ResolveProvider(name string) (provider.Provider, error) {
	return registry.Resolve(name)
}

// OpenStore opens (creating directories for) the SQLite database at
// path; ":memory:" opens the in-process database.
func OpenStore(path string) (*store.Store, error) {
	if path != ":memory:" {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	return store.Open(path)
}

// ResumeSession persists session.resumed (actor user) and flips the
// session back to active before any new turn work events.
func ResumeSession(ctx context.Context, st *store.Store, id string) error {
	w, err := st.NewEventWriter(ctx, id)
	if err != nil {
		return err
	}
	defer w.Close()
	return w.Resume(ctx)
}

// RunTurn runs one bounded provider turn for session id after the
// resume/status preconditions, wiring SIGINT cancellation. It returns
// the full replayed event log, the engine turn result and the turn
// error (if any); Interrupted reports an interrupt-driven stop.
func RunTurn(ctx context.Context, st *store.Store, id string, budget int64, resolve ProviderFactory) (*TurnOutcome, error) {
	sess, err := st.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if sess.Status != "active" && sess.Status != "paused" {
		return nil, fmt.Errorf("%w: session %s is %s", session.ErrTerminal, id, sess.Status)
	}
	if sess.Status == "paused" {
		if rerr := ResumeSession(ctx, st, id); rerr != nil {
			return nil, rerr
		}
		sess.Status = "active"
	}
	p, err := resolve(sess.Provider)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProvider, err)
	}
	streamer, ok := p.(provider.Streamer)
	if !ok {
		return nil, fmt.Errorf("%w: provider %s does not support streaming turns", ErrProvider, sess.Provider)
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
	res, err := TurnWithOptions(sctx, st, sess, streamer, runtime.TurnOptions{Budget: budget})
	outcome := &TurnOutcome{Result: res, Interrupted: sctx.Err() != nil && err != nil}
	evts, rerr := st.Replay(ctx, id)
	if rerr != nil {
		return outcome, rerr
	}
	outcome.Events = evts
	return outcome, err
}

// CancelSession handles SIGINT during `session run`: it cancels the
// session (session.cancelled, status cancelled) and reports the
// cancellation sentinel for the caller to render.
func CancelSession(ctx context.Context, st *store.Store, id string) error {
	if _, err := session.Close(ctx, st, id); err != nil {
		return err
	}
	return ErrSessionCancelled
}
