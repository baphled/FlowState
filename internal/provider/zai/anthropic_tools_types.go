package zai

import (
	"encoding/json"

	"github.com/baphled/flowstate/internal/provider"
)

// anthropicTool is one tool in the Messages API request.
type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// anthropicBlock is one content block: text, tool_use or tool_result.
type anthropicBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

// anthropicMessage is a Messages API message; Content is a string or []anthropicBlock.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// anthropicStream is the per-stream state: the stop reason and the tool_use
// blocks still receiving input_json_delta fragments, by content block index.
type anthropicStream struct {
	usage   usageAccumulator
	stop    string
	pending map[int]*pendingToolUse
	ids     map[string]bool
	names   *toolNames
}

// toolNames maps the wire tool names of one request to the neutral names.
type toolNames struct{ byWire map[string]string }

// pendingToolUse accumulates one streamed tool_use block.
type pendingToolUse struct {
	call provider.ToolCall
	args []byte
}
