// Package internalapp defines the consumer-side persistence ports for
// the session/application layer, per the DB-abstraction ADR (Option C:
// explicit repository ports over the existing database/sql store).
//
// The interfaces live on the CONSUMER side — not in internal/store —
// so the dependency direction stays correct: the store is an adapter
// that depends inward. *store.Store satisfies both ports as-is; no
// wrapper methods are needed. Interfaces are intentionally narrow:
// two small ports, not a method-for-method mirror of Store.
package internalapp

import (
	"context"

	"github.com/baphled/flowstate/internal/store"
)

// SessionRepo is the session-persistence port consumed by the
// session/application layer. It covers session CRUD plus the status
// projections absorbed by the store in packet B-1.
type SessionRepo interface {
	CreateSession(ctx context.Context, id, provider, modelID, harness, configSHA string) (*store.Session, error)
	GetSession(ctx context.Context, id string) (*store.Session, error)
	ListSessions(ctx context.Context, status string) ([]store.Session, error)
	UpdateSessionStatus(ctx context.Context, id, status string) error
}

// EventWriter is the single-writer append port for one session's
// event log. Close releases the single-writer registration; the
// invariant is preserved by keeping the writer registry inside the
// store adapter. *store.EventWriter satisfies it structurally (its
// Append and Close methods match), so it can be used wherever a
// consumer needs only the port view of the concrete writer.
type EventWriter interface {
	Append(ctx context.Context, actor, typ string, payload any) (int64, error)
	Close() error
}

// compile-time proof that the concrete writer satisfies the port.
var _ EventWriter = (*store.EventWriter)(nil)

// EventLog is the event-persistence port: a writer factory plus the
// verified replay used for inspection and export.
type EventLog interface {
	// NewEventWriter returns the single serialized writer for one
	// session's log. The returned EventWriter is the port view of
	// *store.EventWriter.
	NewEventWriter(ctx context.Context, sessionID string) (*store.EventWriter, error)
	Replay(ctx context.Context, sessionID string) ([]store.RecordedEvent, error)
}

// compile-time proof that the concrete store adapter satisfies both
// ports without modification.
var (
	_ SessionRepo = (*store.Store)(nil)
	_ EventLog    = (*store.Store)(nil)
)

// TurnEventLog is the persistence port consumed by the engine turn
// loop: replay, writer factory and status projection in one view so
// *store.Store satisfies it structurally without modification.
type TurnEventLog interface {
	// NewEventWriter returns the single serialized writer for one
	// session's log.
	NewEventWriter(ctx context.Context, sessionID string) (*store.EventWriter, error)
	// Replay returns the hash-verified event log for one session.
	Replay(ctx context.Context, sessionID string) ([]store.RecordedEvent, error)
	// UpdateSessionStatus projects a terminal or paused status.
	UpdateSessionStatus(ctx context.Context, id, status string) error
}

// TurnSession is the session view consumed by the engine turn loop;
// the concrete record is aliased so the store keeps owning its shape
// while runtime depends only on this port spelling.
type TurnSession = store.Session

// PausePayload carries pause metadata for EventWriter.Pause.
type PausePayload = store.PauseInfo
