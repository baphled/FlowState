package app

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/store"
)

// SendSteeringMessage records one durable steering directive from
// sender (an ancestor session in the recipient's delegation chain) to
// the child. Unknown sessions map to ErrUsage; delegation-scope
// violations map to ErrUsage; the store sentinels stay distinct.
func SendSteeringMessage(ctx context.Context, st *Store, sessionID, sender, body string) (store.SessionSteeringMessage, error) {
	msg, err := st.SendMessage(ctx, sessionID, sender, body)
	if err != nil {
		return store.SessionSteeringMessage{}, fmt.Errorf("%w: %v", ErrUsage, err)
	}
	return msg, nil
}

// DrainSteeringMessages drains every pending steering message for the
// session into its event log as session.steered events (actor user,
// payload sender/body) and marks the rows delivered, all in one
// transaction. Called in the run preamble before turn work, so a
// directive lands at the next turn boundary without interrupting a
// live provider stream.
func DrainSteeringMessages(ctx context.Context, st *Store, sessionID string) (int, error) {
	return st.DrainSteeringMessagesFor(ctx, sessionID)
}
