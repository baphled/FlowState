// Package ollamacloud provides an Ollama Cloud provider implementation.
//
// Ollama Cloud exposes an OpenAI-compatible API at https://ollama.com/api.
// This provider uses the openaicompat shared layer so it benefits from the
// same streaming, tool-call, and error-classification infrastructure as the
// other OpenAI-compatible providers (OpenAI, OpenZen, Z.AI).
//
// Configuration is read from config.yaml under providers.ollamacloud or the
// OLLAMA_CLOUD_API_KEY environment variable. An optional base_url override
// is supported for testing or custom endpoints.
package ollamacloud

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

const (
	providerName         = "ollamacloud"
	defaultBaseURL       = "https://ollama.com/api"
	defaultContextLength = 131072
	defaultEmbedModel    = "text-embedding-3-small"
)

var errAPIKeyRequired = errors.New("Ollama Cloud API key is required")

// Provider implements the provider.Provider interface for Ollama Cloud.
type Provider struct {
	client openaiAPI.Client
}

// New creates a new Ollama Cloud provider with the given API key.
//
// Expected:
//   - apiKey is a valid Ollama Cloud API key.
//
// Returns:
//   - A configured Provider on success.
//   - An error if the API key is empty.
//
// Side effects:
//   - None.
func New(apiKey string) (*Provider, error) {
	return NewWithOptions(apiKey, option.WithBaseURL(defaultBaseURL))
}

// NewWithOptions creates a new Ollama Cloud provider with custom request options.
//
// Expected:
//   - apiKey is a valid Ollama Cloud API key.
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

	allOpts := append([]option.RequestOption{option.WithAPIKey(apiKey), option.WithBaseURL(defaultBaseURL)}, opts...)
	client := openaiAPI.NewClient(allOpts...)
	return &Provider{client: client}, nil
}

// NewFromConfig creates a new Ollama Cloud provider from configuration.
//
// Expected:
//   - apiKey is the API key from config.yaml or the OLLAMA_CLOUD_API_KEY env var.
//   - baseURL is an optional override for the API endpoint; empty uses the default.
//
// Returns:
//   - A configured Provider on success.
//   - errAPIKeyRequired when apiKey is empty.
//
// Side effects:
//   - None.
func NewFromConfig(apiKey, baseURL string) (*Provider, error) {
	if apiKey == "" {
		return nil, errAPIKeyRequired
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return NewWithOptions(apiKey, option.WithBaseURL(baseURL))
}

// Name returns the provider name.
//
// Returns:
//   - The string "ollamacloud".
//
// Side effects:
//   - None.
func (p *Provider) Name() string {
	return providerName
}

// Stream sends a streaming chat request to the Ollama Cloud API.
//
// Expected:
//   - ctx is a valid context for the API call.
//   - req contains the messages, model, and optional tools to use.
//
// Returns:
//   - A channel of StreamChunk values containing the streamed response.
//   - An error if the request cannot be initiated.
//
// Side effects:
//   - Spawns a goroutine to read from the Ollama Cloud streaming API.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	params := openaicompat.BuildParams(req)
	return openaicompat.RunStream(ctx, p.client, params, p.Name()), nil
}

// Chat sends a non-streaming chat request to the Ollama Cloud API.
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
//   - Makes an HTTP request to the Ollama Cloud API.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	params := openaicompat.BuildParams(req)
	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return provider.ChatResponse{}, openaicompat.WrapChatError(p.Name(), err)
	}
	return openaicompat.ParseChatResponse(resp)
}

// Embed generates embeddings for the given input text via the Ollama Cloud API.
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
//   - Makes an HTTP request to the Ollama Cloud embeddings API.
func (p *Provider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	model := req.Model
	if model == "" {
		model = defaultEmbedModel
	}

	resp, err := p.client.Embeddings.New(ctx, openaiAPI.EmbeddingNewParams{
		Model: model,
		Input: openaiAPI.EmbeddingNewParamsInputUnion{
			OfString: openaiAPI.String(req.Input),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("ollamacloud embed failed: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, errors.New("no embeddings returned")
	}

	return resp.Data[0].Embedding, nil
}

// Models returns the list of available Ollama Cloud models.
//
// Returns:
//   - A slice of supported Ollama Cloud model definitions.
//   - A hardcoded fallback list if the API call fails.
//
// Side effects:
//   - Makes an HTTP request to the Ollama Cloud models API.
func (p *Provider) Models() ([]provider.Model, error) {
	models, err := p.fetchModels()
	if err == nil {
		return models, nil
	}
	return fallbackModels(), nil
}

// fetchModels retrieves the available model list from the Ollama Cloud API.
//
// Returns:
//   - ([]provider.Model, nil) on success.
//   - (nil, error) if the API call fails.
//
// Side effects:
//   - Makes an HTTP request to the provider's Models API.
func (p *Provider) fetchModels() ([]provider.Model, error) {
	ctx := context.Background()
	modelsPage, err := p.client.Models.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing %s models: %w", providerName, err)
	}

	models := make([]provider.Model, 0, len(modelsPage.Data))
	for i := range modelsPage.Data {
		models = append(models, provider.Model{
			ID:            modelsPage.Data[i].ID,
			Provider:      providerName,
			ContextLength: defaultContextLength,
		})
	}

	return models, nil
}

// fallbackModels returns a hardcoded list of known Ollama Cloud models.
//
// Returns:
//   - A slice of commonly available Ollama Cloud models.
//
// Side effects:
//   - None.
func fallbackModels() []provider.Model {
	return []provider.Model{
		{ID: "llama3.3:70b", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "llama3.2:latest", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "qwen2.5:72b", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "mistral:latest", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "phi3:latest", Provider: providerName, ContextLength: defaultContextLength},
	}
}
