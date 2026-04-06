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
	"github.com/baphled/flowstate/internal/provider/openaicompat"
	"github.com/baphled/flowstate/internal/provider/shared"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

const (
	providerName         = "zai"
	defaultBaseURL       = "https://api.z.ai/api/paas/v4"
	defaultContextLength = 128000
	defaultEmbedModel    = "embedding-3"
)

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

// NewFromOpenCodeOrConfig creates a new Z.AI provider from OpenCode auth or a fallback key.
//
// Expected:
//   - opencodePath is a path to OpenCode auth data or empty.
//   - fallbackKey is a valid Z.AI API key when OpenCode auth is unavailable.
//
// Returns:
//   - A configured Z.AI provider.
//   - An error if credential resolution fails or no API key is available.
//
// Side effects:
//   - Reads OpenCode auth data from disk when opencodePath is provided.
//
//nolint:nestif // credential resolution checks multiple sources
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
	rawCh := openaicompat.RunStream(ctx, p.client, params)
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

// zaiAccessToken extracts the Z.AI access token from OpenCode auth data.
//
// Expected:
//   - authData is a pointer to OpenCodeAuth (may be nil).
//
// Returns:
//   - The access token string and true if found.
//   - Empty string and false if not found.
//
// Side effects:
//   - None.
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

// zaiAccessTokenFromFile reads the Z.AI access token directly from an auth.json file.
//
// Expected:
//   - path is a file path to OpenCode's auth.json.
//
// Returns:
//   - The access token string if found.
//   - An error if the file cannot be read or parsed.
//
// Side effects:
//   - Reads from the file at path.
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
