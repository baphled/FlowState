// Package anthropic provides an Anthropic Claude API provider implementation.
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	anthropicAPI "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/baphled/flowstate/internal/provider"
	shared "github.com/baphled/flowstate/internal/provider/shared"
)

// ErrNotSupported is returned when an unsupported operation is attempted.
var ErrNotSupported = errors.New("anthropic does not support embeddings")

var errAPIKeyRequired = errors.New("anthropic API key is required")
var errOAuthTokenRequired = errors.New("anthropic OAuth token is required")

const (
	providerName          = "anthropic"
	defaultContextLength  = 200000
	streamChannelBuffSize = 16
	defaultMaxTokens      = 4096
	oauthTokenPrefix      = "sk-ant-oat01-"
	oauthBetaHeader       = "oauth-2025-04-20"
	oauthUserAgent        = "claude-cli/2.1.2 (external, cli)"
	oauthAppHeader        = "cli"
	// oauthBillingHeaderName is the HTTP header name Anthropic's
	// edge uses to route OAuth requests to the Claude Code billing
	// pool (no per-token cost on the user). The Claude CLI sends it
	// as a real HTTP header — we mirror that.
	oauthBillingHeaderName = "x-anthropic-billing-header"
	// oauthBillingHeaderValue is the exact value the Claude CLI
	// sends. cc_version, cc_entrypoint and cch are billing-routing
	// metadata; the trailing semicolon matches the CLI's spelling
	// byte-for-byte. Kept as a constant — do NOT synthesise or
	// rotate dynamically until Anthropic documents that contract.
	oauthBillingHeaderValue = "cc_version=2.1.80.a46; " +
		"cc_entrypoint=sdk-cli; cch=00000;"
)

// Provider implements the provider.Provider interface for Anthropic Claude.
type Provider struct {
	client       anthropicAPI.Client
	isOAuth      bool
	tokenManager *TokenManager
	currentToken string
}

// New creates a new Anthropic provider with the given API key.
//
// Expected:
//   - apiKey is a non-empty Anthropic API key string.
//
// Returns:
//   - A configured Provider on success.
//   - An error if the API key is empty.
//
// Side effects:
//   - None.
func New(apiKey string) (*Provider, error) {
	if apiKey == "" {
		return nil, errAPIKeyRequired
	}
	client := anthropicAPI.NewClient(option.WithAPIKey(apiKey))
	return &Provider{client: client}, nil
}

// IsOAuthToken reports whether the given token is an Anthropic OAuth token.
//
// Expected:
//   - token is a string that may be an API key or OAuth token.
//
// Returns:
//   - true if the token starts with the OAuth prefix "sk-ant-oat01-".
//   - false otherwise.
//
// Side effects:
//   - None.
func IsOAuthToken(token string) bool {
	return strings.HasPrefix(token, oauthTokenPrefix)
}

// NewOAuth creates a new Anthropic provider configured for OAuth authentication.
//
// Expected:
//   - token is a non-empty Anthropic OAuth token string.
//
// Returns:
//   - A configured Provider on success.
//   - An error if the token is empty.
//
// Side effects:
//   - None.
func NewOAuth(token string) (*Provider, error) {
	if token == "" {
		return nil, errOAuthTokenRequired
	}
	return &Provider{
		client:       newOAuthClient(token),
		isOAuth:      true,
		tokenManager: NewDirectTokenManager(token),
		currentToken: token,
	}, nil
}

// NewOAuthWithRefresh creates an OAuth provider with automatic token refresh.
//
// Expected:
//   - tm is a non-nil TokenManager with valid credentials.
//
// Returns:
//   - A configured Provider that refreshes tokens automatically.
//   - An error if the initial token cannot be obtained.
//
// Side effects:
//   - May perform an HTTP token refresh.
func NewOAuthWithRefresh(tm *TokenManager) (*Provider, error) {
	token, err := tm.EnsureToken(context.Background())
	if err != nil {
		return nil, fmt.Errorf(
			"anthropic OAuth token refresh failed "+
				"(re-authenticate via `flowstate auth anthropic`): %w",
			err,
		)
	}
	return &Provider{
		client:       newOAuthClient(token),
		isOAuth:      true,
		tokenManager: tm,
		currentToken: token,
	}, nil
}

// newOAuthClient creates an Anthropic API client configured for OAuth bearer authentication.
//
// Expected:
//   - token is a non-empty OAuth bearer token.
//   - extraOpts are appended after the OAuth defaults so callers
//     (notably tests) can override the base URL, HTTP client, or add
//     further headers without rebuilding the OAuth-specific set.
//
// Returns:
//   - A configured Anthropic API client with OAuth headers and the
//     `x-anthropic-billing-header` wire-level header pre-attached.
//
// Side effects:
//   - None.
func newOAuthClient(token string, extraOpts ...option.RequestOption) anthropicAPI.Client {
	opts := []option.RequestOption{
		option.WithAuthToken(token),
		option.WithHeaderAdd("anthropic-beta", oauthBetaHeader),
		// `user-agent` must REPLACE the SDK default ("Anthropic/Go
		// <version>"), not append to it. The Claude CLI sends a
		// single user-agent value; if Anthropic's edge sees the
		// SDK default first it can re-classify the request out of
		// the Claude Code billing pool. WithHeaderAdd would leave
		// both values on the wire (SDK default first), so we use
		// WithHeader to overwrite.
		option.WithHeader("user-agent", oauthUserAgent),
		option.WithHeaderAdd("x-app", oauthAppHeader),
		// Real HTTP header — billing-routing metadata. The Claude
		// CLI sends this as a wire-level header; previously we
		// (incorrectly) injected it as a synthetic system-prompt
		// text block, which wasted ~30 input tokens per request,
		// polluted the model's view of the system prompt, and broke
		// the cache boundary by sitting before the first
		// caller-supplied cache_control breakpoint.
		option.WithHeaderAdd(oauthBillingHeaderName, oauthBillingHeaderValue),
	}
	opts = append(opts, extraOpts...)
	return anthropicAPI.NewClient(opts...)
}

