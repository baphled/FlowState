package app

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// DefaultHarness re-exports the session harness label for CLI composition.
const DefaultHarness = session.DefaultHarness

// TypeSessionDelegated is the event opening a child session's log,
// attributing the delegation to the user (M2 child sessions).
const TypeSessionDelegated = "session.delegated"

// delegatedParentTerminal marks parent statuses that reject delegation.
var delegatedParentTerminal = map[string]bool{
	"completed": true, "cancelled": true, "failed": true,
}

// CreateSession composes session creation over the concrete store: the
// CLI commands wire this package, never the session or store packages
// directly (owning-layer contract).
func CreateSession(ctx context.Context, st *Store, provider, modelID, harness, configSHA, task string) (*store.Session, error) {
	return session.Create(ctx, st, provider, modelID, harness, configSHA, task)
}

// CreateChildSession composes delegated child-session creation over
// the concrete store: the parent must exist and be non-terminal (else
// ErrUsage), the child id is a fresh UUIDv7, and the child log opens
// with a session.delegated event (actor user, payload parent_id)
// before the initial turn.start carrying the task.
func CreateChildSession(ctx context.Context, st *Store, parentID, provider, modelID, harness, configSHA, task string) (*store.Session, error) {
	if parentID == "" || provider == "" || modelID == "" || task == "" {
		return nil, fmt.Errorf("%w: parent, provider, model and task must not be empty", ErrUsage)
	}
	parent, err := st.GetSession(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("%w: unknown parent session %s", ErrUsage, parentID)
	}
	if delegatedParentTerminal[parent.Status] {
		return nil, fmt.Errorf("%w: parent session %s is %s", ErrUsage, parentID, parent.Status)
	}
	if harness == "" {
		harness = DefaultHarness
	}
	id, err := session.NewID()
	if err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}
	child, err := st.CreateChildSession(ctx, id, parentID, provider, modelID, harness, configSHA)
	if err != nil {
		return nil, err
	}
	w, err := st.NewEventWriter(ctx, child.ID)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	if _, err := w.Append(ctx, session.ActorUser, TypeSessionDelegated, map[string]any{"parent_id": parentID}); err != nil {
		return nil, err
	}
	if _, err := w.Append(ctx, session.ActorUser, session.TypeTurnStart, map[string]any{"task": task}); err != nil {
		return nil, err
	}
	return child, nil
}

// ListSessions composes the session listing for the CLI surface.
func ListSessions(ctx context.Context, st *Store, status string) ([]store.Session, error) {
	return session.List(ctx, st, status)
}

// InspectSession composes the session/event inspection for the CLI surface.
func InspectSession(ctx context.Context, st *Store, id string) (*store.Session, []store.RecordedEvent, error) {
	return session.Inspect(ctx, st, id)
}

// CloseSession composes explicit session close for the CLI surface.
func CloseSession(ctx context.Context, st *Store, id string) (*store.Session, error) {
	sess, err := session.Close(ctx, st, id)
	if err == nil {
		settleReservation(ctx, st, id)
	}
	return sess, err
}

// ReserveChildBudget reserves grant budget for childID against the
// parent's declared pool. Pool-missing and pool-exceeded failures map
// to ErrUsage so the CLI surfaces exit 2; store sentinels stay
// distinct inside the wrapped chain.
func ReserveChildBudget(ctx context.Context, st *Store, parentID, childID string, grant int64) error {
	if _, err := st.ReserveBudget(ctx, parentID, childID, grant); err != nil {
		return fmt.Errorf("%w: %v", ErrUsage, err)
	}
	return nil
}

// settleReservation marks a terminal session's budget reservation as
// charged, returning unused grant to the parent pool. Sessions without
// a reservation are untouched; a failed settle surfaces after the
// terminal transition has already committed.
func settleReservation(ctx context.Context, st *Store, sessionID string) {
	_ = st.SetReservationCharged(ctx, sessionID)
}
