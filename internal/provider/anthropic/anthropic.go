// Package anthropic provides an Anthropic Claude API provider implementation.
package anthropic

import (
	"context"
	"errors"
	"fmt"

	anthropicAPI "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/baphled/flowstate/internal/auth"
	"github.com/baphled/flowstate/internal/provider"
)

// ErrNotSupported is returned when an unsupported operation is attempted.
var ErrNotSupported = errors.New("anthropic does not support embeddings")

var errAPIKeyRequired = errors.New("anthropic API key is required")

const (
	providerName          = "anthropic"
	defaultContextLength  = 200000
	streamChannelBuffSize = 16
	defaultMaxTokens      = 4096
)

// Provider implements the provider.Provider interface for Anthropic Claude.
type Provider struct {
	client anthropicAPI.Client
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
		authData, err := auth.LoadOpenCodeAuthFrom(opencodePath)
		if err != nil {
			return nil, fmt.Errorf("loading opencode auth: %w", err)
		}
		if authData != nil && authData.Anthropic != nil && authData.Anthropic.Access != "" {
			return New(authData.Anthropic.Access)
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

	params := buildRequestParams(req)

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
	params := buildRequestParams(req)

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
	messages := make([]anthropicAPI.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			messages = append(messages, anthropicAPI.NewUserMessage(anthropicAPI.NewTextBlock(m.Content)))
		case "assistant":
			if m.Content == "" {
				continue
			}
			messages = append(messages, anthropicAPI.NewAssistantMessage(anthropicAPI.NewTextBlock(m.Content)))
		}
	}
	return messages
}

// extractSystemPrompt collects all system role messages and returns them as
// Anthropic TextBlockParam values suitable for the MessageNewParams.System field.
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
func extractSystemPrompt(msgs []provider.Message) []anthropicAPI.TextBlockParam {
	var blocks []anthropicAPI.TextBlockParam
	for _, m := range msgs {
		if m.Role == "system" && m.Content != "" {
			blocks = append(blocks, anthropicAPI.TextBlockParam{
				Text:         m.Content,
				CacheControl: anthropicAPI.NewCacheControlEphemeralParam(),
			})
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
func buildRequestParams(req provider.ChatRequest) anthropicAPI.MessageNewParams {
	params := anthropicAPI.MessageNewParams{
		Model:       req.Model,
		MaxTokens:   defaultMaxTokens,
		Temperature: anthropicAPI.Float(0),
		Messages:    buildMessages(req.Messages),
	}

	if systemBlocks := extractSystemPrompt(req.Messages); len(systemBlocks) > 0 {
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