// NewFromConfig creates an Anthropic provider from a configured API key or
// OAuth token. The credential is treated as an OAuth token when it has the
// "sk-ant-oat01-" prefix, otherwise as an API key.
//
// Expected:
//   - credential is an Anthropic API key or OAuth access token; empty when
//     no credential is configured.
//
// Returns:
//   - A configured Provider on success.
//   - errAPIKeyRequired when credential is empty.
//
// Side effects:
//   - None.
func NewFromConfig(credential string) (*Provider, error) {
	if credential == "" {
		return nil, errAPIKeyRequired
	}
	if IsOAuthToken(credential) {
		return NewOAuth(credential)
	}
	return New(credential)
}

// NewWithOptions creates a new Anthropic provider with the given API key and options.
//
// Expected:
//   - apiKey is a non-empty Anthropic API key string.
//   - opts is a variadic list of request options for the client.
//
// Returns:
//   - A configured Provider on success.
//   - An error if the API key is empty.
//
// Side effects:
//   - None.
func NewWithOptions(
	apiKey string, opts ...option.RequestOption,
) (*Provider, error) {
	if apiKey == "" {
		return nil, errAPIKeyRequired
	}
	allOpts := append(
		[]option.RequestOption{option.WithAPIKey(apiKey)}, opts...,
	)
	client := anthropicAPI.NewClient(allOpts...)
	return &Provider{client: client}, nil
}

// Name returns the provider name.
//
// Returns:
//   - The string "anthropic".
//
// Side effects:
//   - None.
func (p *Provider) Name() string {
	return providerName
}

// Stream sends a streaming chat request to the Anthropic API.
//
// Expected:
//   - ctx is a valid context for the API call.
//   - req contains the messages and model to use.
//
// Returns:
//   - A channel of StreamChunk values for the streamed response.
//   - An error if the request cannot be initiated.
//
// Side effects:
//   - Spawns a goroutine to read from the Anthropic streaming API.
//   - May refresh the OAuth token if expired.
func (p *Provider) Stream(
	ctx context.Context, req provider.ChatRequest,
) (<-chan provider.StreamChunk, error) {
	if err := p.refreshClientIfNeeded(ctx); err != nil {
		return nil, fmt.Errorf("refreshing token: %w", err)
	}
	params, opts, err := p.buildRequestParams(req)
	if err != nil {
		return nil, fmt.Errorf("building anthropic request: %w", err)
	}
	ch := make(chan provider.StreamChunk, streamChannelBuffSize)

	go p.streamMessages(ctx, params, opts, ch)

	return ch, nil
}

// streamMessages reads from the Anthropic streaming API and sends chunks to the channel.
//
// Expected:
//   - ctx is a valid context for the streaming call.
//   - params contains the configured Anthropic request parameters.
//   - opts carries per-call request options (e.g. conditional beta
//     headers); may be nil.
//   - ch is an open channel for receiving stream chunks.
//
// Side effects:
//   - Closes ch when streaming completes.
//   - Makes an HTTP streaming request to the Anthropic API.
func (p *Provider) streamMessages(
	ctx context.Context,
	params anthropicAPI.MessageNewParams,
	opts []option.RequestOption,
	ch chan<- provider.StreamChunk,
) {
	defer close(ch)

	handler := newStreamEventHandler()
	stream := p.client.Messages.NewStreaming(ctx, params, opts...)

	for stream.Next() {
		event := stream.Current()
		chunk, shouldSend := handler.handleEvent(event)
		if !shouldSend {
			continue
		}
		if !shared.SendChunk(ctx, ch, chunk) {
			return
		}
		if chunk.Done {
			return
		}
	}

	if err := stream.Err(); err != nil {
		streamErr := parseAnthropicStreamError(err)
		ch <- provider.StreamChunk{Error: streamErr, Done: true}
	}
}

// Chat sends a non-streaming chat request to the Anthropic API.
//
// Expected:
//   - ctx is a valid context for the API call.
//   - req contains the messages and model to use.
//
// Returns:
//   - A ChatResponse with the assistant's reply and token usage.
//   - An error if the API call fails.
//
// Side effects:
//   - Makes an HTTP request to the Anthropic API.
//   - May refresh the OAuth token if expired.
func (p *Provider) Chat(
	ctx context.Context, req provider.ChatRequest,
) (provider.ChatResponse, error) {
	if err := p.refreshClientIfNeeded(ctx); err != nil {
		return provider.ChatResponse{},
			fmt.Errorf("refreshing token: %w", err)
	}
	params, opts, err := p.buildRequestParams(req)
	if err != nil {
		return provider.ChatResponse{},
			fmt.Errorf("building anthropic request: %w", err)
	}

	resp, err := p.client.Messages.New(ctx, params, opts...)
	if err != nil {
		if provErr := parseAnthropicError(err); provErr != nil {
			return provider.ChatResponse{}, provErr
		}

		return provider.ChatResponse{},
			fmt.Errorf("anthropic chat failed: %w", err)
	}

	content := extractTextContent(resp.Content)

	return provider.ChatResponse{
		Message: provider.Message{
			Role:    string(resp.Role),
			Content: content,
		},
		Usage: provider.Usage{
			PromptTokens:     int(resp.Usage.InputTokens),
			CompletionTokens: int(resp.Usage.OutputTokens),
			TotalTokens: int(
				resp.Usage.InputTokens + resp.Usage.OutputTokens,
			),
		},
	}, nil
}

