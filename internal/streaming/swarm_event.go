package streaming

import "time"

// CurrentSchemaVersion is the version stamp applied to every SwarmEvent
// produced after P4. The field travels with the event on disk so future
// format migrations can detect mixed-version files and either migrate on
// load or refuse to downgrade.
//
// Version history:
//   - 0 (implicit): legacy files written before P4 — no schema_version
//     field was emitted; the loader treats zero as equivalent to v1 for
//     backward compatibility.
//   - 1: current. Adds schema_version to the on-disk shape.
const CurrentSchemaVersion = 1

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
//
// ID invariants (P2):
//   - Every persisted SwarmEvent MUST have a non-empty ID.
//   - EventDelegation events MUST use the DelegationInfo.ChainID as their ID.
//   - An EventToolCall and its matching EventToolResult MUST share the same
//     ID so downstream consumers can coalesce them (tool-call state machine
//     in P3 relies on this contract). The shared ID is the upstream
//     provider's tool-use ID (Anthropic block.ID, OpenAI tool_call.id), or a
//     generated UUID when the provider omits one.
//   - EventPlan and EventReview events use a generated UUID.
const (
	// EventDelegation identifies delegation transitions (start, progress, completion).
	EventDelegation SwarmEventType = "delegation"
	// EventToolCall identifies tool call lifecycle transitions.
	EventToolCall SwarmEventType = "tool_call"
	// EventToolResult identifies a tool-call completion event carrying the
	// tool's output (or error). An EventToolResult shares its ID with the
	// originating EventToolCall so the P3 state machine can coalesce the
	// pair into a single pane line keyed on ID.
	EventToolResult SwarmEventType = "tool_result"
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
	ID        string                 `json:"id"`
	Type      SwarmEventType         `json:"type"`
	Status    string                 `json:"status"`
	Timestamp time.Time              `json:"timestamp"`
	AgentID   string                 `json:"agent_id"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	// SchemaVersion marks the on-disk shape version (P4). Events produced
	// by the current code stamp CurrentSchemaVersion; files persisted before
	// P4 decode this field as zero and the loader treats 0 as implicit v1
	// for forward compatibility. Kept omitempty so legacy round-trips (no
	// stamped version) do not regress.
	SchemaVersion int `json:"schema_version,omitempty"`
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
