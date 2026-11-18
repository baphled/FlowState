// Package provider defines the chat-provider port implemented by the
// adapters and consumed by the engine turn loop.
package provider

import "context"

// Message is one turn message addressed to the provider.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCalls contains complete assistant calls; partial argument deltas are adapter-owned.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID correlates a tool-role result with one preceding assistant call.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ChatRequest is one provider chat-turn request.
type ChatRequest struct {
	// Tools advertises structured tools; adapters must explicitly support translating them.
	Tools    []ToolDefinition
	Messages []Message
	Model    string
	// MaxTokens caps the provider response; <= 0 means provider default.
	MaxTokens int64
}

// ChatResponse is one provider chat-turn response.
type ChatResponse struct {
	Content string
	// ToolCalls contains complete calls returned by a tool-capable adapter.
	ToolCalls []ToolCall
	Error     error
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
// delta, complete tool call, finish marker or terminal typed error.
// Kind zero preserves legacy Content/Err events. Validate with StreamValidator
// before consuming structured events; this port does not execute tools.
type StreamEvent struct {
	Kind         StreamEventKind
	ToolCall     *ToolCall
	FinishReason string
	Content      string
	Err          error
}