// refreshClientIfNeeded ensures the OAuth token is current and rebuilds the client if it changed.
//
// Expected:
//   - ctx is a valid context for potential token refresh.
//
// Returns:
//   - nil if the token is valid or was refreshed successfully.
//   - An error if token refresh fails.
//
// Side effects:
//   - May perform an HTTP token refresh.
//   - May replace the internal Anthropic API client.
func (p *Provider) refreshClientIfNeeded(
	ctx context.Context,
) error {
	if p.tokenManager == nil {
		return nil
	}
	token, err := p.tokenManager.EnsureToken(ctx)
	if err != nil {
		return err
	}
	if token != p.currentToken {
		p.client = newOAuthClient(token)
		p.currentToken = token
	}
	return nil
}

// Embed returns an error as Anthropic does not support embeddings.
//
// Expected:
//   - This method always fails as embeddings are not offered.
//
// Returns:
//   - nil and ErrNotSupported unconditionally.
//
// Side effects:
//   - None.
func (p *Provider) Embed(
	_ context.Context, _ provider.EmbedRequest,
) ([]float64, error) {
	return nil, ErrNotSupported
}

// Models returns the list of available Anthropic models.
//
// Returns:
//   - A slice of model definitions from the Anthropic Models API.
//   - A hardcoded fallback list if the API call fails.
//
// Side effects:
//   - Makes an HTTP request to the Anthropic Models API.
func (p *Provider) Models() ([]provider.Model, error) {
	models, err := p.fetchModels()
	if err == nil {
		return models, nil
	}
	return fallbackModels(), nil
}

// fetchModels retrieves the available model list from the Anthropic Models API.
//
// Returns:
//   - ([]provider.Model, nil) on success.
//   - (nil, error) if the API call fails.
//
// Side effects:
//   - Makes an HTTP request to the Anthropic Models API.
func (p *Provider) fetchModels() ([]provider.Model, error) {
	ctx := context.Background()
	pager := p.client.Models.ListAutoPaging(
		ctx, anthropicAPI.ModelListParams{},
	)

	var models []provider.Model
	for pager.Next() {
		info := pager.Current()
		models = append(models, provider.Model{
			ID:            info.ID,
			Provider:      providerName,
			ContextLength: defaultContextLength,
		})
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("listing anthropic models: %w", err)
	}
	return models, nil
}

// fallbackModels returns a hardcoded list of Anthropic models when the API is unavailable.
//
// Returns:
//   - A slice of commonly available Anthropic models.
//
// Side effects:
//   - None.
func fallbackModels() []provider.Model {
	return []provider.Model{
		{
			ID: "claude-sonnet-4-20250514", Provider: providerName,
			ContextLength: defaultContextLength,
		},
		{
			ID: "claude-3-5-haiku-latest", Provider: providerName,
			ContextLength: defaultContextLength,
		},
		{
			ID: "claude-opus-4-20250514", Provider: providerName,
			ContextLength: defaultContextLength,
		},
	}
}

// parseAnthropicError extracts a structured provider.Error from an Anthropic SDK error.
//
// Expected:
//   - err may be nil, a wrapped Anthropic API error, or any other error.
//
// Returns:
//   - A *provider.Error with mapped ErrorType if the error is an Anthropic API error.
//   - nil if err is nil or not an Anthropic API error.
//
// Side effects:
//   - None.
func parseAnthropicError(err error) *provider.Error {
	if err == nil {
		return nil
	}

	var apiErr *anthropicAPI.Error
	if !errors.As(err, &apiErr) {
		return nil
	}

	return mapAnthropicStatusCode(apiErr)
}

// mapAnthropicStatusCode maps an Anthropic API error's HTTP status to a provider.Error.
//
// Expected:
//   - apiErr is a non-nil Anthropic API error with a valid StatusCode.
//
// Returns:
//   - A *provider.Error with the appropriate ErrorType and retriability.
//
// Side effects:
//   - None.
func mapAnthropicStatusCode(apiErr *anthropicAPI.Error) *provider.Error {
	switch apiErr.StatusCode {
	case 429:
		return buildProviderError(apiErr, provider.ErrorTypeRateLimit, true)
	case 529:
		return buildProviderError(apiErr, provider.ErrorTypeOverload, true)
	case 401:
		return buildProviderError(apiErr, provider.ErrorTypeAuthFailure, false)
	case 400:
		return mapBadRequestError(apiErr)
	case 503:
		return buildProviderError(apiErr, provider.ErrorTypeOverload, true)
	case 500, 502, 504:
		return buildProviderError(apiErr, provider.ErrorTypeServerError, true)
	default:
		return buildProviderError(apiErr, provider.ErrorTypeUnknown, false)
	}
}

