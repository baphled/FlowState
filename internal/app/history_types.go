package app

import (
	"encoding/json"

	"github.com/baphled/flowstate/internal/provider"
)

// HistoryLimits bounds projection after verified replay. Every limit must be
// positive. MaxEvents bounds inspected rows, MaxMessages bounds conversation
// entries, MaxBytes bounds their serialized JSON array, and MaxResultBytes bounds
// each tool message's JSON content. Replay itself still materialises the full log.
type HistoryLimits struct {
	MaxEvents, MaxMessages, MaxBytes, MaxResultBytes int
}

type historyState struct {
	limits    HistoryLimits
	messages  []provider.Message
	pending   map[string]historyCall
	seen      map[string]bool
	streaming bool
	bytes     int
}

type historyCall struct {
	call    provider.ToolCall
	started bool
}

type historyMessagePayload struct {
	Content   *string             `json:"content"`
	ToolCalls []provider.ToolCall `json:"tool_calls"`
	Delta     *string             `json:"delta"`
}

type historyToolPayload struct {
	ID     json.RawMessage `json:"tool_call_id"`
	Tool   string          `json:"tool"`
	Args   json.RawMessage `json:"args"`
	OK     *bool           `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type historyResultPreview struct {
	Truncated     bool   `json:"truncated"`
	OriginalBytes int    `json:"original_bytes"`
	Preview       string `json:"preview"`
}
