package zai

import "encoding/json"

// anthropicRequest is the Messages API request body.
type anthropicRequest struct {
	Model     string          `json:"model"`
	MaxTokens int64           `json:"max_tokens"`
	Stream    bool            `json:"stream,omitempty"`
	System    string          `json:"system,omitempty"`
	Messages  any             `json:"messages"`
	Tools     []anthropicTool `json:"tools,omitempty"`
}

// anthropicResponse is a non-streaming Messages API response.
type anthropicResponse struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	Usage json.RawMessage `json:"usage"`
}

// anthropicEvent is one SSE event from a streaming Messages response.
type anthropicEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	// ContentBlock is set on content_block_start events.
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
		// PartialJSON is a fragment of a tool_use input on input_json_delta events.
		PartialJSON string `json:"partial_json"`
		// StopReason is set on message_delta events.
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	// Message is set on message_start events (an object holding the usage) and
	// may be any JSON on others (an error event can carry a string), so it is
	// kept raw and decoded only for message_start.
	Message json.RawMessage `json:"message"`
	// Usage is set on message_delta events; it is cumulative for the message.
	Usage json.RawMessage `json:"usage"`
	// Error is set on error events.
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