// mapBadRequestError classifies a 400 error as billing or unknown based on message content.
//
// Expected:
//   - apiErr is an Anthropic API error with StatusCode 400.
//
// Returns:
//   - A *provider.Error with ErrorTypeBilling if the message contains billing keywords.
//   - A *provider.Error with ErrorTypeUnknown otherwise.
//
// Side effects:
//   - None.
func mapBadRequestError(apiErr *anthropicAPI.Error) *provider.Error {
	if containsBillingKeyword(apiErr.Error()) {
		return buildProviderError(apiErr, provider.ErrorTypeBilling, false)
	}

	return buildProviderError(apiErr, provider.ErrorTypeUnknown, false)
}

// buildProviderError creates a provider.Error from an Anthropic API error.
//
// When the underlying HTTP response carries Anthropic's rate-limit
// metadata headers (`retry-after`, `anthropic-ratelimit-*`,
// `request-id`), they are parsed into provider.RateLimit and attached
// so failover schedulers can honour the carrier-issued back-off
// instead of guessing per error type. Headers absent or unparseable
// leave the field nil — callers must treat that as "no metadata
// available", not "limits zeroed".
//
// Expected:
//   - apiErr is a non-nil Anthropic API error.
//   - errType classifies the error.
//   - retriable indicates whether the request can be retried.
//
// Returns:
//   - A fully populated *provider.Error.
//
// Side effects:
//   - None.
func buildProviderError(
	apiErr *anthropicAPI.Error,
	errType provider.ErrorType,
	retriable bool,
) *provider.Error {
	return &provider.Error{
		HTTPStatus:  apiErr.StatusCode,
		ErrorType:   errType,
		Provider:    providerName,
		Message:     apiErr.Error(),
		RawError:    apiErr,
		IsRetriable: retriable,
		RateLimit:   extractRateLimitHeaders(apiErr),
	}
}

// extractRateLimitHeaders inspects the Anthropic SDK error's underlying
// http.Response for the documented rate-limit headers and returns a
// populated RateLimit when at least one header is present.
//
// Anthropic exposes per-window budgets for input tokens, output tokens,
// requests, and (sometimes) a combined token bucket. Each window has
// three headers — limit, remaining, reset (RFC 3339). The 429/529 path
// also carries `retry-after` (seconds) and every response carries
// `request-id` for support correlation.
//
// Returns nil when the SDK error has no Response, the Response has no
// Headers, or none of the documented headers are present.
//
// Expected:
//   - apiErr may be nil or carry a nil Response.
//
// Returns:
//   - A pointer to a RateLimit when at least one rate-limit header was
//     present.
//   - nil otherwise.
//
// Side effects:
//   - None.
func extractRateLimitHeaders(apiErr *anthropicAPI.Error) *provider.RateLimit {
	if apiErr == nil || apiErr.Response == nil {
		return nil
	}
	headers := apiErr.Response.Header
	if len(headers) == 0 {
		return nil
	}

	rl := newEmptyRateLimit()
	hasAny := readScalarHeaders(headers, apiErr.RequestID, &rl)
	hasAny = readWindowHeaders(headers, &rl) || hasAny

	if !hasAny {
		return nil
	}
	return &rl
}

// newEmptyRateLimit builds a RateLimit pre-populated with -1 sentinels
// so the caller can disambiguate "header not provided" from a real "0
// remaining". Reset times stay zero-valued.
//
// Returns:
//   - A RateLimit value ready to receive header-driven mutations.
//
// Side effects:
//   - None.
func newEmptyRateLimit() provider.RateLimit {
	return provider.RateLimit{
		InputTokensLimit:      -1,
		InputTokensRemaining:  -1,
		OutputTokensLimit:     -1,
		OutputTokensRemaining: -1,
		RequestsLimit:         -1,
		RequestsRemaining:     -1,
		TokensLimit:           -1,
		TokensRemaining:       -1,
	}
}

// readScalarHeaders captures the scalar (non-window) rate-limit
// signals: `retry-after`, `request-id`, and the SDK-extracted request
// ID fallback.
//
// Expected:
//   - headers is the response header map (may be empty).
//   - sdkRequestID is apiErr.RequestID; "" when not provided.
//   - rl is non-nil; mutated in place on hits.
//
// Returns:
//   - true when at least one scalar header was captured.
//
// Side effects:
//   - Mutates *rl on hits.
func readScalarHeaders(headers http.Header, sdkRequestID string, rl *provider.RateLimit) bool {
	hit := false
	if v := headers.Get("retry-after"); v != "" {
		if d, ok := parseRetryAfter(v); ok {
			rl.RetryAfter = d
			hit = true
		}
	}
	if v := headers.Get("request-id"); v != "" {
		rl.RequestID = v
		hit = true
	}
	if sdkRequestID != "" && rl.RequestID == "" {
		// SDK already extracted it via response_id; mirror so callers
		// never have to walk both surfaces.
		rl.RequestID = sdkRequestID
		hit = true
	}
	return hit
}

