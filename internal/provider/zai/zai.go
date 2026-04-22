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
	providerName = "zai"
	// General pay-per-token Z.AI endpoint; used for `ZAI_API_KEY` env var,
	// config.Providers.ZAI.APIKey, and OpenCode's canonical `zai` auth entry.
	defaultBaseURL = "https://api.z.ai/api/paas/v4"
	// Z.AI `zai-coding-plan` subscription endpoint. The coding-plan product
	// routes to a different base path than the general pay-per-token plan;
	// using the general endpoint with a coding-plan key returns HTTP 429 /
	// code 1113 (billing) on every call.
	codingPlanBaseURL = "https://api.z.ai/api/coding/paas/v4"

	// Auth-source tag for the canonical OpenCode `zai` entry (and the
	// env/config fallback). Maps to defaultBaseURL.
	authSourceZAI = "zai"
	// Auth-source tag for the OpenCode `zai-coding-plan` entry. Maps to
	// codingPlanBaseURL.
	authSourceZAICodingPlan = "zai-coding-plan"

	defaultContextLength = 128000
	defaultEmbedModel    = "embedding-3"
)

// ResolveOpenCodeAuthForTest exposes the internal auth-source resolution for
// package-external tests. Production code must not call this directly; use
// NewFromOpenCodeOrConfig instead.
//
// Expected:
//   - opencodePath is a path to OpenCode auth.json or empty.
//   - fallbackKey is the key used when no OpenCode credential is available.
//
// Returns:
//   - The same (token, source, error) triple that drives
//     NewFromOpenCodeOrConfig.
//
// Side effects:
//   - Reads OpenCode auth data from disk when opencodePath is provided.
func ResolveOpenCodeAuthForTest(opencodePath, fallbackKey string) (string, string, error) {
	return resolveOpenCodeAuth(opencodePath, fallbackKey)
}

