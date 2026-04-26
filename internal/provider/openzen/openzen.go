package openzen

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
	providerName         = "openzen"
	defaultBaseURL       = "https://api.openzen.ai"
	defaultContextLength = 200000
	defaultEmbedModel    = "text-embedding-3-small"
)

var errAPIKeyRequired = errors.New("OpenZen API key is required")

// Provider implements the provider.Provider interface for OpenZen.
type Provider struct {
	client openaiAPI.Client
}

// New creates a new OpenZen provider with the given API key.
//
// Expected:
//   - apiKey is a valid OpenZen API key.
//
// Returns:
//   - A configured OpenZen provider.
//   - An error if the API key is empty.
//
// Side effects:
//   - None.
func New(apiKey string) (*Provider, error) {
	return NewWithOptions(apiKey, option.WithBaseURL(defaultBaseURL))
}

// NewWithOptions creates a new OpenZen provider with custom request options.
//
// Expected:
//   - apiKey is a valid OpenZen API key.
//   - opts contains any additional request configuration.
//
// Returns:
//   - A configured OpenZen provider.
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

// NewFromConfig creates a new OpenZen provider from a configured API key.
//
// Expected:
//   - apiKey is the OpenZen API key from config.yaml or environment.
//
// Returns:
//   - A configured OpenZen provider.
//   - errAPIKeyRequired when apiKey is empty.
//
// Side effects:
//   - None.
func NewFromConfig(apiKey string) (*Provider, error) {
	if apiKey == "" {
		return nil, errAPIKeyRequired
	}
	return New(apiKey)
}

// Name returns the provider name.
//
// Returns:
//   - The provider identifier string.
//
// Side effects:
//   - None.
func (p *Provider) Name() string {
	return providerName
}

// Stream sends a streaming chat request to the OpenZen API.
//
// Expected:
//   - ctx is a valid context for the request.
//   - req contains the chat messages and model to use.
//
// Returns:
//   - A channel that yields streaming response chunks.
//   - An error if the request cannot be created.
//
// Side effects:
//   - Starts a goroutine and performs network I/O against the OpenZen API.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	params := openaicompat.BuildParams(req)
	return openaicompat.RunStream(ctx, p.client, params, p.Name()), nil
}

// Chat sends a non-streaming chat request to the OpenZen API.
//
// Expected:
//   - ctx is a valid context for the request.
//   - req contains the chat messages and model to use.
//
// Returns:
//   - A chat response with message content and token usage.
//   - An error if the request fails or returns no choices.
//
// Side effects:
//   - Performs network I/O against the OpenZen API.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	params := openaicompat.BuildParams(req)
	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return provider.ChatResponse{}, openaicompat.WrapChatError(p.Name(), err)
	}
	return openaicompat.ParseChatResponse(resp)
}

// Embed generates embeddings for the given input text via the OpenZen API.
//
// Expected:
//   - ctx is a valid context for the request.
//   - req contains the input text and optional model.
//
// Returns:
//   - The generated embedding vector.
//   - An error if embedding generation fails or returns no data.
//
// Side effects:
//   - Performs network I/O against the OpenZen API.
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
		return nil, fmt.Errorf("openzen embed failed: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, errors.New("no embeddings returned")
	}

	return resp.Data[0].Embedding, nil
}

// Models returns the list of available OpenZen models.
//
// Returns:
//   - A slice of model definitions from the provider's Models API.
//   - A hardcoded fallback list if the API call fails.
//
// Side effects:
//   - Makes an HTTP request to the provider's Models API.
func (p *Provider) Models() ([]provider.Model, error) {
	models, err := p.fetchModels()
	if err == nil {
		return models, nil
	}
	return fallbackModels(), nil
}

// fetchModels retrieves the available model list from the provider's Models API.
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

// fallbackModels returns a hardcoded list of known models when the API is unavailable.
//
// Returns:
//   - A slice of commonly available models.
//
// Side effects:
//   - None.
func fallbackModels() []provider.Model {
	return []provider.Model{
		{ID: "claude-sonnet-4-5", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "claude-3-5-sonnet", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "gpt-4o", Provider: providerName, ContextLength: defaultContextLength},
	}
}