// readWindowHeaders captures the four limit/remaining/reset triples
// (input tokens, output tokens, requests, combined tokens). Decomposed
// out of extractRateLimitHeaders so the latter stays under the
// gocognit threshold.
//
// Expected:
//   - headers is the response header map (may be empty).
//   - rl is non-nil; mutated in place on hits.
//
// Returns:
//   - true when at least one window header was captured.
//
// Side effects:
//   - Mutates *rl on hits.
func readWindowHeaders(headers http.Header, rl *provider.RateLimit) bool {
	windows := []struct {
		limitHdr     string
		remainingHdr string
		resetHdr     string
		limitDst     *int
		remainingDst *int
		resetDst     *time.Time
	}{
		{
			"anthropic-ratelimit-input-tokens-limit",
			"anthropic-ratelimit-input-tokens-remaining",
			"anthropic-ratelimit-input-tokens-reset",
			&rl.InputTokensLimit, &rl.InputTokensRemaining,
			&rl.InputTokensReset,
		},
		{
			"anthropic-ratelimit-output-tokens-limit",
			"anthropic-ratelimit-output-tokens-remaining",
			"anthropic-ratelimit-output-tokens-reset",
			&rl.OutputTokensLimit, &rl.OutputTokensRemaining,
			&rl.OutputTokensReset,
		},
		{
			"anthropic-ratelimit-requests-limit",
			"anthropic-ratelimit-requests-remaining",
			"anthropic-ratelimit-requests-reset",
			&rl.RequestsLimit, &rl.RequestsRemaining,
			&rl.RequestsReset,
		},
		{
			"anthropic-ratelimit-tokens-limit",
			"anthropic-ratelimit-tokens-remaining",
			"anthropic-ratelimit-tokens-reset",
			&rl.TokensLimit, &rl.TokensRemaining,
			&rl.TokensReset,
		},
	}
	hit := false
	for _, w := range windows {
		if readIntHeader(headers, w.limitHdr, w.limitDst) {
			hit = true
		}
		if readIntHeader(headers, w.remainingHdr, w.remainingDst) {
			hit = true
		}
		if readTimeHeader(headers, w.resetHdr, w.resetDst) {
			hit = true
		}
	}
	return hit
}

// parseRetryAfter parses the `retry-after` HTTP header value. Anthropic
// emits the seconds form on 429/529; the spec also permits an
// HTTP-date, which we accept for forward-compat.
//
// Expected:
//   - value is the raw header text.
//
// Returns:
//   - The duration to wait and true on success.
//   - 0 and false when the header is absent, blank, or unparseable.
//
// Side effects:
//   - None.
func parseRetryAfter(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(value); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(value); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

// readIntHeader parses a non-negative integer header into dst and
// returns true when the header was present and parseable.
//
// Expected:
//   - headers is the response header map (may be empty).
//   - name is the header to look up.
//   - dst is non-nil; left untouched on parse failure.
//
// Returns:
//   - true when the header was present and successfully parsed.
//
// Side effects:
//   - Mutates *dst on success.
func readIntHeader(headers http.Header, name string, dst *int) bool {
	v := headers.Get(name)
	if v == "" {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return false
	}
	*dst = n
	return true
}

// readTimeHeader parses an RFC 3339 timestamp header into dst and
// returns true when the header was present and parseable.
//
// Expected:
//   - headers is the response header map (may be empty).
//   - name is the header to look up.
//   - dst is non-nil; left untouched on parse failure.
//
// Returns:
//   - true when the header was present and successfully parsed.
//
// Side effects:
//   - Mutates *dst on success.
func readTimeHeader(headers http.Header, name string, dst *time.Time) bool {
	v := strings.TrimSpace(headers.Get(name))
	if v == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return false
	}
	*dst = t
	return true
}

// parseAnthropicStreamError returns a structured provider.Error if the error is
// an Anthropic API error, otherwise returns the original error unchanged.
//
// Expected:
//   - err is a non-nil error from a streaming response.
//
// Returns:
//   - A *provider.Error if the error is a recognised Anthropic API error.
//   - The original error otherwise.
//
// Side effects:
//   - None.
func parseAnthropicStreamError(err error) error {
	if provErr := parseAnthropicError(err); provErr != nil {
		return provErr
	}

	return err
}

// containsBillingKeyword checks whether the message contains billing-related terms.
//
// Expected:
//   - msg is the error or response text to inspect.
//
// Returns:
//   - true when msg contains a billing-related keyword.
//   - false otherwise.
//
// Side effects:
//   - None.
func containsBillingKeyword(msg string) bool {
	lower := strings.ToLower(msg)

	return strings.Contains(lower, "billing") ||
		strings.Contains(lower, "credit") ||
		strings.Contains(lower, "balance")
}

// buildAssistantMessage converts a provider message to an Anthropic assistant message parameter.
//
// When the message carries thinking blocks (signed or redacted), they
// are prepended to the content blocks array. Anthropic requires that
// thinking blocks come BEFORE text and tool_use blocks on a replayed
// assistant message; without round-tripping them with their original
// signatures the server silently disables extended thinking on the
// next turn. Empty thinking blocks are skipped so a fresh first turn
// (no prior thinking) is unchanged.
//
// Expected:
//   - m is a message with role "assistant".
//
// Returns:
//   - A non-nil MessageParam if the message has content, tool calls,
//     or thinking blocks.
//   - nil if the message is empty.
//
// Side effects:
//   - None.
func buildAssistantMessage(
	m provider.Message,
) *anthropicAPI.MessageParam {
	if len(m.ToolCalls) > 0 {
		return buildAssistantWithTools(m)
	}
	thinkingBlocks := buildThinkingBlocks(m.ThinkingBlocks)
	if m.Content != "" {
		blocks := make(
			[]anthropicAPI.ContentBlockParamUnion,
			0, len(thinkingBlocks)+1,
		)
		blocks = append(blocks, thinkingBlocks...)
		blocks = append(blocks, anthropicAPI.NewTextBlock(m.Content))
		msg := anthropicAPI.NewAssistantMessage(blocks...)
		return &msg
	}
	if len(thinkingBlocks) > 0 {
		// Edge case: assistant turn that produced only thinking (no
		// visible text and no tool call). Anthropic accepts this on
		// replay — the thinking blocks alone are valid content.
		msg := anthropicAPI.NewAssistantMessage(thinkingBlocks...)
		return &msg
	}
	return nil
}

