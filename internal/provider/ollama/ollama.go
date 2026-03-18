// Package ollama provides an Ollama LLM provider implementation.
package ollama

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/baphled/flowstate/internal/provider"
	ollamaAPI "github.com/ollama/ollama/api"
)

// Provider implements the provider.Provider interface for Ollama.
type Provider struct {
	client *ollamaAPI.Client
	host   string
}

// New creates a new Ollama provider with the given host.
func New(host string) (*Provider, error) {
	client, err := ollamaAPI.ClientFromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to create ollama client: %w", err)
	}
	return &Provider{
		client: client,
		host:   host,
	}, nil
}

// NewWithClient creates a new Ollama provider with a custom HTTP client.
func NewWithClient(baseURL string, httpClient *http.Client) (*Provider, error) {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ollama base URL: %w", err)
	}
	client := ollamaAPI.NewClient(parsedURL, httpClient)
	return &Provider{
		client: client,
		host:   baseURL,
	}, nil
}

// Name returns the provider name.
func (p *Provider) Name() string {
	return "ollama"
}

// Stream sends a chat request and returns a channel of streaming response chunks.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 16)

	messages := make([]ollamaAPI.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, ollamaAPI.Message{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	ollamaTools := buildOllamaTools(req.Tools)

	go func() {
		defer close(ch)

		chatReq := &ollamaAPI.ChatRequest{
			Model:    req.Model,
			Messages: messages,
			Stream:   boolPtr(true),
			Tools:    ollamaTools,
		}

		err := p.client.Chat(ctx, chatReq, func(resp ollamaAPI.ChatResponse) error {
			if len(resp.Message.ToolCalls) > 0 {
				for _, tc := range resp.Message.ToolCalls {
					chunk := provider.StreamChunk{
						EventType: "tool_call",
						ToolCall: &provider.ToolCall{
							ID:        tc.Function.Name,
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments.ToMap(),
						},
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case ch <- chunk:
					}
				}
				ch <- provider.StreamChunk{Done: true}
				return nil
			}
			chunk := provider.StreamChunk{
				Content: resp.Message.Content,
				Done:    resp.Done,
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case ch <- chunk:
			}
			return nil
		})
		if err != nil {
			ch <- provider.StreamChunk{Error: err, Done: true}
		}
	}()

	return ch, nil
}

// Chat sends a chat request and returns the complete response.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	messages := make([]ollamaAPI.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, ollamaAPI.Message{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	ollamaTools := buildOllamaTools(req.Tools)

	chatReq := &ollamaAPI.ChatRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   boolPtr(false),
		Tools:    ollamaTools,
	}

	var finalResp ollamaAPI.ChatResponse
	err := p.client.Chat(ctx, chatReq, func(resp ollamaAPI.ChatResponse) error {
		finalResp = resp
		return nil
	})
	if err != nil {
		return provider.ChatResponse{}, fmt.Errorf("ollama chat failed: %w", err)
	}

	response := provider.ChatResponse{
		Message: provider.Message{
			Role:    finalResp.Message.Role,
			Content: finalResp.Message.Content,
		},
		Usage: provider.Usage{
			PromptTokens:     finalResp.PromptEvalCount,
			CompletionTokens: finalResp.EvalCount,
			TotalTokens:      finalResp.PromptEvalCount + finalResp.EvalCount,
		},
	}

	if len(finalResp.Message.ToolCalls) > 0 {
		response.Message.ToolCalls = parseToolCalls(finalResp.Message.ToolCalls)
	}

	return response, nil
}

// Embed generates embeddings for the given input text.
func (p *Provider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	embedReq := &ollamaAPI.EmbedRequest{
		Model: req.Model,
		Input: req.Input,
	}

	resp, err := p.client.Embed(ctx, embedReq)
	if err != nil {
		return nil, fmt.Errorf("ollama embed failed: %w", err)
	}

	if len(resp.Embeddings) == 0 {
		return nil, errors.New("no embeddings returned")
	}

	result := make([]float64, len(resp.Embeddings[0]))
	for i, v := range resp.Embeddings[0] {
		result[i] = float64(v)
	}

	return result, nil
}

// Models returns the list of available models.
func (p *Provider) Models() ([]provider.Model, error) {
	resp, err := p.client.List(context.Background())
	if err != nil {
		return nil, fmt.Errorf("ollama list models failed: %w", err)
	}

	models := make([]provider.Model, 0, len(resp.Models))
	for i := range resp.Models {
		contextLen := 4096
		models = append(models, provider.Model{
			ID:            resp.Models[i].Name,
			Provider:      "ollama",
			ContextLength: contextLen,
		})
	}

	return models, nil
}

func boolPtr(b bool) *bool {
	return &b
}

func buildOllamaTools(tools []provider.Tool) ollamaAPI.Tools {
	result := make(ollamaAPI.Tools, len(tools))
	for i, t := range tools {
		props := ollamaAPI.NewToolPropertiesMap()
		for key, val := range t.Schema.Properties {
			if propMap, ok := val.(map[string]interface{}); ok {
				prop := ollamaAPI.ToolProperty{}
				if propType, ok := propMap["type"].(string); ok {
					prop.Type = ollamaAPI.PropertyType{propType}
				}
				if desc, ok := propMap["description"].(string); ok {
					prop.Description = desc
				}
				props.Set(key, prop)
			}
		}
		result[i] = ollamaAPI.Tool{
			Type: "function",
			Function: ollamaAPI.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters: ollamaAPI.ToolFunctionParameters{
					Type:       t.Schema.Type,
					Properties: props,
					Required:   t.Schema.Required,
				},
			},
		}
	}
	return result
}

func parseToolCalls(toolCalls []ollamaAPI.ToolCall) []provider.ToolCall {
	result := make([]provider.ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		result[i] = provider.ToolCall{
			ID:        tc.Function.Name,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments.ToMap(),
		}
	}
	return result
}
