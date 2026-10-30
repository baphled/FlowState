package app

import (
	"github.com/baphled/flowstate/internal/provider"

	"github.com/baphled/flowstate/internal/runtime"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// TurnOutcome summarises one `session run` turn: the replayed event
// log for projection plus the engine result and interrupt state.
type TurnOutcome struct {
	// Events is the full hash-verified event log after the turn.
	Events []store.RecordedEvent
	// Result is the engine turn result (nil only on early failure).
	Result *runtime.TurnResult
	// Interrupted reports an interrupt-driven cancellation stop.
	Interrupted bool
}

// Dependencies are the explicit inputs of the composition root. Every
// field is injectable so tests can supply substitutes such as an
// in-memory store or fake tools (M1-COMPOSITION: composition verified
// by an injectable-fake test that asserts the wiring graph).
type Dependencies struct {
	// Store is the SQLite event store; required (store.OpenMemory for
	// tests).
	Store *store.Store

	// Registry supplies the tools available to sessions; required.
	Registry *tool.Registry

	// Allowlist names the tools a session may invoke; deny-by-default
	// when empty (M1-TOOLS).
	Allowlist []string
}

// Composition is one fully wired session runtime: the store plus a
// factory for per-session tool loops. It holds no ambient state; every
// field is the explicit wiring graph.
type Composition struct {
	store    *store.Store
	registry *tool.Registry
	allow    map[string]bool
}

// LoopHandle owns one session's tool loop and its event-writer
// registration. Close releases the writer so a later loop may be
// wired for the same session.
type LoopHandle struct {
	// Loop is the allowlisted tool execution loop for the session.
	Loop *tool.Loop

	// close releases the event-writer registration.
	close func() error
}

// Store aliases the concrete SQLite store so the thin cmd adapter can
// pass it through without importing the store package directly.
type Store = store.Store

// ProviderFactory resolves a provider by name; tests swap it for an
// httptest-backed adapter, production uses the registry.
type ProviderFactory func(name string) (provider.Provider, error)