// buildAssistantWithTools creates an assistant message with thinking,
// text, and tool-use blocks in that order.
//
// Anthropic requires thinking blocks to precede tool_use blocks on
// replayed assistant messages — the API rejects (or silently drops
// thinking on) a request where tool_use comes before its associated
// thinking.
//
// Expected:
//   - m is a message with at least one tool call.
//
// Returns:
//   - A MessageParam containing thinking, text, and tool-use content
//     blocks.
//
// Side effects:
//   - None.
func buildAssistantWithTools(
	m provider.Message,
) *anthropicAPI.MessageParam {
	thinkingBlocks := buildThinkingBlocks(m.ThinkingBlocks)
	blocks := make(
		[]anthropicAPI.ContentBlockParamUnion,
		0, len(thinkingBlocks)+len(m.ToolCalls)+1,
	)
	blocks = append(blocks, thinkingBlocks...)
	if m.Content != "" {
		blocks = append(
			blocks, anthropicAPI.NewTextBlock(m.Content),
		)
	}
	for _, tc := range m.ToolCalls {
		blocks = append(blocks, anthropicAPI.NewToolUseBlock(
			shared.TranslateToolCallID(tc.ID, shared.ToolIDTargetAnthropic),
			tc.Arguments,
			tc.Name,
		))
	}
	msg := anthropicAPI.NewAssistantMessage(blocks...)
	return &msg
}

// buildThinkingBlocks converts captured ThinkingBlock records into
// Anthropic ContentBlockParamUnion values suitable for replay on a
// subsequent turn. Both signed thinking (Thinking + Signature) and
// redacted thinking (Redacted=true + Data) are supported; empty
// records are skipped.
//
// The Signature must be sent back UNCHANGED — without it the server
// rejects thinking continuity and silently re-disables thinking on
// the response, defeating per-model thinking opt-in.
func buildThinkingBlocks(
	blocks []provider.ThinkingBlock,
) []anthropicAPI.ContentBlockParamUnion {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]anthropicAPI.ContentBlockParamUnion, 0, len(blocks))
	for _, b := range blocks {
		switch {
		case b.Redacted && b.Data != "":
			out = append(out, anthropicAPI.NewRedactedThinkingBlock(b.Data))
		case b.Thinking != "" || b.Signature != "":
			out = append(out, anthropicAPI.NewThinkingBlock(b.Signature, b.Thinking))
		}
	}
	return out
}

// buildToolResultMessage creates an Anthropic user message containing tool result blocks.
//
// Expected:
//   - m is a message with role "tool" and associated tool calls.
//
// Returns:
//   - A non-nil MessageParam if tool results exist.
//   - nil if no tool calls are present.
//
// Side effects:
//   - None.
func buildToolResultMessage(
	m provider.Message,
) *anthropicAPI.MessageParam {
	blocks := make(
		[]anthropicAPI.ContentBlockParamUnion, 0, len(m.ToolCalls),
	)
	for _, tc := range m.ToolCalls {
		isError := strings.HasPrefix(m.Content, "Error:")
		blocks = append(blocks, anthropicAPI.NewToolResultBlock(
			shared.TranslateToolCallID(tc.ID, shared.ToolIDTargetAnthropic),
			m.Content,
			isError,
		))
	}
	if len(blocks) > 0 {
		msg := anthropicAPI.NewUserMessage(blocks...)
		return &msg
	}
	return nil
}

// sanitizeMessageSequence removes system messages and merges consecutive user messages.
//
// Expected:
//   - msgs is a slice of provider messages in conversation order.
//
// Returns:
//   - A sanitized slice with system messages removed and consecutive user messages merged.
//
// Side effects:
//   - None.
func sanitizeMessageSequence(
	msgs []provider.Message,
) []provider.Message {
	result := make([]provider.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "system" {
			continue
		}
		if len(result) == 0 {
			result = append(result, m)
			continue
		}
		last := &result[len(result)-1]
		if last.Role != "user" || m.Role != "user" {
			result = append(result, m)
			continue
		}
		mergeConsecutiveUserMessages(last, m)
	}
	return result
}

// mergeConsecutiveUserMessages appends content from m into last to combine adjacent user messages.
//
// Expected:
//   - last is a non-nil pointer to the preceding user message.
//   - m is the subsequent user message to merge.
//
// Side effects:
//   - Mutates last.Content by appending m.Content.
func mergeConsecutiveUserMessages(
	last *provider.Message, m provider.Message,
) {
	if m.Content != "" {
		if last.Content != "" {
			last.Content += "\n\n" + m.Content
		} else {
			last.Content = m.Content
		}
	}
}

