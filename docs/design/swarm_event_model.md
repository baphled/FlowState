# SwarmEvent Model

## Overview

`SwarmEvent` is the canonical structure for the multi-agent chat activity
timeline. It provides a minimal, UI-focused projection of the richer streaming
pipeline events for rendering in the Bubble Tea TUI. The model lives at
`internal/streaming/swarm_event.go`.

## Schema

```go
type SwarmEvent struct {
    ID        string                 `json:"id"`
    Type      SwarmEventType         `json:"type"`
    Status    string                 `json:"status"`
    Timestamp time.Time              `json:"timestamp"`
    AgentID   string                 `json:"agent_id"`
    Metadata  map[string]interface{} `json:"metadata,omitempty"`
}
```

| Field | Type | Description |
|-------|------|-------------|
| `ID` | `string` | Unique identifier for the event. |
| `Type` | `SwarmEventType` | Category discriminator (see Event Types below). |
| `Status` | `string` | Lifecycle state (e.g. `"started"`, `"completed"`, `"error"`). |
| `Timestamp` | `time.Time` | When the event occurred. Serialised as RFC3339. |
| `AgentID` | `string` | Originating agent identifier. |
| `Metadata` | `map[string]interface{}` | Event-specific data with stable string keys. Omitted from JSON when empty. |

## Event Types

`SwarmEventType` is a `string` type. The canonical string values match the
discriminators used by the streaming event pipeline so that conversions are
string-identical.

| Type | Value | Metadata keys | Description |
|------|-------|---------------|-------------|
| `EventDelegation` | `"delegation"` | `source_agent`, `description`, `chain_id` | Delegation transitions (start, progress, completion). |
| `EventToolCall` | `"tool_call"` | `tool_name`, `is_error` | Tool call lifecycle transitions. |
| `EventPlan` | `"plan"` | `content`, `phase` | Plan artefact events. |
| `EventReview` | `"review"` | `verdict`, `content` | Review verdict events. |

## Store Interface

```go
type SwarmEventStore interface {
    Append(ev SwarmEvent)
    All() []SwarmEvent
    Clear()
}
```

| Method | Behaviour |
|--------|-----------|
| `Append` | Adds an event, evicting the oldest entry when at capacity. Safe for concurrent callers. |
| `All` | Returns a defensive copy of stored events, oldest first. |
| `Clear` | Removes all events. Provided for test isolation and future "clear activity" affordances. |

### MemorySwarmStore

`MemorySwarmStore` (`internal/streaming/event_store_memory.go`) is the
in-memory implementation. It is mutex-protected (not channel-based) to avoid
dropping events or blocking streams under concurrent producer load.

- **Default capacity:** 200 events (`DefaultSwarmStoreCapacity`).
- **Eviction policy:** oldest-first when at capacity.
- **Thread safety:** all methods acquire a `sync.Mutex`.

Construct with `NewMemorySwarmStore(capacity)`. A non-positive capacity falls
back to the default.

## Persistence

Events are persisted in JSONL format (JSON Lines) via two functions in
`internal/streaming/swarm_event_persistence.go`:

| Function | Signature | Behaviour |
|----------|-----------|-----------|
| `WriteEventsJSONL` | `(w io.Writer, events []SwarmEvent) error` | Writes one JSON object per line. Timestamps encode as RFC3339. An empty slice produces no output. |
| `ReadEventsJSONL` | `(r io.Reader) ([]SwarmEvent, error)` | Reads JSON Lines, returning parsed events. Corrupted lines are silently skipped. Returns an error only on reader failure (not parse errors). |

### Format rules

- One JSON object per line (no pretty-printing).
- Timestamps in RFC3339 format (standard `encoding/json` behaviour for `time.Time`).
- `metadata` field carries `omitempty` — absent when the map is nil or empty.
- Empty lines are skipped on read.
- Corrupted lines are skipped gracefully; a single bad line does not discard the timeline.

## Examples

Sample JSONL output showing the three most common event types:

```jsonl
{"id":"evt-001","type":"delegation","status":"started","timestamp":"2026-04-16T10:00:01Z","agent_id":"planner-01","metadata":{"source_agent":"orchestrator","description":"Plan authentication flow","chain_id":"chain-42"}}
{"id":"evt-002","type":"tool_call","status":"completed","timestamp":"2026-04-16T10:00:03Z","agent_id":"senior-eng-01","metadata":{"tool_name":"file_read","is_error":false}}
{"id":"evt-003","type":"review","status":"completed","timestamp":"2026-04-16T10:00:05Z","agent_id":"reviewer-01","metadata":{"verdict":"approved","content":"LGTM — no issues found"}}
```
