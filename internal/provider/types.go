package provider

import (
	"context"
	"time"
)

// Message represents a chat message between user and assistant.
type Message struct {
	Role      string
	Content   string
	ToolCalls []ToolCall
}

// ToolCall represents a tool invocation request from the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]interface{}
}

// ToolResultInfo carries tool execution output in a stream chunk.
type ToolResultInfo struct {
	Content string
	IsError bool
}

// ChatRequest contains the parameters for a chat completion request.
type ChatRequest struct {
	Model    string
	Messages []Message
	Tools    []Tool
}

// Tool describes a tool available for the model to use.
type Tool struct {
	Name        string
	Description string
	Schema      ToolSchema
}

// ToolSchema describes the input schema for a tool.
type ToolSchema struct {
	Type       string
	Properties map[string]interface{}
	Required   []string
}

// ChatResponse contains the result of a chat completion request.
type ChatResponse struct {
	Message Message
	Usage   Usage
}

// Usage contains token usage statistics for a request.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// DelegationInfo carries delegation event metadata in a stream chunk.
type DelegationInfo struct {
	SourceAgent  string     `json:"source_agent"`
	TargetAgent  string     `json:"target_agent"`
	ChainID      string     `json:"chain_id,omitempty"`
	ToolCalls    int        `json:"tool_calls,omitempty"`
	LastTool     string     `json:"last_tool,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	Status       string     `json:"status"`
	ModelName    string     `json:"model_name"`
	ProviderName string     `json:"provider_name"`
	Description  string     `json:"description"`
}

// StreamChunk represents a single chunk of a streaming response.
type StreamChunk struct {
	Content        string
	Done           bool
	Error          error
	EventType      string
	ToolCall       *ToolCall
	ToolResult     *ToolResultInfo
	DelegationInfo *DelegationInfo
	Event          interface{}
}

// EmbedRequest contains the parameters for an embedding request.
type EmbedRequest struct {
	Input string
	Model string
}

// Model describes an available LLM model.
type Model struct {
	ID            string
	Provider      string
	ContextLength int
}

// Health contains health check information for a provider.
type Health struct {
	Name      string
	Available bool
	Latency   int64
	Error     error
}

// Provider defines the interface for LLM provider implementations.
type Provider interface {
	// Name returns the provider name.
	Name() string
	// Stream sends a streaming chat request and returns a channel of response chunks.
	Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
	// Chat sends a chat completion request and returns the response.
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	// Embed generates embeddings for the given input text.
	Embed(ctx context.Context, req EmbedRequest) ([]float64, error)
	// Models returns the list of available models.
	Models() ([]Model, error)
}
