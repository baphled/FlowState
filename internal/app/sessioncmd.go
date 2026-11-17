package app

import (
	"context"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// DefaultHarness re-exports the session harness label for CLI composition.
const DefaultHarness = session.DefaultHarness

// CreateSession composes session creation over the concrete store: the
// CLI commands wire this package, never the session or store packages
// directly (owning-layer contract).
func CreateSession(ctx context.Context, st *Store, provider, modelID, harness, configSHA, task string) (*store.Session, error) {
	return session.Create(ctx, st, provider, modelID, harness, configSHA, task)
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
	return session.Close(ctx, st, id)
}
