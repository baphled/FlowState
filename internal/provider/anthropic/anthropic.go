// Package anthropic provides an Anthropic Claude API provider implementation.
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	anthropicAPI "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/baphled/flowstate/internal/auth"
	"github.com/baphled/flowstate/internal/provider"
)

// ErrNotSupported is returned when an unsupported operation is attempted.
var ErrNotSupported = errors.New("anthropic does not support embeddings")

var errAPIKeyRequired = errors.New("anthropic API key is required")
var errOAuthTokenRequired = errors.New("anthropic OAuth token is required")
var errNoOpenCodeCredentials = errors.New(
	"no anthropic credentials in opencode auth",
)

const (
	providerName          = "anthropic"
	defaultContextLength  = 200000
	streamChannelBuffSize = 16
	defaultMaxTokens      = 4096
	oauthTokenPrefix      = "sk-ant-oat01-"
	oauthBetaHeader       = "oauth-2025-04-20"
	oauthUserAgent        = "claude-cli/2.1.2 (external, cli)"
	oauthAppHeader        = "cli"
	oauthBillingHeader    = "x-anthropic-billing-header: " +
		"cc_version=2.1.80.a46; cc_entrypoint=sdk-cli; cch=00000;"
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
		return nil, fmt.Errorf("obtaining initial token: %w", err)
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
//
// Returns:
//   - A configured Anthropic API client with OAuth headers.
//
// Side effects:
//   - None.
func newOAuthClient(token string) anthropicAPI.Client {
	return anthropicAPI.NewClient(
		option.WithAuthToken(token),
		option.WithHeaderAdd("anthropic-beta", oauthBetaHeader),
		option.WithHeaderAdd("user-agent", oauthUserAgent),
		option.WithHeaderAdd("x-app", oauthAppHeader),
	)
}

// tryOpenCodeAuth attempts to load Anthropic credentials from OpenCode auth.json.
//
// Expected:
//   - opencodePath is a file path to OpenCode's auth.json.
//
// Returns:
//   - (*Provider, nil) if valid credentials found.
//   - (nil, error) if auth file exists but cannot be parsed.
//   - (nil, errNoOpenCodeCredentials) if no credentials found.
//
// Side effects:
//   - Reads from opencodePath.
func tryOpenCodeAuth(opencodePath string) (*Provider, error) {
	authData, err := auth.LoadOpenCodeAuthFrom(opencodePath)
	if err != nil {
		return nil, fmt.Errorf("loading opencode auth: %w", err)
	}
	if authData == nil || authData.Anthropic == nil {
		return nil, errNoOpenCodeCredentials
	}
	if authData.Anthropic.Access == "" {
		return nil, errNoOpenCodeCredentials
	}
	token := authData.Anthropic.Access
	if !IsOAuthToken(token) {
		return New(token)
	}
	return buildOAuthProvider(authData.Anthropic, opencodePath)
}

// buildOAuthProvider creates an OAuth provider with optional token refresh from auth credentials.
//
// Expected:
//   - pa contains valid Anthropic OAuth credentials.
//   - authPath is the file path to auth.json for token persistence.
//
// Returns:
//   - (*Provider, nil) on success.
//   - (nil, error) if provider creation fails.
//
// Side effects:
//   - May perform an HTTP token refresh via NewOAuthWithRefresh.
func buildOAuthProvider(
	pa *auth.ProviderAuth, authPath string,
) (*Provider, error) {
	if pa.Refresh == "" {
		return NewOAuth(pa.Access)
	}
	refresher := &HTTPTokenRefresher{
		Client: &http.Client{},
	}
	tm := NewTokenManager(
		pa.Access, pa.Refresh, pa.Expires,
		refresher, authPath,
	)
	return NewOAuthWithRefresh(tm)
}

// NewFromOpenCodeOrConfig attempts to load Anthropic credentials from OpenCode
// auth.json, falling back to the provided API key from config.
//
// Expected:
//   - opencodePath is a file path to auth.json (or empty to skip).
//   - fallbackKey is the API key from config.yaml (may be empty).
//
// Returns:
//   - A configured Provider using OpenCode credential if found.
//   - A configured Provider using fallbackKey if OpenCode not available.
//   - An error if neither source provides a valid credential.
//
// Side effects:
//   - Reads from opencodePath if provided.
func NewFromOpenCodeOrConfig(
	opencodePath string, fallbackKey string,
) (*Provider, error) {
	if opencodePath != "" {
		p, err := tryOpenCodeAuth(opencodePath)
		if err != nil && !isExpectedAuthError(err) {
			return nil, err
		}
		if p != nil {
			return p, nil
		}
	}

	if fallbackKey != "" {
		return New(fallbackKey)
	}

	return nil, errAPIKeyRequired
}

// isExpectedAuthError reports whether the error is a benign auth-file-not-found condition.
//
// Expected:
//   - err is a non-nil error to classify.
//
// Returns:
//   - true if the error indicates missing credentials or auth file.
//   - false for unexpected errors that should be propagated.
//
// Side effects:
//   - None.
func isExpectedAuthError(err error) bool {
	return errors.Is(err, errNoOpenCodeCredentials) ||
		errors.Is(err, auth.ErrAuthFileNotFound) ||
		errors.Is(err, auth.ErrNoCredentials)
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
	ch := make(chan provider.StreamChunk, streamChannelBuffSize)
	params := p.buildRequestParams(req)

	go p.streamMessages(ctx, params, ch)

	return ch, nil
}

// streamMessages reads from the Anthropic streaming API and sends chunks to the channel.
//
// Expected:
//   - ctx is a valid context for the streaming call.
//   - params contains the configured Anthropic request parameters.
//   - ch is an open channel for receiving stream chunks.
//
// Side effects:
//   - Closes ch when streaming completes.
//   - Makes an HTTP streaming request to the Anthropic API.
func (p *Provider) streamMessages(
	ctx context.Context,
	params anthropicAPI.MessageNewParams,
	ch chan<- provider.StreamChunk,
) {
	defer close(ch)

	stream := p.client.Messages.NewStreaming(ctx, params)

	for stream.Next() {
		event := stream.Current()
		chunk := convertStreamEvent(event)
		if chunk.Content == "" && !chunk.Done && chunk.Error == nil {
			continue
		}
		select {
		case <-ctx.Done():
			ch <- provider.StreamChunk{
				Error: ctx.Err(), Done: true,
			}
			return
		case ch <- chunk:
		}
	}

	if err := stream.Err(); err != nil {
		ch <- provider.StreamChunk{Error: err, Done: true}
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
	params := p.buildRequestParams(req)

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
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

// buildAssistantMessage converts a provider message to an Anthropic assistant message parameter.
//
// Expected:
//   - m is a message with role "assistant".
//
// Returns:
//   - A non-nil MessageParam if the message has content or tool calls.
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
	if m.Content != "" {
		msg := anthropicAPI.NewAssistantMessage(
			anthropicAPI.NewTextBlock(m.Content),
		)
		return &msg
	}
	return nil
}

// buildAssistantWithTools creates an assistant message with text and tool-use blocks.
//
// Expected:
//   - m is a message with at least one tool call.
//
// Returns:
//   - A MessageParam containing text and tool-use content blocks.
//
// Side effects:
//   - None.
func buildAssistantWithTools(
	m provider.Message,
) *anthropicAPI.MessageParam {
	blocks := make(
		[]anthropicAPI.ContentBlockParamUnion,
		0, len(m.ToolCalls)+1,
	)
	if m.Content != "" {
		blocks = append(
			blocks, anthropicAPI.NewTextBlock(m.Content),
		)
	}
	for _, tc := range m.ToolCalls {
		blocks = append(blocks, anthropicAPI.NewToolUseBlock(
			tc.ID, tc.Arguments, tc.Name,
		))
	}
	msg := anthropicAPI.NewAssistantMessage(blocks...)
	return &msg
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
			tc.ID, m.Content, isError,
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

// extractSystemPrompt collects system messages into text blocks, prepending the billing header for OAuth.
//
// Expected:
//   - msgs is a slice of provider messages that may include system messages.
//
// Returns:
//   - A slice of TextBlockParam values for the system prompt.
//
// Side effects:
//   - None.
func (p *Provider) extractSystemPrompt(
	msgs []provider.Message,
) []anthropicAPI.TextBlockParam {
	var blocks []anthropicAPI.TextBlockParam
	if p.isOAuth {
		blocks = append(blocks, anthropicAPI.TextBlockParam{
			Text: oauthBillingHeader,
		})
	}
	for _, m := range msgs {
		if m.Role != "system" || m.Content == "" {
			continue
		}
		block := anthropicAPI.TextBlockParam{Text: m.Content}
		if !p.isOAuth {
			block.CacheControl = anthropicAPI.NewCacheControlEphemeralParam()
		}
		blocks = append(blocks, block)
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
		toolParam := anthropicAPI.ToolParam{
			Name:        t.Name,
			Description: anthropicAPI.String(t.Description),
			InputSchema: anthropicAPI.ToolInputSchemaParam{
				Properties: t.Schema.Properties,
				Required:   t.Schema.Required,
			},
		}
		result = append(result, anthropicAPI.ToolUnionParam{
			OfTool: &toolParam,
		})
	}
	return result
}

// buildRequestParams assembles the Anthropic API request from a ChatRequest.
//
// Expected:
//   - req contains the model, messages, and optional tools for the request.
//
// Returns:
//   - A fully configured MessageNewParams for the Anthropic API.
//
// Side effects:
//   - None.
func (p *Provider) buildRequestParams(
	req provider.ChatRequest,
) anthropicAPI.MessageNewParams {
	params := anthropicAPI.MessageNewParams{
		Model:       req.Model,
		MaxTokens:   defaultMaxTokens,
		Temperature: anthropicAPI.Float(0),
		Messages:    buildMessages(req.Messages),
	}

	sysBlocks := p.extractSystemPrompt(req.Messages)
	if len(sysBlocks) > 0 {
		params.System = sysBlocks
	}

	if tools := buildTools(req.Tools); len(tools) > 0 {
		params.Tools = tools
	}

	return params
}

// convertStreamEvent maps an Anthropic stream event to a provider StreamChunk.
//
// Expected:
//   - event is a valid Anthropic streaming event.
//
// Returns:
//   - A StreamChunk with content, tool call data, or done signal.
//
// Side effects:
//   - None.
func convertStreamEvent(
	event anthropicAPI.MessageStreamEventUnion,
) provider.StreamChunk {
	switch event.Type {
	case "content_block_delta":
		if event.Delta.Type == "text_delta" {
			return provider.StreamChunk{Content: event.Delta.Text}
		}
	case "message_stop":
		return provider.StreamChunk{Done: true}
	case "content_block_start":
		if event.ContentBlock.Type == "tool_use" {
			return provider.StreamChunk{
				EventType: "tool_call",
				ToolCall: &provider.ToolCall{
					ID:   event.ContentBlock.ID,
					Name: event.ContentBlock.Name,
				},
			}
		}
	}
	return provider.StreamChunk{}
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
