package streaming

import "time"

// SwarmEventType identifies the category of swarm activity captured by the
// multi-agent chat activity timeline.
//
// The canonical string values match the discriminators used by the existing
// streaming event pipeline (see events.go) so that conversions between
// pipeline events and SwarmEvent entries are string-identical.
type SwarmEventType string

// Supported SwarmEventType values.
//
// EventToolCall maps to the provider-agnostic tool call pipeline event
// (EventTypeToolCall) and intentionally uses the "tool_call" string rather
// than "tool" so downstream subscribers see identical discriminators.
const (
	// EventDelegation identifies delegation transitions (start, progress, completion).
	EventDelegation SwarmEventType = "delegation"
	// EventToolCall identifies tool call lifecycle transitions.
	EventToolCall SwarmEventType = "tool_call"
	// EventPlan identifies plan artefact events.
	EventPlan SwarmEventType = "plan"
	// EventReview identifies review verdict events.
	EventReview SwarmEventType = "review"
)

// SwarmEvent is the canonical structure for the multi-agent chat activity
// timeline. It captures a minimal, UI-focused projection of the richer
// streaming pipeline events for rendering in the Bubble Tea TUI.
//
// Metadata is intentionally opaque so Wave 3 persistence can serialise
// without recompiling producers; producers are expected to use stable string
// keys (for example "tool_name", "source_agent") for downstream filtering.
type SwarmEvent struct {
	ID        string
	Type      SwarmEventType
	Status    string
	Timestamp time.Time
	AgentID   string
	Metadata  map[string]interface{}
}

// SwarmEventStore is a thread-safe append-only store for SwarmEvent entries
// backing the activity pane. Implementations must enforce a bounded capacity
// with oldest-first eviction so producers never block the Bubble Tea loop.
//
// Expected usage: stream-worker goroutines call Append; the chat intent reads
// via All on the Bubble Tea goroutine. Clear is provided for test isolation
// and future "clear activity" affordances; it is not invoked during normal
// streaming.
type SwarmEventStore interface {
	// Append adds ev to the store, evicting the oldest entry when the store
	// is at capacity. Safe for concurrent callers.
	Append(ev SwarmEvent)
	// All returns a defensive copy of the stored events, oldest first.
	All() []SwarmEvent
	// Clear removes all events from the store.
	Clear()
}
