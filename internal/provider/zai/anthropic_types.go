package zai

import "github.com/baphled/flowstate/internal/provider"

// anthropicRequest is the Messages API request body.
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int64              `json:"max_tokens"`
	Stream    bool               `json:"stream,omitempty"`
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
	} `json:"delta"`
}
