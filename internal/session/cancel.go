package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/baphled/flowstate/internal/store"
)

// ErrContextCancelled is returned when a turn is aborted because the
// caller's context was cancelled mid-turn (e.g. SIGINT); the session
// itself has been durably marked cancelled before this error is
// returned (M1-TERMINAL).
var ErrContextCancelled = errors.New("session: context cancelled mid-turn")

// Cancel requests cancellation of a session (M1-TERMINAL). When the
// caller's context is already cancelled — a mid-turn interrupt — the
// cancellation is persisted with a reason derived from the context
// error before ErrContextCancelled is returned, mapping the
// interruption onto a durable `session.cancelled` event and terminal
// `cancelled` status. With a live context it performs a plain cancel.
//
// Cancelling an already-terminal session fails with ErrTerminal
// (terminal-once; store.ErrSessionTerminal is mapped onto the CLI
// error contract); an unknown id fails with store.ErrUnknownSession.
func Cancel(ctx context.Context, st *store.Store, id, reason string) error {
	if err := ctx.Err(); err != nil {
		persistReason := "context cancelled"
		if reason != "" {
			persistReason = reason + " (" + err.Error() + ")"
		}
		serr := persistCancelIgnoringCtx(ctx, st, id, persistReason)
		if serr != nil {
			return serr
		}
		return fmt.Errorf("%w: %v", ErrContextCancelled, err)
	}
	return persistCancel(ctx, st, id, reason)
}

// persistCancel commits the terminal cancelled state, mapping the
// store's terminal error onto the session error contract.
func persistCancel(ctx context.Context, st *store.Store, id, reason string) error {
	_, err := st.CancelSession(ctx, id, reason)
	if errors.Is(err, store.ErrSessionTerminal) {
		return fmt.Errorf("%w: session %s", ErrTerminal, id)
	}
	return err
}

// persistCancelIgnoringCtx persists the cancellation with a fresh
// bounded context so an interrupted parent ctx cannot abort the
// durable write itself; the recorded interruption still fails the
// call for the caller.
func persistCancelIgnoringCtx(ctx context.Context, st *store.Store, id, reason string) error {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return persistCancel(persistCtx, st, id, reason)
}
