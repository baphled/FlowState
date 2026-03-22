// Package anthropic provides an Anthropic Claude API provider implementation.
package anthropic

import (
	"context"
	"errors"
	"fmt"
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
var errNoOpenCodeCredentials = errors.New("no anthropic credentials in opencode auth")

const (
	providerName          = "anthropic"
	defaultContextLength  = 200000
	streamChannelBuffSize = 16
	defaultMaxTokens      = 4096
	oauthTokenPrefix      = "sk-ant-oat01-"
	oauthBetaHeader       = "oauth-2025-04-20"
	oauthUserAgent        = "claude-cli/2.1.2 (external, cli)"
	oauthAppHeader        = "cli"
	oauthBillingHeader    = "x-anthropic-billing-header: cc_version=2.1.80.a46; cc_entrypoint=sdk-cli; cch=00000;"
)

// Provider implements the provider.Provider interface for Anthropic Claude.
type Provider struct {
	client  anthropicAPI.Client
	isOAuth bool
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

// NewOAuth creates a new Anthropic provider configured for OAuth token authentication.
// OAuth tokens use the Authorization: Bearer header instead of X-Api-Key.
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
	client := anthropicAPI.NewClient(
		option.WithAuthToken(token),
		option.WithHeaderAdd("anthropic-beta", oauthBetaHeader),
		option.WithHeaderAdd("user-agent", oauthUserAgent),
		option.WithHeaderAdd("x-app", oauthAppHeader),
	)
	return &Provider{client: client, isOAuth: true}, nil
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
	if authData != nil && authData.Anthropic != nil && authData.Anthropic.Access != "" {
		token := authData.Anthropic.Access
		if IsOAuthToken(token) {
			return NewOAuth(token)
		}
		return New(token)
	}
	return nil, errNoOpenCodeCredentials
}

// NewFromOpenCodeOrConfig attempts to load Anthropic credentials from OpenCode auth.json,
// falling back to the provided API key from config.
//
// Expected:
//   - opencodePath is a file path to OpenCode's auth.json (or empty string to skip OpenCode).
//   - fallbackKey is the API key from config.yaml (may be empty).
//
// Returns:
//   - A configured Provider using OpenCode credential if found and valid.
//   - A configured Provider using fallbackKey if OpenCode not available.
//   - An error if OpenCode exists but cannot be parsed.
//   - An error if neither OpenCode nor fallback key is available.
//
// Side effects:
//   - Reads from opencodePath if provided.
func NewFromOpenCodeOrConfig(opencodePath string, fallbackKey string) (*Provider, error) {
	if opencodePath != "" {
		p, err := tryOpenCodeAuth(opencodePath)
		if err != nil &&
			!errors.Is(err, errNoOpenCodeCredentials) &&
			!errors.Is(err, auth.ErrAuthFileNotFound) &&
			!errors.Is(err, auth.ErrNoCredentials) {
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
func NewWithOptions(apiKey string, opts ...option.RequestOption) (*Provider, error) {
	if apiKey == "" {
		return nil, errAPIKeyRequired
	}
	allOpts := append([]option.RequestOption{option.WithAPIKey(apiKey)}, opts...)
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
//   - A channel of StreamChunk values containing the streamed response.
//   - An error if the request cannot be initiated.
//
// Side effects:
//   - Spawns a goroutine to read from the Anthropic streaming API.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, streamChannelBuffSize)

	params := p.buildRequestParams(req)

	go func() {
		defer close(ch)

		stream := p.client.Messages.NewStreaming(ctx, params)

		for stream.Next() {
			event := stream.Current()
			chunk := convertStreamEvent(event)
			if chunk.Content != "" || chunk.Done || chunk.Error != nil {
				select {
				case <-ctx.Done():
					ch <- provider.StreamChunk{Error: ctx.Err(), Done: true}
					return
				case ch <- chunk:
				}
			}
		}

		if err := stream.Err(); err != nil {
			ch <- provider.StreamChunk{Error: err, Done: true}
		}
	}()

	return ch, nil
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
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	params := p.buildRequestParams(req)

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return provider.ChatResponse{}, fmt.Errorf("anthropic chat failed: %w", err)
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
			TotalTokens:      int(resp.Usage.InputTokens + resp.Usage.OutputTokens),
		},
	}, nil
}

// Embed returns an error as Anthropic does not support embeddings.
//
// Expected:
//   - This method always fails as Anthropic does not offer embedding support.
//
// Returns:
//   - nil and ErrNotSupported unconditionally.
//
// Side effects:
//   - None.
func (p *Provider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, ErrNotSupported
}

// Models returns the list of available Anthropic models by querying the API.
//
// Returns:
//   - A slice of model definitions fetched from the Anthropic Models API.
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

// fetchModels queries the Anthropic Models API for available models.
//
// Returns:
//   - A slice of provider.Model values from the API.
//   - An error if the API call fails.
//
// Side effects:
//   - Makes an HTTP request to the Anthropic Models API.
func (p *Provider) fetchModels() ([]provider.Model, error) {
	ctx := context.Background()
	pager := p.client.Models.ListAutoPaging(ctx, anthropicAPI.ModelListParams{})

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

// fallbackModels returns a hardcoded list of known Anthropic models.
//
// Returns:
//   - A static slice of well-known Anthropic model definitions.
//
// Side effects:
//   - None.
func fallbackModels() []provider.Model {
	return []provider.Model{
		{ID: "claude-sonnet-4-20250514", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "claude-3-5-haiku-latest", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "claude-opus-4-20250514", Provider: providerName, ContextLength: defaultContextLength},
	}
}

// buildAssistantMessage constructs an assistant message with optional tool calls.
//
// Expected:
//   - m is a provider message with role "assistant".
//
// Returns:
//   - A MessageParam for the assistant message, or nil if empty.
//
// Side effects:
//   - None.
func buildAssistantMessage(m provider.Message) *anthropicAPI.MessageParam {
	if len(m.ToolCalls) > 0 {
		blocks := make([]anthropicAPI.ContentBlockParamUnion, 0, len(m.ToolCalls)+1)
		if m.Content != "" {
			blocks = append(blocks, anthropicAPI.NewTextBlock(m.Content))
		}
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, anthropicAPI.NewToolUseBlock(tc.ID, tc.Arguments, tc.Name))
		}
		msg := anthropicAPI.NewAssistantMessage(blocks...)
		return &msg
	}
	if m.Content != "" {
		msg := anthropicAPI.NewAssistantMessage(anthropicAPI.NewTextBlock(m.Content))
		return &msg
	}
	return nil
}

// buildToolResultMessage constructs a user message containing tool result blocks.
//
// Expected:
//   - m is a provider message with role "tool".
//
// Returns:
//   - A MessageParam for the tool result message, or nil if empty.
//
// Side effects:
//   - None.
func buildToolResultMessage(m provider.Message) *anthropicAPI.MessageParam {
	blocks := make([]anthropicAPI.ContentBlockParamUnion, 0, len(m.ToolCalls))
	for _, tc := range m.ToolCalls {
		isError := strings.HasPrefix(m.Content, "Error:")
		blocks = append(blocks, anthropicAPI.NewToolResultBlock(tc.ID, m.Content, isError))
	}
	if len(blocks) > 0 {
		msg := anthropicAPI.NewUserMessage(blocks...)
		return &msg
	}
	return nil
}

// sanitizeMessageSequence ensures the message sequence is valid for Anthropic's API,
// which requires strict user/assistant alternation.
//
// Expected:
//   - msgs is a slice of provider messages from the context window.
//
// Returns:
//   - A sanitized slice where consecutive user messages are merged into one.
//   - System messages are excluded (handled separately by extractSystemPrompt).
//
// Side effects:
//   - None.
func sanitizeMessageSequence(msgs []provider.Message) []provider.Message {
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

// mergeConsecutiveUserMessages combines two consecutive user messages into the first one.
//
// Expected:
//   - last is a pointer to a user message to be updated.
//   - m is the next user message to merge into last.
//
// Side effects:
//   - Updates last.Content in place by appending m.Content with "\n\n" separator if both non-empty.
func mergeConsecutiveUserMessages(last *provider.Message, m provider.Message) {
	if m.Content != "" {
		if last.Content != "" {
			last.Content += "\n\n" + m.Content
		} else {
			last.Content = m.Content
		}
	}
}

// buildMessages converts provider messages to Anthropic API message parameters,
// filtering out system role messages which are handled separately.
//
// Expected:
//   - msgs is a slice of provider messages with role and content fields.
//
// Returns:
//   - A slice of Anthropic MessageParam values.
//   - Only user and assistant roles are converted; system and other roles are skipped.
//
// Side effects:
//   - None.
func buildMessages(msgs []provider.Message) []anthropicAPI.MessageParam {
	sanitized := sanitizeMessageSequence(msgs)
	messages := make([]anthropicAPI.MessageParam, 0, len(sanitized))
	for _, m := range sanitized {
		switch m.Role {
		case "user":
			messages = append(messages, anthropicAPI.NewUserMessage(anthropicAPI.NewTextBlock(m.Content)))
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

// extractSystemPrompt collects all system role messages and returns them as
// Anthropic TextBlockParam values suitable for the MessageNewParams.System field.
// When the provider uses OAuth authentication, CacheControl is omitted because
// prompt caching is not supported on the OAuth path.
//
// Expected:
//   - msgs is a slice of provider messages that may include system role messages.
//
// Returns:
//   - A slice of TextBlockParam values from system messages.
//   - An empty slice if no system messages exist.
//
// Side effects:
//   - None.
func (p *Provider) extractSystemPrompt(msgs []provider.Message) []anthropicAPI.TextBlockParam {
	var blocks []anthropicAPI.TextBlockParam
	if p.isOAuth {
		blocks = append(blocks, anthropicAPI.TextBlockParam{Text: oauthBillingHeader})
	}
	for _, m := range msgs {
		if m.Role == "system" && m.Content != "" {
			block := anthropicAPI.TextBlockParam{Text: m.Content}
			if !p.isOAuth {
				block.CacheControl = anthropicAPI.NewCacheControlEphemeralParam()
			}
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// buildTools converts provider tool definitions to Anthropic API tool parameters.
//
// Expected:
//   - tools is a slice of provider.Tool values with name, description, and schema.
//
// Returns:
//   - A slice of Anthropic ToolUnionParam values.
//   - An empty slice if no tools are provided.
//
// Side effects:
//   - None.
func buildTools(tools []provider.Tool) []anthropicAPI.ToolUnionParam {
	if len(tools) == 0 {
		return nil
	}
	result := make([]anthropicAPI.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		toolParam := anthropicAPI.ToolParam{
			Name:        t.Name,
			Description: anthropicAPI.String(t.Description),
			InputSchema: anthropicAPI.ToolInputSchemaParam{
				Properties: t.Schema.Properties,
				Required:   t.Schema.Required,
			},
		}
		result = append(result, anthropicAPI.ToolUnionParam{OfTool: &toolParam})
	}
	return result
}

// buildRequestParams constructs the Anthropic MessageNewParams from a ChatRequest,
// properly mapping system prompts, messages, tools, and model.
//
// Expected:
//   - req is a valid provider.ChatRequest.
//
// Returns:
//   - A fully populated MessageNewParams ready for the Anthropic API.
//
// Side effects:
//   - None.
func (p *Provider) buildRequestParams(req provider.ChatRequest) anthropicAPI.MessageNewParams {
	params := anthropicAPI.MessageNewParams{
		Model:       req.Model,
		MaxTokens:   defaultMaxTokens,
		Temperature: anthropicAPI.Float(0),
		Messages:    buildMessages(req.Messages),
	}

	if systemBlocks := p.extractSystemPrompt(req.Messages); len(systemBlocks) > 0 {
		params.System = systemBlocks
	}

	if tools := buildTools(req.Tools); len(tools) > 0 {
		params.Tools = tools
	}

	return params
}

// convertStreamEvent transforms an Anthropic stream event into a provider StreamChunk.
//
// Expected:
//   - event is a valid Anthropic MessageStreamEventUnion.
//
// Returns:
//   - A StreamChunk with content, tool call, or done flag set appropriately.
//   - An empty StreamChunk if the event type is not recognised.
//
// Side effects:
//   - None.
func convertStreamEvent(event anthropicAPI.MessageStreamEventUnion) provider.StreamChunk {
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

// extractTextContent retrieves the first text block from a content block slice.
//
// Expected:
//   - blocks is a slice of Anthropic content blocks (may be empty).
//
// Returns:
//   - The text content of the first text block found.
//   - An empty string if no text block exists.
//
// Side effects:
//   - None.
func extractTextContent(blocks []anthropicAPI.ContentBlockUnion) string {
	for i := range blocks {
		if blocks[i].Type == "text" {
			return blocks[i].Text
		}
	}
	return ""
}
