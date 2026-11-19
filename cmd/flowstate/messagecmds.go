package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/baphled/flowstate/internal/app"
)

// cmdMessage records one steering directive addressed to the child
// session id positional: `session message <child> --from <sender>
// --body <text>`. The sender must be an ancestor in the child's
// delegation chain; the message drains at the child's next run.
func cmdMessage(ctx context.Context, st *app.Store, id string, fl *flags, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session message requires a child session id", app.ErrUsage))
		return exitUsage
	}
	if fl.msgFrom == "" || strings.TrimSpace(fl.msgBody) == "" {
		writeError(stderr, fmt.Errorf("%w: --from and --body are required", app.ErrUsage))
		return exitUsage
	}
	msg, err := app.SendSteeringMessage(ctx, st, id, fl.msgFrom, fl.msgBody)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if err := writeJSON(stdout, map[string]any{
		"id": msg.ID, "session_id": msg.SessionID, "sender": msg.Sender,
	}); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}
