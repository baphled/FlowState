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
	// loaded is the skill.load call whose successful result the next skill.loaded may record, and loadedFrame that result
	// as the model received it (ADR-013 D9); an empty call when none is owed.
	loaded, loadedFrame string
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

// historySkillPayload is the skill.loaded record of one delivered skill file.
type historySkillPayload struct {
	Name       string `json:"name"`
	File       string `json:"file"`
	SHA256     string `json:"sha256"`
	Path       string `json:"path"`
	Bytes      int    `json:"bytes"`
	ToolCallID string `json:"tool_call_id"`
}
