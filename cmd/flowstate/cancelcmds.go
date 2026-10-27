package main

import (
	"context"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// cmdCancel implements `flowstate session cancel <id>`: explicit
// cancel (M1-TERMINAL). Persists the `session.cancelled` event and
// terminal status, then prints the state JSON.
//
// Exit codes per CLI contract §1: 0 cancelled; 1 unknown session; 6
// already terminal; 130 when the invocation itself was interrupted
// (context cancelled) — the cancellation is still persisted in that
// case.
func cmdCancel(ctx context.Context, st *store.Store, id, reason string, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", session.ErrUsage))
		return exitUsage
	}
	if err := session.Cancel(ctx, st, id, reason); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	sess, err := st.GetSession(context.Background(), id)
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
