package provider

import "context"

type Message struct {
	Role    string
	Content string
}

type ChatRequest struct {
	Messages []Message
	Model    string
}

type ChatResponse struct {
	Content string
	Error   error
}

type Provider interface {
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	Name() string
	Models() []string
}

// Streamer is the streaming-provider port consumed by the engine
// turn loop (M1-SESSION). Adapters that support streamed turns (zai)
// satisfy it in addition to Provider; the engine depends on this
// interface, not on concrete adapters.
type Streamer interface {
	Provider
	Stream(ctx context.Context, req *ChatRequest) <-chan StreamEvent
}

// StreamEvent is one incremental streamed-turn event: a content
// delta or a terminal typed error.
type StreamEvent struct {
	Content string
	Err     error
}
