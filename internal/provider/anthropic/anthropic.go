// Package anthropic provides an Anthropic Claude API provider implementation.
package anthropic

import (
	"context"
	"errors"
	"fmt"

	anthropicAPI "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
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
func New(apiKey string) (*Provider, error) {
	if apiKey == "" {
		return nil, errAPIKeyRequired
	}
	client := anthropicAPI.NewClient(option.WithAPIKey(apiKey))
	return &Provider{client: client}, nil
}

// NewWithOptions creates a new Anthropic provider with the given API key and options.
func NewWithOptions(apiKey string, opts ...option.RequestOption) (*Provider, error) {
	if apiKey == "" {
		return nil, errAPIKeyRequired
	}
	allOpts := append([]option.RequestOption{option.WithAPIKey(apiKey)}, opts...)
	client := anthropicAPI.NewClient(allOpts...)
	return &Provider{client: client}, nil
}

// Name returns the provider name.
func (p *Provider) Name() string {
	return providerName
}

// Stream implements provider.Provider.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, streamChannelBuffSize)

	messages := buildMessages(req.Messages)

	go func() {
		defer close(ch)

		stream := p.client.Messages.NewStreaming(ctx, anthropicAPI.MessageNewParams{
			Model:     req.Model,
			MaxTokens: defaultMaxTokens,
			Messages:  messages,
		})

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

// Chat implements provider.Provider.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	messages := buildMessages(req.Messages)

	resp, err := p.client.Messages.New(ctx, anthropicAPI.MessageNewParams{
		Model:     req.Model,
		MaxTokens: defaultMaxTokens,
		Messages:  messages,
	})
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
func (p *Provider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, ErrNotSupported
}

// Models returns the list of available Anthropic models.
func (p *Provider) Models() ([]provider.Model, error) {
	return []provider.Model{
		{ID: "claude-sonnet-4-20250514", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "claude-3-5-haiku-latest", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "claude-opus-4-20250514", Provider: providerName, ContextLength: defaultContextLength},
	}, nil
}

func buildMessages(msgs []provider.Message) []anthropicAPI.MessageParam {
	messages := make([]anthropicAPI.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			messages = append(messages, anthropicAPI.NewUserMessage(anthropicAPI.NewTextBlock(m.Content)))
		case "assistant":
			messages = append(messages, anthropicAPI.NewAssistantMessage(anthropicAPI.NewTextBlock(m.Content)))
		}
	}
	return messages
}

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

func extractTextContent(blocks []anthropicAPI.ContentBlockUnion) string {
	for i := range blocks {
		if blocks[i].Type == "text" {
			return blocks[i].Text
		}
	}
	return ""
}
