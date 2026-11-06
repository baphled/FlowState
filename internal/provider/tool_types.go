package provider

import "encoding/json"

// ToolDefinition advertises a provider-neutral tool. Parameters is a JSON Schema
// object; validation checks JSON object syntax, not schema dialect or semantics.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolCall is one complete assistant call. ID uses 1–128 ASCII letters,
// digits, underscores or hyphens. Name additionally permits dots for tool namespaces. Arguments must be a JSON object. RawMessage
// ownership remains with the caller; do not mutate it during validation/use.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// StreamEventKind distinguishes complete domain events, never wire fragments.
type StreamEventKind string

const (
	// StreamEventText carries only a content delta.
	StreamEventText StreamEventKind = "text"
	// StreamEventToolCall carries exactly one complete, validated call.
	StreamEventToolCall StreamEventKind = "tool_call"
	// StreamEventFinish carries a nonempty provider-neutral finish reason.
	StreamEventFinish StreamEventKind = "finish"
	// StreamEventError carries a terminal error.
	StreamEventError StreamEventKind = "error"
)

// StreamValidator validates an ordered stream and rejects repeated call IDs or
// events after finish/error. Its zero value is ready for one stream. It is owned
// by one consumer and is not concurrency-safe. Failed Accept calls change no state.
type StreamValidator struct {
	ids      map[string]bool
	terminal bool
}
