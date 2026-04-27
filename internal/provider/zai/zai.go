// Package zai provides a Z.AI (Zhipu AI) provider implementation.
package zai

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
	"github.com/baphled/flowstate/internal/provider/shared"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

const (
	providerName = "zai"
	// General pay-per-token Z.AI endpoint; used for `ZAI_API_KEY` env var
	// and config.Providers.ZAI.APIKey when no plan is set.
	defaultBaseURL = "https://api.z.ai/api/paas/v4"
	// Z.AI `coding-plan` subscription endpoint. The coding-plan product
	// routes to a different base path than the general pay-per-token plan;
	// using the general endpoint with a coding-plan key returns HTTP 429 /
	// code 1113 (billing) on every call. Set `providers.zai.host` to this
	// URL (or set the plan to "coding") to route correctly.
	codingPlanBaseURL = "https://api.z.ai/api/coding/paas/v4"

	// PlanGeneral is the canonical pay-per-token Z.AI plan tag; maps to
	// defaultBaseURL.
	PlanGeneral = "general"
	// PlanCoding is the Z.AI coding-plan subscription tag; maps to
	// codingPlanBaseURL.
	PlanCoding = "coding"

	defaultContextLength = 128000
	defaultEmbedModel    = "embedding-3"
)

// BaseURLForPlan returns the Z.AI base URL for the named plan.
//
// Expected:
//   - plan is "general", "coding", or empty. Any other value (including
//     empty) falls back to the general pay-per-token endpoint.
//
// Returns:
//   - The coding-plan URL when plan is "coding".
//   - The general pay-per-token URL otherwise.
//
// Side effects:
//   - None.
func BaseURLForPlan(plan string) string {
	if plan == PlanCoding {
		return codingPlanBaseURL
	}
	return defaultBaseURL
}

var errAPIKeyRequired = errors.New("Z.AI API key is required")

// Provider implements the provider.Provider interface for Z.AI.
type Provider struct {
	client openaiAPI.Client
}

// New creates a new Z.AI provider with the given API key.
//
// Expected:
//   - apiKey is a valid Z.AI API key.
//
// Returns:
//   - A configured Z.AI provider.
//   - An error if the API key is empty.
//
// Side effects:
//   - None.
func New(apiKey string) (*Provider, error) {
	return NewWithOptions(apiKey, option.WithBaseURL(defaultBaseURL))
}

// NewWithOptions creates a new Z.AI provider with custom request options.
//
// Expected:
//   - apiKey is a valid Z.AI API key.
//   - opts contains any additional request configuration.
//
// Returns:
//   - A configured Z.AI provider.
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

// NewFromConfig creates a new Z.AI provider from a configured API key and
// optional plan tag.
//
// Expected:
//   - apiKey is the Z.AI API key from config.yaml or environment.
//   - plan is "coding" for the coding-plan subscription endpoint, or
//     anything else (typically "general" or empty) for the pay-per-token
//     endpoint.
//
// Returns:
//   - A configured Z.AI provider.
//   - errAPIKeyRequired when apiKey is empty.
//
// Side effects:
//   - None.
func NewFromConfig(apiKey, plan string) (*Provider, error) {
	if apiKey == "" {
		return nil, errAPIKeyRequired
	}
	return NewWithOptions(apiKey, option.WithBaseURL(BaseURLForPlan(plan)))
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

// Stream sends a streaming chat request to the Z.AI API.
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
//   - Starts a goroutine and performs network I/O against the Z.AI API.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	params := openaicompat.BuildParams(req)
	rawCh := openaicompat.RunStream(ctx, p.client, params, p.Name())
	return classifyStreamErrors(ctx, rawCh), nil
}

// Chat sends a non-streaming chat request to the Z.AI API.
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
//   - Performs network I/O against the Z.AI API.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	params := openaicompat.BuildParams(req)
	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		if provErr := openaicompat.ParseProviderError(providerName, err); provErr != nil {
			return provider.ChatResponse{}, classifyZAIError(provErr)
		}
		return provider.ChatResponse{}, openaicompat.WrapChatError(providerName, err)
	}
	return openaicompat.ParseChatResponse(resp)
}

// Embed generates embeddings for the given input text via the Z.AI API.
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
//   - Performs network I/O against the Z.AI API.
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
		{ID: "glm-5", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "glm-4.7", Provider: providerName, ContextLength: defaultContextLength},
		{ID: "glm-4.7-flash", Provider: providerName, ContextLength: defaultContextLength},
	}
}

// classifyZAIError refines the classification of a Z.AI error by inspecting the provider-specific
// error code. Z.AI reuses HTTP 429 for billing, quota, and overload errors that are NOT rate limits.
//
// Expected:
//   - baseErr may be nil.
//
// Returns:
//   - A refined *provider.Error when the code matches a known Z.AI condition.
//   - Nil when baseErr is nil.
//   - baseErr when no specialisation applies.
//
// Side effects:
//   - None.
func classifyZAIError(baseErr *provider.Error) *provider.Error {
	if baseErr == nil {
		return nil
	}
	switch baseErr.ErrorCode {
	case "1001":
		return &provider.Error{
			HTTPStatus: baseErr.HTTPStatus, ErrorCode: "1001", ErrorType: provider.ErrorTypeRateLimit,
			Provider: providerName, Message: baseErr.Message, IsRetriable: true, RawError: baseErr.RawError,
		}
	case "1002":
		return &provider.Error{
			HTTPStatus: baseErr.HTTPStatus, ErrorCode: "1002", ErrorType: provider.ErrorTypeOverload,
			Provider: providerName, Message: baseErr.Message, IsRetriable: true, RawError: baseErr.RawError,
		}
	case "1112":
		return &provider.Error{
			HTTPStatus: baseErr.HTTPStatus, ErrorCode: "1112", ErrorType: provider.ErrorTypeQuota,
			Provider: providerName, Message: baseErr.Message, IsRetriable: false, RawError: baseErr.RawError,
		}
	case "1113":
		return &provider.Error{
			HTTPStatus: baseErr.HTTPStatus, ErrorCode: "1113", ErrorType: provider.ErrorTypeBilling,
			Provider: providerName, Message: baseErr.Message, IsRetriable: false, RawError: baseErr.RawError,
		}
	default:
		return baseErr
	}
}

// classifyStreamErrors wraps a raw stream channel and applies Z.AI-specific error classification.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - rawCh yields provider.StreamChunk values and may be closed.
//
// Returns:
//   - A channel that forwards the incoming stream chunks.
//
// Side effects:
//   - Starts a goroutine and closes the returned channel when rawCh is exhausted.
func classifyStreamErrors(ctx context.Context, rawCh <-chan provider.StreamChunk) <-chan provider.StreamChunk {
	ch := make(chan provider.StreamChunk, 16)
	go func() {
		defer close(ch)
		for chunk := range rawCh {
			if chunk.Error != nil {
				if provErr := openaicompat.ParseProviderError(providerName, chunk.Error); provErr != nil {
					chunk.Error = classifyZAIError(provErr)
				}
			}
			shared.SendChunk(ctx, ch, chunk)
		}
	}()
	return ch
}
