// Package provider defines the chat-provider port implemented by the
// adapters and consumed by the engine turn loop.
package provider

import "context"

// Message is one turn message addressed to the provider.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is one provider chat-turn request.
type ChatRequest struct {
	Messages []Message
	Model    string
	// MaxTokens caps the provider response; <= 0 means provider default.
	MaxTokens int64
}

// ChatResponse is one provider chat-turn response.
type ChatResponse struct {
	Content string
	Error   error
}

// Provider is the chat-provider port implemented by adapters.
type Provider interface {
	// Chat performs one non-streaming chat turn.
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	// Name identifies the provider in session records.
	Name() string
	// Models lists models offered by the provider.
	Models() []string
}

// Streamer is the streaming-provider port consumed by the engine
// turn loop (M1-SESSION). Adapters that support streamed turns (zai)
// satisfy it in addition to Provider; the engine depends on this
// interface, not on concrete adapters.
type Streamer interface {
	Provider
	// Stream performs one streaming chat turn, emitting deltas on the
	// returned channel until the stream ends or a terminal error.
	Stream(ctx context.Context, req *ChatRequest) <-chan StreamEvent
}

// StreamEvent is one incremental streamed-turn event: a content
// delta or a terminal typed error.
type StreamEvent struct {
	Content string
	Err     error
}
