// Package openai provides an OpenAI provider implementation.
package openai

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

var errAPIKeyRequired = errors.New("OpenAI API key is required")

// Provider implements the provider.Provider interface for OpenAI.
type Provider struct {
	client openaiAPI.Client
}

// New creates a new OpenAI provider with the given API key.
//
// Expected:
//   - apiKey is a non-empty OpenAI API key string.
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
	client := openaiAPI.NewClient(option.WithAPIKey(apiKey))
	return &Provider{
		client: client,
	}, nil
}

// NewWithOptions creates a new OpenAI provider with custom request options.
//
// Expected:
//   - apiKey is a non-empty OpenAI API key string.
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
	client := openaiAPI.NewClient(allOpts...)
	return &Provider{
		client: client,
	}, nil
}

// Name returns the provider name.
//
// Returns:
//   - The string "openai".
//
// Side effects:
//   - None.
func (p *Provider) Name() string {
	return "openai"
}

// Stream sends a streaming chat request to the OpenAI API.
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
//   - Spawns a goroutine to read from the OpenAI streaming API.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	params := openaicompat.BuildParams(req)
	return openaicompat.RunStream(ctx, p.client, params), nil
}

// Chat sends a non-streaming chat request to the OpenAI API.
//
// Expected:
//   - ctx is a valid context for the API call.
//   - req contains the messages and model to use.
//
// Returns:
//   - A ChatResponse with the assistant's reply and token usage.
//   - An error if the API call fails or no choices are returned.
//
// Side effects:
//   - Makes an HTTP request to the OpenAI API.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	params := openaicompat.BuildParams(req)
	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return provider.ChatResponse{}, fmt.Errorf("openai chat failed: %w", err)
	}
	return openaicompat.ParseChatResponse(resp)
}

// Embed generates embeddings for the given input text via the OpenAI API.
//
// Expected:
//   - ctx is a valid context for the API call.
//   - req contains the input text and optional model override.
//
// Returns:
//   - A float64 slice containing the embedding vector.
//   - An error if the API call fails or no embeddings are returned.
//
// Side effects:
//   - Makes an HTTP request to the OpenAI embeddings API.
func (p *Provider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	model := req.Model
	if model == "" {
		model = "text-embedding-3-small"
	}

	resp, err := p.client.Embeddings.New(ctx, openaiAPI.EmbeddingNewParams{
		Model: model,
		Input: openaiAPI.EmbeddingNewParamsInputUnion{
			OfString: openaiAPI.String(req.Input),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai embed failed: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, errors.New("no embeddings returned")
	}

	return resp.Data[0].Embedding, nil
}

// Models returns the list of available OpenAI models.
//
// Returns:
//   - A slice of supported OpenAI model definitions.
//
// Side effects:
//   - None.
func (p *Provider) Models() ([]provider.Model, error) {
	return []provider.Model{
		{ID: "gpt-4o", Provider: "openai", ContextLength: 128000},
		{ID: "gpt-4o-mini", Provider: "openai", ContextLength: 128000},
		{ID: "gpt-4-turbo", Provider: "openai", ContextLength: 128000},
		{ID: "gpt-3.5-turbo", Provider: "openai", ContextLength: 16385},
	}, nil
}
