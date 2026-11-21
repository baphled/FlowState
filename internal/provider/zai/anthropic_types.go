package zai

import "github.com/baphled/flowstate/internal/provider"

// anthropicRequest is the Messages API request body.
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int64              `json:"max_tokens"`
	Stream    bool               `json:"stream,omitempty"`
	System    string             `json:"system,omitempty"`
	Messages  []provider.Message `json:"messages"`
}

// anthropicResponse is a non-streaming Messages API response.
type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// anthropicEvent is one SSE event from a streaming Messages response.
type anthropicEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
		// StopReason is set on message_delta events.
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	// Error is set on error events.
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
