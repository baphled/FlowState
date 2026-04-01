// Package zai provides a Z.AI (Zhipu AI) provider implementation.
package zai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/baphled/flowstate/internal/auth"
	"github.com/baphled/flowstate/internal/provider"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

const (
	providerName         = "zai"
	defaultBaseURL       = "https://api.z.ai"
	defaultContextLength = 128000
	defaultEmbedModel    = "embedding-3"
)

var errAPIKeyRequired = errors.New("Z.AI API key is required")

// Provider implements the provider.Provider interface for Z.AI.
type Provider struct {
	client openaiAPI.Client
}

// New creates a new Z.AI provider with the given API key.
func New(apiKey string) (*Provider, error) {
	return NewWithOptions(apiKey, option.WithBaseURL(defaultBaseURL))
}

// NewWithOptions creates a new Z.AI provider with custom request options.
func NewWithOptions(apiKey string, opts ...option.RequestOption) (*Provider, error) {
	if apiKey == "" {
		return nil, errAPIKeyRequired
	}

	allOpts := append([]option.RequestOption{option.WithAPIKey(apiKey), option.WithBaseURL(defaultBaseURL)}, opts...)
	client := openaiAPI.NewClient(allOpts...)
	return &Provider{client: client}, nil
}

// NewFromOpenCodeOrConfig creates a new Z.AI provider from OpenCode auth or a fallback key.
func NewFromOpenCodeOrConfig(opencodePath string, fallbackKey string) (*Provider, error) {
	if opencodePath != "" {
		authData, err := auth.LoadOpenCodeAuthFrom(opencodePath)
		if err != nil {
			if !errors.Is(err, auth.ErrAuthFileNotFound) && !errors.Is(err, auth.ErrNoCredentials) {
				return nil, err
			}
		} else if token, ok := zaiAccessToken(authData); ok {
			return New(token)
		}

		if token, err := zaiAccessTokenFromFile(opencodePath); err != nil {
			return nil, err
		} else if token != "" {
			return New(token)
		}
	}

	if fallbackKey == "" {
		return nil, errAPIKeyRequired
	}

	return New(fallbackKey)
}

// Name returns the provider name.
func (p *Provider) Name() string {
	return providerName
}

// Stream sends a streaming chat request to the Z.AI API.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 16)

	messages := make([]openaiAPI.ChatCompletionMessageParamUnion, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			messages = append(messages, openaiAPI.UserMessage(m.Content))
		case "assistant":
			messages = append(messages, openaiAPI.AssistantMessage(m.Content))
		case "system":
			messages = append(messages, openaiAPI.SystemMessage(m.Content))
		}
	}

	go func() {
		defer close(ch)

		stream := p.client.Chat.Completions.NewStreaming(ctx, openaiAPI.ChatCompletionNewParams{
			Model:    req.Model,
			Messages: messages,
		})

		for stream.Next() {
			chunk := stream.Current()
			if len(chunk.Choices) == 0 {
				continue
			}
			delta := chunk.Choices[0].Delta
			ch <- provider.StreamChunk{Content: delta.Content, Done: chunk.Choices[0].FinishReason != ""}
		}

		if err := stream.Err(); err != nil {
			ch <- provider.StreamChunk{Error: err, Done: true}
		}
	}()

	return ch, nil
}

// Chat sends a non-streaming chat request to the Z.AI API.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	messages := make([]openaiAPI.ChatCompletionMessageParamUnion, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			messages = append(messages, openaiAPI.UserMessage(m.Content))
		case "assistant":
			messages = append(messages, openaiAPI.AssistantMessage(m.Content))
		case "system":
			messages = append(messages, openaiAPI.SystemMessage(m.Content))
		}
	}

	resp, err := p.client.Chat.Completions.New(ctx, openaiAPI.ChatCompletionNewParams{Model: req.Model, Messages: messages})
	if err != nil {
		return provider.ChatResponse{}, fmt.Errorf("zai chat failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return provider.ChatResponse{}, errors.New("no choices in response")
	}

	return provider.ChatResponse{
		Message: provider.Message{
			Role:    string(resp.Choices[0].Message.Role),
			Content: resp.Choices[0].Message.Content,
		},
		Usage: provider.Usage{
			PromptTokens:     int(resp.Usage.PromptTokens),
			CompletionTokens: int(resp.Usage.CompletionTokens),
			TotalTokens:      int(resp.Usage.TotalTokens),
		},
	}, nil
}

// Embed generates embeddings for the given input text via the Z.AI API.
func (p *Provider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	model := req.Model
	if model == "" {
		model = defaultEmbedModel
	}

	resp, err := p.client.Embeddings.New(ctx, openaiAPI.EmbeddingNewParams{
		Model: model,
		Input: openaiAPI.EmbeddingNewParamsInputUnion{OfString: openaiAPI.String(req.Input)},
	})
	if err != nil {
		return nil, fmt.Errorf("zai embed failed: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, errors.New("no embeddings returned")
	}

	return resp.Data[0].Embedding, nil
}

// Models returns the list of available Z.AI models.
func (p *Provider) Models() ([]provider.Model, error) {
	return []provider.Model{
		{ID: "glm-5", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "glm-4.7", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "glm-4.7-flash", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "glm-4.5-air", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "glm-4.5-flash", Provider: providerName, ContextLength: defaultContextLength},
	}, nil
}

func zaiAccessToken(authData *auth.OpenCodeAuth) (string, bool) {
	if authData == nil {
		return "", false
	}

	value := reflect.ValueOf(authData).Elem()
	field := value.FieldByName("ZAI")
	if !field.IsValid() || field.IsNil() {
		return "", false
	}

	providerAuth, ok := field.Interface().(*auth.ProviderAuth)
	if !ok || providerAuth == nil || providerAuth.Access == "" {
		return "", false
	}

	return providerAuth.Access, true
}

func zaiAccessTokenFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("reading opencode auth: %w", err)
	}

	var raw struct {
		ZAI *auth.ProviderAuth `json:"zai,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("parsing opencode auth: %w", err)
	}
	if raw.ZAI == nil || raw.ZAI.Access == "" {
		return "", nil
	}

	return raw.ZAI.Access, nil
}
