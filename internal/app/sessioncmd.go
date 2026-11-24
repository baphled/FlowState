package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// DefaultHarness re-exports the session harness label for CLI composition.
const DefaultHarness = session.DefaultHarness

// TypeSessionDelegated is the event opening a child session's log,
// attributing the delegation to the user (M2 child sessions).
const TypeSessionDelegated = "session.delegated"

// MaxDelegationDepth caps delegation nesting: root (0), child (1) and
// nested child (2) per the M1 delegation vocabulary.
const MaxDelegationDepth = 2

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
// before the initial turn.start carrying the task. A context that is
// already done creates nothing: a cancelled one reports ErrSessionCancelled
// and a deadline-exceeded one a plain wrapped error (see childContextError),
// each keeping the context error in the chain. Once the child's row is
// written the creation completes whatever happens to the context, so a
// child either does not exist or exists with its task.
func CreateChildSession(ctx context.Context, st *Store, parentID, provider, modelID, harness, configSHA, task string) (*store.Session, error) {
	if err := checkChildCreation(ctx, st, parentID, provider, modelID, task); err != nil {
		return nil, err
	}
	if harness == "" {
		harness = DefaultHarness
	}
	id, err := session.NewID()
	if err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}
	// From here the creation is not interrupted: a cancellation between
	// the row and its opening events would leave an active child with no
	// task that nothing owns.
	return openChildSession(context.WithoutCancel(ctx), st, childSpec{
		id: id, parentID: parentID, provider: provider, modelID: modelID,
		harness: harness, configSHA: configSHA, task: task,
	})
}

// checkChildCreation applies every precondition of child creation without
// writing: non-empty arguments, a live context, an existing non-terminal
// parent and room under MaxDelegationDepth. RunDelegatedChild calls it
// before it reserves budget, so a refusal leaves nothing behind.
func checkChildCreation(ctx context.Context, st *Store, parentID, provider, modelID, task string) error {
	if parentID == "" || provider == "" || modelID == "" || task == "" {
		return fmt.Errorf("%w: parent, provider, model and task must not be empty", ErrUsage)
	}
	if err := ctx.Err(); err != nil {
		return childContextError(err)
	}
	parent, err := st.GetSession(ctx, parentID)
	if err != nil {
		return fmt.Errorf("%w: unknown parent session %s", ErrUsage, parentID)
	}
	if delegatedParentTerminal[parent.Status] {
		return fmt.Errorf("%w: parent session %s is %s", ErrUsage, parentID, parent.Status)
	}
	chain, cerr := st.DelegationChain(ctx, parentID)
	if cerr != nil {
		return fmt.Errorf("%w: delegation chain: %v", ErrUsage, cerr)
	}
	if len(chain)+1 > MaxDelegationDepth {
		return fmt.Errorf("%w: delegation depth exceeds %d (root/child/nested-child)", ErrUsage, MaxDelegationDepth)
	}
	return nil
}

// childContextError classes a done context for child creation as
// mapSwarmRunError does for a swarm run: only context.Canceled is an
// interrupt (ErrSessionCancelled, exit 130). A deadline is a limit the
// caller imposed, not a user interrupt, so DeadlineExceeded keeps its
// own identity under a plain wrapping (class internal).
func childContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: create child session: %w", ErrSessionCancelled, err)
	}
	return fmt.Errorf("app: create child session: %w", err)
}

// openChildSession writes the child's row and its two opening events:
// session.delegated, then the turn.start carrying the task.
func openChildSession(ctx context.Context, st *Store, c childSpec) (*store.Session, error) {
	child, err := st.CreateChildSession(ctx, c.id, c.parentID, c.provider, c.modelID, c.harness, c.configSHA)
	if err != nil {
		return nil, err
	}
	w, err := st.NewEventWriter(ctx, child.ID)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	if _, err := w.Append(ctx, session.ActorUser, TypeSessionDelegated, delegatedPayload(c)); err != nil {
		return nil, err
	}
	if _, err := w.Append(ctx, session.ActorUser, session.TypeTurnStart, map[string]any{"task": c.task}); err != nil {
		return nil, err
	}
	return child, nil
}

// delegatedPayload is the session.delegated payload: the parent id and,
// for a profile-driven child, the profile it runs under.
func delegatedPayload(c childSpec) map[string]any {
	payload := map[string]any{"parent_id": c.parentID}
	if c.profile != nil {
		payload["profile"] = c.profile
	}
	return payload
}

// SessionEventsAfter returns one session's committed events with seq
// greater than afterSeq, ordered by seq (the follow/tail surface).
// Unlike the bare store port it first checks the session exists, so
// an unknown id surfaces session_not_found instead of an empty log.
func SessionEventsAfter(ctx context.Context, st *Store, id string, afterSeq int64) ([]store.RecordedEvent, error) {
	if _, err := st.GetSession(ctx, id); err != nil {
		return nil, err
	}
	return st.EventsAfter(ctx, id, afterSeq)
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
		return fmt.Errorf("%w: %w", ErrUsage, err)
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

// DelegationChain returns the delegation ancestors of sessionID
// ordered root-first (empty for a root), so the CLI and future API
// surfaces read the delegation graph through the owning layer.
func DelegationChain(ctx context.Context, st *Store, sessionID string) ([]store.Session, error) {
	return st.DelegationChain(ctx, sessionID)
}

// ResolveRunBudget resolves the effective budget for a session run:
// an explicitly requested budget anchors the session's shared pool and
// is returned unchanged; without one, a delegated child draws from its
// reservation (grant minus recorded consumption). Sessions with
// neither return 0 (the caller's unlimited/default handling).
func ResolveRunBudget(ctx context.Context, st *Store, sess *Session, requested int64) (int64, error) {
	if requested > 0 {
		if err := st.SetBudgetPool(ctx, sess.ID, requested); err != nil {
			return 0, err
		}
		return requested, nil
	}
	res, err := st.ReservationFor(ctx, sess.ID)
	if errors.Is(err, store.ErrUnknownReservation) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if res.Status != store.ReservationStatusReserved {
		return 0, nil
	}
	budget := res.Granted - res.Consumed
	if budget < 0 {
		budget = 0
	}
	return budget, nil
}
