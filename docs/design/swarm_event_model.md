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
| `EventToolResult` | `"tool_result"` | `content`, `is_error` | Tool-call completion carrying the executed tool's output (or error body). |
| `EventPlan` | `"plan"` | `content`, `phase` | Plan artefact events. |
| `EventReview` | `"review"` | `verdict`, `content` | Review verdict events. |

### ID Invariants (P2)

Every persisted `SwarmEvent` MUST carry a non-empty `ID`. The intent layer
enforces this contract via `swarmEventFromChunk`, which falls back to a
generated UUID whenever the upstream source does not surface one.

| Event type | ID source | Contract |
|------------|-----------|----------|
| `EventDelegation` | `DelegationInfo.ChainID` | Reuses the chain identifier so delegation-start, progress, and completion events share an ID. |
| `EventToolCall` | `StreamChunk.ToolCallID` (Anthropic `tool_use.id`; OpenAI `tool_calls[].id`) | Surfaced from the provider layer so tool calls are correlatable across chunks. Falls back to a generated UUID when the provider omits one. |
| `EventToolResult` | Same `ToolCallID` as the originating `EventToolCall` | **Invariant:** an `EventToolCall` and its matching `EventToolResult` share the same ID. This is the correlation the P3 coalesce state machine uses to fold start/completion into a single pane line. |
| `EventPlan` | Generated UUID (`uuid.NewString`) | Providers do not surface an ID for plan artefacts. |
| `EventReview` | Generated UUID (`uuid.NewString`) | Providers do not surface an ID for review verdicts. |

### Coalesce Contract

`EventToolCall` and `EventToolResult` together describe the full lifecycle of
one tool invocation:

```
┌───────────────── same ID ─────────────────┐
│                                           │
│   tool_call (status="started")            │
│        │                                  │
│        ▼                                  │
│   tool_result (status="completed"/"error")│
│                                           │
└───────────────────────────────────────────┘
```

Consumers (the activity pane in P3, downstream analytics) rely on the shared
ID to merge the two events without guessing by `(agent, tool_name, time)`.
Because the ID originates in the provider-level `StreamChunk.ToolCallID`, the
contract survives concurrent tool calls from the same agent.

### Wire-Level Correlation (`StreamChunk.ToolCallID`)

`provider.StreamChunk` carries a `ToolCallID string` field populated at every
layer that handles tool calls:

- **Anthropic** (`internal/provider/anthropic/streaming.go`) — populated from
  the `tool_use` content block's `ID` at `content_block_stop`.
- **OpenAI-compatible** providers, including Z.AI
  (`internal/provider/openaicompat/openaicompat.go`) — populated from
  `delta.tool_calls[].id` in the accumulator's `JustFinishedToolCall` hook and
  the fallback flush path.
- **Engine** (`internal/engine/engine.go`) — re-populated on the synthetic
  `tool_result` chunk emitted after a tool executes, using the originating
  `ToolCall.ID`. This is the point at which the chain closes: the intent layer
  sees identical IDs on the call and the result.

Chunks unrelated to tool calls leave `ToolCallID` empty.

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

Sample JSONL output showing delegation, a correlated tool_call/tool_result
pair (note the shared `id`), and a review event:

```jsonl
{"id":"chain-42","type":"delegation","status":"started","timestamp":"2026-04-16T10:00:01Z","agent_id":"planner-01","metadata":{"source_agent":"orchestrator","description":"Plan authentication flow"}}
{"id":"toolu_01ABC","type":"tool_call","status":"started","timestamp":"2026-04-16T10:00:03Z","agent_id":"senior-eng-01","metadata":{"tool_name":"file_read","is_error":false}}
{"id":"toolu_01ABC","type":"tool_result","status":"completed","timestamp":"2026-04-16T10:00:04Z","agent_id":"senior-eng-01","metadata":{"content":"package foo\n...","is_error":false}}
{"id":"0a0b5e5c-9c88-4b2c-9d2f-9f0b2f7b4a2c","type":"review","status":"completed","timestamp":"2026-04-16T10:00:05Z","agent_id":"reviewer-01","metadata":{"verdict":"approved","content":"LGTM — no issues found"}}
```

Note how the `tool_call` and `tool_result` rows share `id` `toolu_01ABC` — this
is the upstream Anthropic `tool_use.id` surfaced on `provider.StreamChunk` and
propagated through both events, enabling downstream consumers to coalesce the
pair without string matching on names or timestamps.
