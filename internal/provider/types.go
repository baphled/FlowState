package provider

import (
	"context"
)

type Message struct {
	Role      string
	Content   string
	ToolCalls []ToolCall
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]interface{}
}

type ChatRequest struct {
	Model    string
	Messages []Message
	Tools    []Tool
}

type Tool struct {
	Name        string
	Description string
	Schema      ToolSchema
}

type ToolSchema struct {
	Type       string
	Properties map[string]interface{}
	Required   []string
}

type ChatResponse struct {
	Message Message
	Usage   Usage
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type StreamChunk struct {
	Content   string
	Done      bool
	Error     error
	EventType string
	ToolCall  *ToolCall
}

type EmbedRequest struct {
	Input string
	Model string
}

type Model struct {
	ID            string
	Provider      string
	ContextLength int
}

type ProviderHealth struct {
	Name      string
	Available bool
	Latency   int64
	Error     error
}

type Provider interface {
	Name() string
	Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	Embed(ctx context.Context, req EmbedRequest) ([]float64, error)
	Models() ([]Model, error)
}
