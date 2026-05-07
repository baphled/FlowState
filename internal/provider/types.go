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
	// ThinkingBlocks carries the structured thinking content blocks
	// emitted by the upstream provider on the turn that produced this
	// message. The Anthropic API requires that on every subsequent turn
	// the assistant's thinking blocks are sent back UNCHANGED — including
	// the encrypted `signature` field on each thinking block, and the
	// opaque `data` payload for redacted thinking. Without round-tripping
	// these the server silently disables extended thinking on turn 2+.
	//
	// Empty for non-thinking turns and for providers that do not produce
	// thinking blocks. Construction is the responsibility of the engine /
	// session accumulator, NOT the provider — providers only emit the
	// per-block fragments via StreamChunk.
	ThinkingBlocks []ThinkingBlock
	// StopReason is the upstream provider's stop reason for the turn that
	// produced this message (e.g. "end_turn", "tool_use", "max_tokens",
	// "refusal", "model_context_window_exceeded"). Empty when unknown.
	StopReason string
}

// ThinkingBlock is a single thinking-content block as produced by the
// upstream provider. Anthropic's extended thinking ships these as
// signed (Thinking + Signature) or redacted (Redacted=true + Data)
// variants. To round-trip thinking across turns, the engine must
// preserve every block verbatim and replay it on the subsequent
// request — see provider.Message.ThinkingBlocks.
type ThinkingBlock struct {
	// Thinking is the visible thinking text. Empty when Redacted is
	// true.
	Thinking string `json:"thinking,omitempty"`
	// Signature is the encrypted continuity signature attached to a
	// signed thinking block. Required by Anthropic on every replayed
	// thinking block; must be sent back UNCHANGED.
	Signature string `json:"signature,omitempty"`
	// Redacted is true when the upstream returned a redacted_thinking
	// block. Redacted blocks have no visible text — only the encrypted
	// Data payload — but must still be replayed verbatim.
	Redacted bool `json:"redacted,omitempty"`
	// Data is the opaque encrypted payload for a redacted thinking
	// block. Empty when Redacted is false.
	Data string `json:"data,omitempty"`
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
//
// The optional sampling and behaviour fields (MaxTokens, Temperature,
// TopP, TopK, ThinkingMode, ToolChoice) are caller-supplied hints. Each
// provider applies its own model-aware policy on top — e.g. the
// Anthropic provider strips non-default sampling for models that reject
// it (Opus 4.7) and rewrites manual `enabled` thinking to `adaptive`
// where required. A zero value means "let the provider pick its
// default", preserving back-compat for callers that do not set them.
type ChatRequest struct {
	Provider string
	Model    string
	Messages []Message
	Tools    []Tool
	// MaxTokens is the caller-requested upper bound on generated tokens.
	// Zero means "use provider/model default". Providers may clamp this
	// to a per-model ceiling.
	MaxTokens int
	// Temperature is the sampling temperature. Nil means "do not set"
	// (provider default applies). Some models reject any non-default
	// value; the provider is responsible for stripping when required.
	Temperature *float64
	// TopP is the nucleus-sampling cutoff. Nil means "do not set".
	TopP *float64
	// TopK is the top-K sampling cutoff. Nil means "do not set".
	TopK *int
	// ThinkingMode selects the extended-thinking configuration. Empty
	// means "do not set". Recognised values:
	//   - "disabled"    – explicitly disable thinking
	//   - "adaptive"    – model-managed budget (Opus 4.7+ default)
	//   - "enabled"     – manual thinking with provider-default budget
	//   - "enabled:N"   – manual thinking with budget_tokens=N (N>=1024)
	// Providers may rewrite or reject values incompatible with the
	// target model (e.g. Opus 4.7 rejects "enabled").
	ThinkingMode string
	// ToolChoice constrains tool selection. Empty means "do not set".
	// Recognised values:
	//   - "auto"      – model decides (default behaviour)
	//   - "any"       – model must call some tool
	//   - "none"      – model must not call any tool
	//   - "tool:NAME" – model must call the named tool
	// Providers map to their native enum and may reject combinations
	// incompatible with thinking (e.g. {auto, none} only when thinking
	// is on).
	ToolChoice string
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

// UsageDelta carries per-turn token-accounting deltas reported by the
// upstream provider on streaming events that carry usage data
// (Anthropic's `message_start` and `message_delta`).
//
// The Anthropic API ships cumulative output_tokens on `message_delta`
// and cache stats on `message_start`. RequestID is the upstream message
// ID (Anthropic's `message.id`) and Model is the wire-confirmed model
// from `message_start.message.model`.
//
// All fields are zero/empty when the chunk does not carry that data.
// Consumers that aggregate usage should treat each chunk as a
// snapshot — a later message_delta carries the latest cumulative
// values, not an increment over the previous one.
type UsageDelta struct {
	InputTokens              int64  `json:"input_tokens,omitempty"`
	OutputTokens             int64  `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens,omitempty"`
	RequestID                string `json:"request_id,omitempty"`
	Model                    string `json:"model,omitempty"`
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
	// Usage carries token-accounting and identity data captured from
	// `message_start` / `message_delta` events. Nil when the chunk
	// carries no usage data. EventType is "usage" for message_start
	// chunks and "stop_reason" for message_delta chunks.
	Usage *UsageDelta
	// Signature is the encrypted continuity signature for a thinking
	// block, accumulated across one or more `signature_delta` events
	// and emitted alongside the matching Thinking content on
	// content_block_stop. Empty for non-thinking chunks.
	Signature string
	// RedactedThinking is the opaque encrypted payload of a
	// `redacted_thinking` content block, captured on content_block_start
	// and emitted on the matching content_block_stop. Empty for
	// non-redacted-thinking chunks.
	RedactedThinking string
	// StopReason is the upstream provider's stop reason for the turn
	// (e.g. "end_turn", "tool_use", "max_tokens", "refusal",
	// "model_context_window_exceeded"). Populated on the chunk emitted
	// from a `message_delta` event. Empty otherwise.
	StopReason string
	// StopSequence is the matched stop sequence for the turn, when the
	// stop reason is "stop_sequence". Empty otherwise.
	StopSequence string
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
	// ProviderID is the provider that produced this chunk, stamped by the engine at
	// stream time. Paired with ModelID so downstream consumers (the session
	// accumulator, the SSE writer, the per-message audit trail) can attribute
	// every assistant turn to a (provider, model) pair without re-querying the
	// engine. The value is the provider's stable identifier (e.g. "anthropic",
	// "zai", "openai"), matching the `currentProviderId` shape used elsewhere.
	ProviderID string
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
	// ErrorTypeContextWindowExceeded indicates the request was refused
	// because its estimated input-token count exceeded the configured
	// per-model context window. The engine emits this BEFORE flushing to
	// the upstream provider, via the proactive overflow gate that mirrors
	// OpenCode's isOverflow check (compaction.ts:30-89). It must surface
	// to the user as a SeverityCritical event so the Vue chat UI shows
	// the persistent CriticalErrorBanner with recoverable-action copy
	// (trim recent tool results, start a fresh session).
	ErrorTypeContextWindowExceeded ErrorType = "context_window_exceeded"
	// ErrorTypeUnknown indicates the provider error could not be classified.
	ErrorTypeUnknown ErrorType = "unknown"
)

// RateLimit carries provider-issued rate-limit metadata extracted from
// an HTTP response (typically on a 429 / 529 / 503 error). Each field is
// optional — providers populate only what the upstream supplied. A
// failover scheduler should prefer RetryAfter when non-zero over a
// generic per-error-type cooldown so back-off matches the carrier
// signal the provider gave us.
//
// All token / request counters use -1 to mean "not provided" so the
// zero value (no metadata) is unambiguous from a real "0 remaining".
// Reset times are zero-valued time.Time when not provided.
//
// The current populator is the Anthropic provider, which extracts the
// `retry-after` and `anthropic-ratelimit-*` response headers; other
// providers may leave the *RateLimit pointer nil on Error and the
// failover hook falls back to the per-error-type cooldown table.
type RateLimit struct {
	// RetryAfter is the duration the carrier asked us to wait before
	// retrying, parsed from the `retry-after` HTTP header. Zero when
	// the header is absent or unparseable.
	RetryAfter time.Duration
	// InputTokensLimit is the per-window input-token budget. -1 when
	// not provided.
	InputTokensLimit int
	// InputTokensRemaining is the input-token budget left in the
	// current window. -1 when not provided.
	InputTokensRemaining int
	// InputTokensReset is the wall-clock time at which the
	// input-token budget resets. Zero when not provided.
	InputTokensReset time.Time
	// OutputTokensLimit is the per-window output-token budget. -1
	// when not provided.
	OutputTokensLimit int
	// OutputTokensRemaining is the output-token budget left in the
	// current window. -1 when not provided.
	OutputTokensRemaining int
	// OutputTokensReset is the wall-clock time at which the
	// output-token budget resets. Zero when not provided.
	OutputTokensReset time.Time
	// RequestsLimit is the per-window request budget. -1 when not
	// provided.
	RequestsLimit int
	// RequestsRemaining is the request budget left in the current
	// window. -1 when not provided.
	RequestsRemaining int
	// RequestsReset is the wall-clock time at which the request
	// budget resets. Zero when not provided.
	RequestsReset time.Time
	// TokensLimit is the combined input+output per-window token
	// budget when the provider exposes one. -1 when not provided.
	TokensLimit int
	// TokensRemaining is the combined token budget left in the
	// current window. -1 when not provided.
	TokensRemaining int
	// TokensReset is the wall-clock time at which the combined token
	// budget resets. Zero when not provided.
	TokensReset time.Time
	// RequestID is the upstream request identifier (Anthropic's
	// `request-id` header) for support correlation. Empty when not
	// provided.
	RequestID string
}

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
	// RateLimit carries provider-issued rate-limit metadata when
	// available. Nil when the provider did not surface any
	// rate-limit headers (e.g. a generic 500 server error, or any
	// provider that does not yet populate this field). A failover
	// scheduler should consult RateLimit.RetryAfter (when non-zero)
	// in preference to a generic per-error-type cooldown.
	RateLimit *RateLimit
	// Model is the model identifier this error attributes to. Populated
	// by the engine's proactive context-window overflow gate; empty for
	// upstream-classified errors that did not carry the model on the
	// wire (legacy provider error types continue to flow with this
	// field zero-valued).
	Model string
	// EstimatedInputTokens is the engine's input-token estimate for the
	// refused request, populated only by the proactive context-window
	// overflow gate (ErrorTypeContextWindowExceeded). Zero for any
	// other ErrorType.
	EstimatedInputTokens int
	// ContextLimit is the resolved per-model context window limit the
	// estimate was compared against. Zero unless ErrorType is
	// ErrorTypeContextWindowExceeded. Pairs with EstimatedInputTokens
	// for operator log triage (slog "estimated_input_tokens"/"limit"
	// kv pair, see engine.streamFromProvider).
	ContextLimit int
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
