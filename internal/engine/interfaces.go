package engine

import "github.com/baphled/flowstate/internal/session"

// ChildSessionCreator creates a child session under a known parent session.
//
// Expected:
//   - parentID identifies an already-registered parent session.
//   - agentID identifies the agent for the new child session.
//
// Returns:
//   - A newly created child Session linked to parentID.
//   - session.ErrSessionNotFound when the parent is not registered.
//   - Any other error encountered during session creation.
//
// Side effects:
//   - Persists the new child session in the implementation's store.
type ChildSessionCreator interface {
	// CreateWithParent creates a child session under the given parent session.
	CreateWithParent(parentID string, agentID string) (*session.Session, error)
}
