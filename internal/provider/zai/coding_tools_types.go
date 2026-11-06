package zai

import (
	"encoding/json"

	"github.com/baphled/flowstate/internal/provider"
)

// ToolProtocolError reports unsupported tool routes or invalid structured wire
// data. No complete calls or finish event are emitted after this terminal error.
type ToolProtocolError struct{ Reason string }

type codingFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type codingCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function codingFunction `json:"function"`
}
type codingMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content"`
	ToolCalls  []codingCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}
type codingTool struct {
	Type     string                  `json:"type"`
	Function provider.ToolDefinition `json:"function"`
}
type codingRequest struct {
	Model     string          `json:"model"`
	Messages  []codingMessage `json:"messages"`
	MaxTokens int64           `json:"max_tokens,omitempty"`
	Stream    bool            `json:"stream,omitempty"`
	Tools     []codingTool    `json:"tools,omitempty"`
}
type codingDelta struct {
	Index    *int           `json:"index"`
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function codingFunction `json:"function"`
}
type codingChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content   string        `json:"content"`
			ToolCalls []codingDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}
type codingAssembly struct {
	calls  []codingCall
	finish string
	strict bool
}
type codingResponse struct {
	Choices []struct {
		Message struct {
			Content   string          `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}