// buildMessages converts provider messages to Anthropic API message parameters.
//
// Expected:
//   - msgs is a slice of provider messages in conversation order.
//
// Returns:
//   - A slice of Anthropic MessageParam values ready for the API.
//
// Side effects:
//   - None.
func buildMessages(
	msgs []provider.Message,
) []anthropicAPI.MessageParam {
	sanitized := sanitizeMessageSequence(msgs)
	messages := make(
		[]anthropicAPI.MessageParam, 0, len(sanitized),
	)
	for _, m := range sanitized {
		switch m.Role {
		case "user":
			messages = append(messages,
				anthropicAPI.NewUserMessage(
					anthropicAPI.NewTextBlock(m.Content),
				),
			)
		case "assistant":
			if msg := buildAssistantMessage(m); msg != nil {
				messages = append(messages, *msg)
			}
		case "tool":
			if msg := buildToolResultMessage(m); msg != nil {
				messages = append(messages, *msg)
			}
		}
	}
	return messages
}

// extractSystemPrompt collects system messages into text blocks.
//
// The OAuth billing-routing metadata is sent as a real HTTP header
// (`x-anthropic-billing-header`) configured on the SDK client — see
// newOAuthClient. It is intentionally NOT injected as a synthetic
// system-prompt block: doing so wastes input tokens, leaks routing
// metadata into the model's view, and (because the synthetic block
// has no cache_control) sits in front of the caller's cache
// breakpoint as an uncached prefix, invalidating the cache key.
//
// Cache breakpoint placement: a cache_control breakpoint anchors the
// END of a cacheable prefix. Only the LAST system block carries the
// breakpoint — earlier system blocks ride inside the prefix it anchors
// and do NOT need their own breakpoints. Marking every block was the
// historical implementation; it wasted breakpoint slots (Anthropic caps
// at 4 per request) without adding any cache-hit benefit. See the
// "extractSystemPrompt cache breakpoint placement" specs for the
// behaviour pin.
//
// Expected:
//   - msgs is a slice of provider messages that may include system messages.
//
// Returns:
//   - A slice of TextBlockParam values for the system prompt. The LAST
//     block (when any) carries an ephemeral cache_control breakpoint;
//     earlier blocks carry none.
//
// Side effects:
//   - None.
func (p *Provider) extractSystemPrompt(
	msgs []provider.Message,
) []anthropicAPI.TextBlockParam {
	var blocks []anthropicAPI.TextBlockParam
	for _, m := range msgs {
		if m.Role != "system" || m.Content == "" {
			continue
		}
		blocks = append(blocks, anthropicAPI.TextBlockParam{
			Text: m.Content,
		})
	}
	if len(blocks) > 0 {
		blocks[len(blocks)-1].CacheControl =
			anthropicAPI.NewCacheControlEphemeralParam()
	}
	return blocks
}

// buildTools converts provider tool definitions to Anthropic tool parameters.
//
// Expected:
//   - tools is a slice of provider tool definitions.
//
// Returns:
//   - A slice of Anthropic ToolUnionParam values, or nil if tools is empty.
//
// Side effects:
//   - None.
func buildTools(
	tools []provider.Tool,
) []anthropicAPI.ToolUnionParam {
	if len(tools) == 0 {
		return nil
	}
	result := make(
		[]anthropicAPI.ToolUnionParam, 0, len(tools),
	)
	for _, t := range tools {
		base := shared.BuildBaseToolSchema(t)
		toolParam := anthropicAPI.ToolParam{
			Name:        base.Name,
			Description: anthropicAPI.String(base.Description),
			InputSchema: anthropicAPI.ToolInputSchemaParam{
				Properties: base.Properties,
				Required:   base.Required,
			},
		}
		result = append(result, anthropicAPI.ToolUnionParam{
			OfTool: &toolParam,
		})
	}
	lastTool := result[len(result)-1].OfTool
	lastTool.CacheControl = anthropicAPI.NewCacheControlEphemeralParam()
	return result
}

// buildRequestParams assembles the Anthropic API request from a ChatRequest.
//
// The per-model contract (max_tokens default, sampling rules, thinking
// rewrite, tool_choice rules) is enforced via applyModelConstraints
// rather than being hard-coded in this function. See model_constraints.go
// for the per-model decision tree.
//
// In addition to the assembled MessageNewParams the function returns a
// slice of per-call request options carrying any anthropic-beta headers
// that depend on the assembled request (notably the
// interleaved-thinking-2025-05-14 header which is conditional on both
// thinking being on AND tools being present, only on the model families
// that need it). The OAuth client-level beta header is unrelated and
// unaffected.
//
// Expected:
//   - req contains the model, messages, and optional tools for the request.
//
// Returns:
//   - A fully configured MessageNewParams for the Anthropic API.
//   - The per-call request options to be passed to Messages.New /
//     Messages.NewStreaming. May be nil when no extra headers apply.
//   - An error when the per-model contract rejects the request
//     (e.g. Opus 4.7 + manual `thinking: enabled`, or a thinking budget
//     that fails validation).
//
// Side effects:
//   - None.
func (p *Provider) buildRequestParams(
	req provider.ChatRequest,
) (anthropicAPI.MessageNewParams, []option.RequestOption, error) {
	params := anthropicAPI.MessageNewParams{
		Model:    anthropicAPI.Model(req.Model),
		Messages: buildMessages(req.Messages),
	}

	if err := applyModelConstraints(&params, req); err != nil {
		return anthropicAPI.MessageNewParams{}, nil, err
	}

	sysBlocks := p.extractSystemPrompt(req.Messages)
	if len(sysBlocks) > 0 {
		params.System = sysBlocks
	}

	tools := buildTools(req.Tools)
	if len(tools) > 0 {
		params.Tools = tools
	}

	// Anchor the conversation prefix with a cache_control breakpoint
	// on the last assistant message — but only if the request still
	// has budget under Anthropic's 4-breakpoint cap. See
	// applyConversationCacheBreakpoint for the placement rule.
	applyConversationCacheBreakpoint(&params)

	defs := resolveModelDefaults(req.Model)
	betas := defs.betaHeaders(
		isThinkingActive(&params), len(tools) > 0, params.MaxTokens,
	)
	opts := buildBetaHeaderOptions(betas)

	return params, opts, nil
}

