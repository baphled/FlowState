package provider

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrNoChoices is returned when an API response contains no completion choices.
var ErrNoChoices = errors.New("no choices in response")

// Message represents a chat message between user and assistant.
type Message struct {
	Role      string
	Content   string
	ToolCalls []ToolCall
	Thinking  string
	// ModelID identifies the model that generated this message, if known.
	ModelID string
}

// ToolCall represents a tool invocation request from the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// ToolResultInfo carries tool execution output in a stream chunk.
type ToolResultInfo struct {
	Content string
	IsError bool
}

// ChatRequest contains the parameters for a chat completion request.
type ChatRequest struct {
	Provider string
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
	Properties map[string]any
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
	// ToolCallID carries the upstream provider's tool-use identifier (Anthropic
	// block.ID for tool_use blocks, OpenAI tool_calls[].id) on every chunk
	// associated with a tool call. Populated by providers on tool_call chunks,
	// and re-populated by the engine on the tool_result chunk emitted after a
	// tool executes. The ID is empty on chunks unrelated to tool calls.
	//
	// Consumers use InternalToolCallID (not this field) for cross-provider
	// correlation; ToolCallID is retained for audit trails and for surfaces
	// that need the provider-native id verbatim (Ctrl+E details modal).
	ToolCallID string
	// InternalToolCallID is the FlowState-internal, session-scoped identifier
	// that survives provider failover. Populated by the engine on every
	// chunk associated with a tool call via streaming.ToolCallCorrelator —
	// providers do not populate this field.
	//
	// The same logical tool call emitted from two different providers (the
	// failover case: provider A's "toolu_01abc" is replayed to provider B as
	// "call_xyz123") resolves to the same InternalToolCallID so downstream
	// coalesce logic pairs the tool_call and tool_result correctly.
	//
	// Empty on chunks unrelated to tool calls.
	InternalToolCallID string
	// Event carries a streaming.ProgressEvent or other streaming.Event implementation.
	// Set by the streaming infrastructure (not by providers directly) to convey typed
	// progress data to consumers such as SSE and WebSocket handlers.
	Event    any
	Thinking string
	// ModelID is the model that produced this chunk, stamped by the engine at stream time.
	ModelID string
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

// ModelPreference specifies a preferred model and provider combination.
type ModelPreference struct {
	Provider string
	Model    string
}

// Health contains health check information for a provider.
type Health struct {
	Name      string
	Available bool
	Latency   int64
	Error     error
}

// ErrorType classifies the kind of error returned by a provider.
type ErrorType string

const (
	// ErrorTypeRateLimit indicates the provider rejected the request because of rate limiting.
	ErrorTypeRateLimit ErrorType = "rate_limit"
	// ErrorTypeBilling indicates the provider rejected the request because of billing issues.
	ErrorTypeBilling ErrorType = "billing"
	// ErrorTypeQuota indicates the provider rejected the request because a quota was exceeded.
	ErrorTypeQuota ErrorType = "quota"
	// ErrorTypeOverload indicates the provider is temporarily overloaded.
	ErrorTypeOverload ErrorType = "overload"
	// ErrorTypeAuthFailure indicates the provider rejected the request because of authentication failure.
	ErrorTypeAuthFailure ErrorType = "auth_failure"
	// ErrorTypeModelNotFound indicates the requested model does not exist for the provider.
	ErrorTypeModelNotFound ErrorType = "model_not_found"
	// ErrorTypeNetworkError indicates the provider request failed because of a network issue.
	ErrorTypeNetworkError ErrorType = "network_error"
	// ErrorTypeServerError indicates the provider returned a server-side failure.
	ErrorTypeServerError ErrorType = "server_error"
	// ErrorTypeUnknown indicates the provider error could not be classified.
	ErrorTypeUnknown ErrorType = "unknown"
)

// Error is a structured provider failure returned at the boundary.
// It preserves HTTP status codes and provider-specific error codes for accurate classification.
type Error struct {
	HTTPStatus  int
	ErrorCode   string
	ErrorType   ErrorType
	Provider    string
	Message     string
	IsRetriable bool
	RawError    error
}

// NewProviderError constructs a provider error with the supplied metadata.
//
// Expected:
//   - provider identifies the upstream provider.
//   - errorType identifies the provider failure classification.
//   - message contains the provider-facing error message.
//
// Returns:
//   - A provider error populated with the supplied metadata.
//
// Side effects:
//   - None.
func NewProviderError(provider string, errorType ErrorType, message string) *Error {
	return &Error{Provider: provider, ErrorType: errorType, Message: message}
}

// Error returns a human-readable description of the provider error.
//
// Expected:
//   - p may be nil.
//
// Returns:
//   - A formatted error string, or "<nil>" when p is nil.
//
// Side effects:
//   - None.
func (p *Error) Error() string {
	if p == nil {
		return "<nil>"
	}

	provider := p.Provider
	if provider == "" {
		provider = "unknown"
	}

	details := ""
	if p.ErrorType != "" {
		details = string(p.ErrorType)
	}
	if p.ErrorCode != "" {
		if details != "" {
			details += "/"
		}
		details += p.ErrorCode
	}
	if p.HTTPStatus != 0 {
		if details != "" {
			details += " "
		}
		details += fmt.Sprintf("HTTP %d", p.HTTPStatus)
	}

	message := p.Message
	if message == "" && p.RawError != nil {
		message = p.RawError.Error()
	}

	if details == "" {
		if message == "" {
			return fmt.Sprintf("provider %s error", provider)
		}
		return fmt.Sprintf("provider %s error: %s", provider, message)
	}

	if message == "" {
		return fmt.Sprintf("provider %s error [%s]", provider, details)
	}

	return fmt.Sprintf("provider %s error [%s]: %s", provider, details, message)
}

// Unwrap returns the underlying error for errors.Is and errors.As traversal.
//
// Expected:
//   - p may be nil.
//
// Returns:
//   - The wrapped error, or nil when p is nil.
//
// Side effects:
//   - None.
func (p *Error) Unwrap() error {
	if p == nil {
		return nil
	}

	return p.RawError
}

// IsRetriableErrorType reports whether the given error type should be retried.
//
// Expected:
//   - t is a provider error classification.
//
// Returns:
//   - true when t is retriable.
//   - false otherwise.
//
// Side effects:
//   - None.
func IsRetriableErrorType(t ErrorType) bool {
	switch t {
	case ErrorTypeRateLimit, ErrorTypeOverload, ErrorTypeNetworkError, ErrorTypeServerError:
		return true
	default:
		return false
	}
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