// BaseURLForAuthSource returns the Z.AI base URL matching the auth source
// that produced the credential.
//
// Expected:
//   - source is one of "zai" (general pay-per-token / env var / config) or
//     "zai-coding-plan" (OpenCode subscription alias). Any other value
//     (including empty) falls back to the general endpoint.
//
// Returns:
//   - The coding-plan URL when source is "zai-coding-plan".
//   - The general pay-per-token URL otherwise.
//
// Side effects:
//   - None.
func BaseURLForAuthSource(source string) string {
	if source == authSourceZAICodingPlan {
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

// NewFromOpenCodeOrConfig creates a new Z.AI provider from OpenCode auth or a fallback key.
//
// The constructor selects the API base URL based on the auth source: the
// OpenCode `zai-coding-plan` entry routes to the coding-plan endpoint while
// the canonical `zai` entry and fallback env/config keys route to the
// general pay-per-token endpoint.
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
func NewFromOpenCodeOrConfig(opencodePath string, fallbackKey string) (*Provider, error) {
	token, source, err := resolveOpenCodeAuth(opencodePath, fallbackKey)
	if err != nil {
		return nil, err
	}
	return NewWithOptions(token, option.WithBaseURL(BaseURLForAuthSource(source)))
}

// resolveOpenCodeAuth resolves a Z.AI token from OpenCode auth then fallback
// key, returning the source that produced it so the caller can pick the
// correct base URL.
//
// Expected:
//   - opencodePath is a path to OpenCode auth.json or empty.
//   - fallbackKey is a Z.AI API key to use when no OpenCode credential is
//     available.
//
// Returns:
//   - (token, "zai-coding-plan", nil) when the credential came from the
//     OpenCode `zai-coding-plan` entry.
//   - (token, "zai", nil) when the credential came from the canonical
//     OpenCode `zai` entry or the fallback key.
//   - ("", "", errAPIKeyRequired) when no credential is available.
//   - ("", "", err) when OpenCode auth cannot be read/parsed.
//
// Side effects:
//   - Reads OpenCode auth data from disk when opencodePath is provided.
//
//nolint:nestif // credential resolution checks multiple sources
func resolveOpenCodeAuth(opencodePath, fallbackKey string) (string, string, error) {
	if opencodePath != "" {
		authData, err := auth.LoadOpenCodeAuthFrom(opencodePath)
		if err != nil {
			if !errors.Is(err, auth.ErrAuthFileNotFound) && !errors.Is(err, auth.ErrNoCredentials) {
				return "", "", err
			}
		} else if token, source, ok := zaiAccessToken(authData); ok {
			return token, source, nil
		}

		token, source, err := zaiAccessTokenFromFile(opencodePath)
		if err != nil {
			return "", "", err
		}
		if token != "" {
			return token, source, nil
		}
	}

	if fallbackKey == "" {
		return "", "", errAPIKeyRequired
	}

	return fallbackKey, authSourceZAI, nil
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

// zaiAccessToken extracts the Z.AI access token from OpenCode auth data and
// reports which auth source supplied it.
//
// LoadOpenCodeAuthFrom aliases `zai-coding-plan` into the canonical `ZAI`
// field (pointer copy) when no distinct `zai` entry exists. This function
// detects that aliasing so the caller can pick the correct base URL.
//
// Expected:
//   - authData is a pointer to OpenCodeAuth (may be nil).
//
// Returns:
//   - (token, "zai-coding-plan", true) when the token came from the
//     `zai-coding-plan` entry (either via aliasing or when only that entry
//     has a token).
//   - (token, "zai", true) when the token came from a distinct canonical
//     `zai` entry.
//   - ("", "", false) when no usable token is present.
//
// Side effects:
//   - None.
func zaiAccessToken(authData *auth.OpenCodeAuth) (string, string, bool) {
	if authData == nil {
		return "", "", false
	}

	value := reflect.ValueOf(authData).Elem()
	field := value.FieldByName("ZAI")
	if !field.IsValid() || field.IsNil() {
		return "", "", false
	}

	providerAuth, ok := field.Interface().(*auth.ProviderAuth)
	if !ok || providerAuth == nil || providerAuth.Access == "" {
		return "", "", false
	}

	// When LoadOpenCodeAuthFrom aliased ZAICodingPlan into ZAI (because no
	// distinct canonical `zai` entry was present), the ZAI and ZAICodingPlan
	// fields point at the same struct. Treat that as the coding-plan source.
	if authData.ZAICodingPlan != nil && authData.ZAICodingPlan == providerAuth {
		return providerAuth.Access, authSourceZAICodingPlan, true
	}

	return providerAuth.Access, authSourceZAI, true
}

// zaiAccessTokenFromFile reads the Z.AI access token directly from an
// auth.json file and reports which auth source supplied it.
//
// Expected:
//   - path is a file path to OpenCode's auth.json.
//
// Returns:
//   - (token, "zai", nil) when the canonical `zai` entry provided the token.
//   - (token, "zai-coding-plan", nil) when the `zai-coding-plan` entry
//     provided the token.
//   - ("", "", nil) when the file is missing or has no usable Z.AI
//     credentials.
//   - ("", "", err) when the file cannot be read or parsed.
//
// Side effects:
//   - Reads from the file at path.
func zaiAccessTokenFromFile(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("reading opencode auth: %w", err)
	}

	var raw struct {
		ZAI           *auth.ProviderAuth `json:"zai,omitempty"`
		ZAICodingPlan *auth.ProviderAuth `json:"zai-coding-plan,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", "", fmt.Errorf("parsing opencode auth: %w", err)
	}
	if tok := providerAuthToken(raw.ZAI); tok != "" {
		return tok, authSourceZAI, nil
	}
	if tok := providerAuthToken(raw.ZAICodingPlan); tok != "" {
		return tok, authSourceZAICodingPlan, nil
	}
	return "", "", nil
}

// providerAuthToken returns the canonical access token from a ProviderAuth,
// falling back to the alias `key` field when `access` is empty.
//
// Expected:
//   - pa may be nil.
//
// Returns:
//   - The access token if present, otherwise an empty string.
//
// Side effects:
//   - None.
func providerAuthToken(pa *auth.ProviderAuth) string {
	if pa == nil {
		return ""
	}
	if pa.Access != "" {
		return pa.Access
	}
	return pa.Key
}
