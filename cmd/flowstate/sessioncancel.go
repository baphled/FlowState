package main

import (
	"context"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// cancelSession cancels one session and emits its state JSON.
func cancelSession(ctx context.Context, st *app.Store, id string, stdout, stderr *os.File) int {
	sess, err := app.CancelSession(ctx, st, id)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if err := writeJSON(stdout, map[string]any{"id": sess.ID, "status": sess.Status}); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}

// cmdCancel implements `flowstate session cancel <id>`: transitions
// an active or paused session to cancelled with a session.cancelled
// event. Exit 0 cancelled; 1 unknown session; 2 usage; 6 terminal;
// 130 when invoked as the SIGINT cancel path.
func cmdCancel(ctx context.Context, st *app.Store, id string, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", app.ErrUsage))
		return exitUsage
	}
	if ctx.Value(sigintCancelledKey{}) != nil {
		if _, err := app.CancelSession(ctx, st, id); err != nil {
			writeError(stderr, err)
			return exitCodeFor(err)
		}
		writeError(stderr, fmt.Errorf("%w: interrupted; session cancelled", app.ErrSessionCancelled))
		return exitSigint
	}
	return cancelSession(ctx, st, id, stdout, stderr)
}

// contextWithSigint marks a context as SIGINT-cancelled; test-only
// helper mirroring dispatchContext's marking.
func contextWithSigint() context.Context {
	return context.WithValue(context.Background(), sigintCancelledKey{}, true)
}