// maxCacheBreakpoints is Anthropic's hard cap on cache_control
// breakpoints per request. Exceeding it returns 400 from the API. The
// provider's strategy uses at most 3 (system + tools + last-assistant)
// so a fully-loaded request always has headroom.
const maxCacheBreakpoints = 4

// applyConversationCacheBreakpoint adds a cache_control breakpoint at
// the END of the last assistant message in the conversation. The last
// content block of that message is the one marked — anchoring the
// entire conversation prefix through the model's most recent turn.
// This is the highest-reuse cache key in a multi-turn agent loop:
// every follow-up turn replays the same prefix.
//
// The function counts the breakpoints already present on system blocks
// and tool definitions and skips the assistant breakpoint if doing so
// would exceed the per-request cap of 4. The earlier-prefix anchors
// are higher-reuse and larger, so we sacrifice the conversation
// breakpoint first under pressure.
//
// Expected:
//   - params.System and params.Tools have already had their breakpoints
//     placed by extractSystemPrompt and buildTools respectively.
//
// Returns:
//   - Nothing — params.Messages is mutated in place.
//
// Side effects:
//   - Mutates the LAST content block of the LAST assistant message in
//     params.Messages by setting its CacheControl field. Other messages
//     and blocks are untouched.
func applyConversationCacheBreakpoint(
	params *anthropicAPI.MessageNewParams,
) {
	existing := countExistingCacheBreakpoints(params)
	if existing >= maxCacheBreakpoints {
		// No headroom — the system or tool prefix already used all four
		// slots (should never happen under the current strategy, but
		// guard defensively so we never send a 5th breakpoint).
		return
	}
	idx := lastAssistantIndex(params.Messages)
	if idx < 0 {
		// No assistant turn yet — nothing to anchor.
		return
	}
	msg := &params.Messages[idx]
	if len(msg.Content) == 0 {
		return
	}
	last := &msg.Content[len(msg.Content)-1]
	switch {
	case last.OfText != nil:
		last.OfText.CacheControl =
			anthropicAPI.NewCacheControlEphemeralParam()
	case last.OfToolUse != nil:
		last.OfToolUse.CacheControl =
			anthropicAPI.NewCacheControlEphemeralParam()
	case last.OfToolResult != nil:
		last.OfToolResult.CacheControl =
			anthropicAPI.NewCacheControlEphemeralParam()
	}
	// Thinking and other block kinds do not accept cache_control on
	// the v1.27 SDK — fall through silently if the assistant turn
	// happened to end on one of those.
}

// lastAssistantIndex returns the index of the last assistant message
// in messages, or -1 when none exists.
func lastAssistantIndex(messages []anthropicAPI.MessageParam) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if string(messages[i].Role) == "assistant" {
			return i
		}
	}
	return -1
}

// countExistingCacheBreakpoints counts cache_control breakpoints
// already present on system blocks and tool definitions. Used to gate
// the conversation breakpoint against the per-request cap.
func countExistingCacheBreakpoints(
	params *anthropicAPI.MessageNewParams,
) int {
	count := 0
	for i := range params.System {
		if string(params.System[i].CacheControl.Type) != "" {
			count++
		}
	}
	for i := range params.Tools {
		t := params.Tools[i].OfTool
		if t != nil && string(t.CacheControl.Type) != "" {
			count++
		}
	}
	return count
}

// buildBetaHeaderOptions converts a slice of anthropic-beta header
// values into per-call request options. Each value is added via a
// separate WithHeaderAdd call so the SDK appends them onto the same
// `anthropic-beta` header (HTTP comma-joins multiple values), which is
// what Anthropic and the official client expect.
//
// Expected:
//   - betas is a slice of beta-feature names (may be nil/empty).
//
// Returns:
//   - A slice of option.RequestOption values, or nil when betas is empty.
//
// Side effects:
//   - None.
func buildBetaHeaderOptions(betas []string) []option.RequestOption {
	if len(betas) == 0 {
		return nil
	}
	opts := make([]option.RequestOption, 0, len(betas))
	for _, b := range betas {
		opts = append(opts, option.WithHeaderAdd("anthropic-beta", b))
	}
	return opts
}

// extractTextContent returns the text from the first text-type content block.
//
// Expected:
//   - blocks is a slice of Anthropic content blocks from a response.
//
// Returns:
//   - The text content of the first text block, or empty string if none found.
//
// Side effects:
//   - None.
func extractTextContent(
	blocks []anthropicAPI.ContentBlockUnion,
) string {
	for i := range blocks {
		if blocks[i].Type == "text" {
			return blocks[i].Text
		}
	}
	return ""
}
