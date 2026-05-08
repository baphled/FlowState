// Package orchestrator — types.go declares the surface-agnostic
// payload and edge-interface types the lifecycle methods (SwitchAgent,
// SwitchModel, LoadSession, NewSession, SaveTurnEnd) consume and
// return. Per ADR - FlowState Engine Boundary Invariant 1, every
// type here is pure-Go: no consumer-framework imports (tea.*, http.*,
// SSE writers).
package orchestrator

import (
	"errors"

	"github.com/baphled/flowstate/internal/agent"
	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
)

// Engine is the wider orchestrator-level engine surface — it combines
// swarm.DispatchEngine (the streaming half) with the lifecycle setters
// (SetManifest, SetModelPreference, SetContextStore) the new
// SwitchAgent / SwitchModel / LoadSession / NewSession methods need.
//
// *engine.Engine satisfies this interface in production; tests pass
// a fake. We declare it at the orchestrator's edge per
// ADR - FlowState Engine Boundary Invariant 4.
type Engine interface {
	swarm.DispatchEngine
	SetManifest(agent.Manifest)
	SetModelPreference(providerName, modelName string)
	SetContextStore(store *recall.FileContextStore, sessionID string)
}

// SessionStore is the narrow surface the orchestrator needs from the
// session-persistence layer. *contextpkg.FileSessionStore satisfies
// this in production; tests pass fakes.
//
// Save and Load match the existing contextpkg.SessionStore signatures
// verbatim so production wiring is a no-op assignment.
type SessionStore interface {
	Save(sessionID string, store *recall.FileContextStore, meta contextpkg.SessionMetadata) error
	Load(sessionID string) (*recall.FileContextStore, error)
}

// SessionManager is the narrow surface the orchestrator needs from
// session.Manager. Declared at the consumer's edge per
// ADR - FlowState Engine Boundary Invariant 4 (test boundary mirrors
// runtime boundary) so tests fake it without standing up a full
// session manager.
type SessionManager interface {
	UpdateSessionAgent(sessionID, agentID string) error
	UpdateSessionModel(sessionID, providerName, modelName string) error
}

// SwarmEventPersister is the optional capability a SessionStore may
// satisfy to support persisting and restoring SwarmEvent activity
// timelines alongside session data. The orchestrator type-asserts
// the SessionStore at call time — implementations that do not
// satisfy this interface skip the swarm-event side of LoadSession /
// SaveTurnEnd silently.
type SwarmEventPersister interface {
	SaveEvents(sessionID string, evs []streaming.SwarmEvent) error
	LoadEvents(sessionID string) ([]streaming.SwarmEvent, error)
}

// LoadedSession is everything a surface needs after a session load.
// The Store is installed on the engine by LoadSession itself; the
// caller (TUI / web SSE) is responsible for applying SwarmEvents
// to its surface-scoped event store.
type LoadedSession struct {
	Store       *recall.FileContextStore
	Metadata    contextpkg.SessionMetadata
	SwarmEvents []streaming.SwarmEvent
}

// TurnSnapshot is the per-turn state the orchestrator persists at
// end of turn. Fields surfaced verbatim from
// contextpkg.SessionMetadata + a swarm-event channel.
//
// The snapshot is built by the caller, not the orchestrator: per
// ADR - Session Orchestrator for Surface Parity §SaveTurnEnd, the
// caller reads engine state on its own goroutine (the TUI's Bubble
// Tea loop, the CLI's main goroutine) where ownership of concurrent
// reads is unambiguous. The orchestrator accepts the snapshot and
// performs only the persistence side-effects.
type TurnSnapshot struct {
	// Store is the engine's current FileContextStore. Required.
	Store *recall.FileContextStore
	// AgentID is the active agent's id at end of turn.
	AgentID string
	// SystemPrompt is the engine.BuildSystemPrompt result.
	SystemPrompt string
	// LoadedSkills is the list of skill names returned by
	// engine.LoadedSkills, projected to a string slice by the caller.
	LoadedSkills []string
	// SwarmEvents is the surface's swarm-event store snapshot
	// (i.swarmStore.All() in the TUI). Empty slices are tolerated
	// and result in no SaveEvents call.
	SwarmEvents []streaming.SwarmEvent
}

// errAgentNotFound fires when SwitchAgent is called with an agent id
// that the registry does not resolve.
var errAgentNotFound = errors.New("session orchestrator: agent not found")

// errSessionNotFound fires when LoadSession is called with a session
// id that the SessionStore does not resolve, or when SaveTurnEnd /
// LoadSession are called against an orchestrator constructed without
// a SessionStore (nil-tolerance mirrors the TUI's existing nil-store
// guard at intent.go:saveSession).
var errSessionNotFound = errors.New("session orchestrator: session not found")

// errStoreNotConfigured fires when LoadSession or SaveTurnEnd are
// called against an orchestrator that was constructed without a
// SessionStore. Callers should treat this as "persistence is disabled
// for this orchestrator instance" and continue.
var errStoreNotConfigured = errors.New("session orchestrator: session store not configured")

// contextMetadataFromSnapshot projects a TurnSnapshot's metadata
// fields onto contextpkg.SessionMetadata so SaveTurnEnd can call
// SessionStore.Save without exposing the contextpkg package surface
// to the snapshot type.
func contextMetadataFromSnapshot(s TurnSnapshot) contextpkg.SessionMetadata {
	return contextpkg.SessionMetadata{
		AgentID:      s.AgentID,
		SystemPrompt: s.SystemPrompt,
		LoadedSkills: s.LoadedSkills,
	}
}
