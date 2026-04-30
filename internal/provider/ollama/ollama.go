// Package ollama provides an Ollama LLM provider implementation.
package ollama

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/shared"
	ollamaAPI "github.com/ollama/ollama/api"
)

// Provider implements the provider.Provider interface for Ollama.
type Provider struct {
	client *ollamaAPI.Client
	host   string
}

// New creates a new Ollama provider with the given host.
//
// Expected:
//   - host is the Ollama server address.
//
// Returns:
//   - A configured Provider on success.
//   - An error if the client cannot be created.
//
// Side effects:
//   - Reads Ollama client configuration from environment variables.
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
//
// Expected:
//   - baseURL is a valid Ollama server URL.
//   - httpClient is a non-nil HTTP client to use for requests.
//
// Returns:
//   - A configured Provider on success.
//   - An error if the URL cannot be parsed.
//
// Side effects:
//   - None.
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

const (
	defaultMaxTokens = 4096
	providerName     = "ollama"
)

// knownModelContextLengths maps Ollama model family prefixes to their
// published context windows. Keys are matched against the model ID via a
// case-insensitive HasPrefix check so that variants such as
// "llama3.2:latest", "llama3.2:3b", and "llama3.2" all resolve to the
// same length. The map is deliberately small and conservative: values
// come from the public Ollama model pages and the Hugging Face model
// cards of the underlying weights. Adding an entry costs nothing at
// runtime; picking the wrong value for an unknown model costs a
// truncated prompt.
//
// Rationale for hard-coding rather than querying /api/show at runtime:
// the HTTP round-trip for every model every Models() call would amplify
// latency on the failover resolver's hot path, and the context length
// of a given model rarely changes — the hard-coded fallback stays
// correct across Ollama versions. If a future deployment needs dynamic
// resolution (e.g. fine-tuned local models with bespoke context
// windows), wire an optional /api/show lookup around this lookup table
// rather than replacing it.
var knownModelContextLengths = map[string]int{
	"llama3.3":             131072,
	"llama3.2":             131072,
	"llama3.1":             131072,
	"llama3":               8192,
	"qwen3":                32768,
	"qwen2.5":              32768,
	"qwen2":                32768,
	"granite4":             131072,
	"granite3":             131072,
	"mistral":              32768,
	"mixtral":              32768,
	"nous-hermes2-mixtral": 32768,
	"phi3":                 131072,
	"gemma4":               131072,
	"gemma3n":              32768,
	"gemma3":               131072,
	"gemma2":               8192,
	"codegemma":            8192,
	"codellama":            16384,
	"devstral":             131072,
	"deepseek":             16384,
}

// resolveOllamaContextLength returns the context length for an Ollama
// model ID by walking knownModelContextLengths for a matching family
// prefix. When no entry matches, it returns defaultMaxTokens so the
// caller still receives a usable value and the failover manager's own
// fallback is never reached on a known family.
//
// Namespaced model IDs (e.g. "username/model:tag") have their namespace
// prefix stripped before matching so that community fine-tunes resolve
// against their base family.
//
// Expected:
//   - modelID is a concrete Ollama model identifier such as
//     "llama3.2:latest", "qwen2.5:7b-instruct", or "user/llama3.1:8b".
//
// Returns:
//   - The longest-prefix matched context length in tokens, or
//     defaultMaxTokens (4096) when no family prefix matches.
//
// Side effects:
//   - None.
func resolveOllamaContextLength(modelID string) int {
	if modelID == "" {
		return defaultMaxTokens
	}
	// Strip optional namespace ("username/model:tag" → "model:tag").
	if idx := strings.Index(modelID, "/"); idx >= 0 {
		modelID = modelID[idx+1:]
	}
	lower := strings.ToLower(modelID)
	var bestPrefix string
	var bestLen int
	for family, length := range knownModelContextLengths {
		if strings.HasPrefix(lower, family) && len(family) > len(bestPrefix) {
			bestPrefix = family
			bestLen = length
		}
	}
	if bestPrefix == "" {
		return defaultMaxTokens
	}
	return bestLen
}

// Name returns the provider name.
//
// Returns:
//   - The string "ollama".
//
// Side effects:
//   - None.
func (p *Provider) Name() string {
	return providerName
}

// Stream sends a streaming chat request to the Ollama API.
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
//   - Spawns a goroutine to read from the Ollama streaming API.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 16)

	messages := convertMessages(req.Messages)
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
					if !shared.SendChunk(ctx, ch, chunk) {
						return ctx.Err()
					}
				}
				shared.SendChunk(ctx, ch, provider.StreamChunk{Done: true})
				return nil
			}
			chunk := provider.StreamChunk{
				Content: resp.Message.Content,
				Done:    resp.Done,
			}
			if !shared.SendChunk(ctx, ch, chunk) {
				return ctx.Err()
			}
			return nil
		})
		if err != nil {
			shared.SendChunk(ctx, ch, provider.StreamChunk{Error: classifiedOrRawError(err), Done: true})
		}
	}()

	return ch, nil
}

// Chat sends a non-streaming chat request to the Ollama API.
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
//   - Makes an HTTP request to the Ollama API.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	messages := convertMessages(req.Messages)
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
		if pErr := parseOllamaError(err); pErr != nil {
			return provider.ChatResponse{}, pErr
		}
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

// Embed generates embeddings for the given input text via the Ollama API.
//
// Expected:
//   - ctx is a valid context for the API call.
//   - req contains the input text and model to use.
//
// Returns:
//   - A float64 slice containing the embedding vector.
//   - An error if the API call fails or no embeddings are returned.
//
// Side effects:
//   - Makes an HTTP request to the Ollama embedding API.
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

// Models returns the list of available Ollama models.
//
// Returns:
//   - A slice of Model values listing locally available models.
//   - An error if the model list cannot be fetched.
//
// Side effects:
//   - Makes an HTTP request to the Ollama API.
func (p *Provider) Models() ([]provider.Model, error) {
	resp, err := p.client.List(context.Background())
	if err != nil {
		return nil, fmt.Errorf("ollama list models failed: %w", err)
	}

	models := make([]provider.Model, 0, len(resp.Models))
	for i := range resp.Models {
		models = append(models, provider.Model{
			ID:            resp.Models[i].Name,
			Provider:      providerName,
			ContextLength: resolveOllamaContextLength(resp.Models[i].Name),
		})
	}

	return models, nil
}

// classifiedOrRawError returns a *provider.Error if err can be classified, or the original error otherwise.
//
// Expected:
//   - err may be nil.
//
// Returns:
//   - Nil when err is nil.
//   - A *provider.Error when classification succeeds.
//   - The original error otherwise.
//
// Side effects:
//   - None.
func classifiedOrRawError(err error) error {
	if pErr := parseOllamaError(err); pErr != nil {
		return pErr
	}
	return err
}

// parseOllamaError converts Ollama SDK and transport errors into *provider.Error values.
//
// Expected:
//   - err may be nil.
//
// Returns:
//   - A populated *provider.Error when the error can be classified.
//   - Nil when err is nil or unclassified.
//
// Side effects:
//   - None.
func parseOllamaError(err error) *provider.Error {
	if err == nil {
		return nil
	}

	var statusErr ollamaAPI.StatusError
	if errors.As(err, &statusErr) {
		return classifyStatusError(statusErr)
	}

	var authErr ollamaAPI.AuthorizationError
	if errors.As(err, &authErr) {
		return &provider.Error{
			HTTPStatus:  authErr.StatusCode,
			ErrorType:   provider.ErrorTypeAuthFailure,
			Provider:    providerName,
			Message:     authErr.Error(),
			IsRetriable: false,
			RawError:    err,
		}
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return &provider.Error{
			ErrorType:   provider.ErrorTypeNetworkError,
			Provider:    providerName,
			Message:     urlErr.Error(),
			IsRetriable: true,
			RawError:    err,
		}
	}

	if strings.Contains(err.Error(), "connection refused") {
		return &provider.Error{
			ErrorType:   provider.ErrorTypeNetworkError,
			Provider:    providerName,
			Message:     err.Error(),
			IsRetriable: true,
			RawError:    err,
		}
	}

	return nil
}

// classifyStatusError maps an Ollama status error to a provider.Error classification.
//
// Expected:
//   - statusErr contains the Ollama HTTP status and message.
//
// Returns:
//   - A populated *provider.Error describing the failure.
//
// Side effects:
//   - None.
func classifyStatusError(statusErr ollamaAPI.StatusError) *provider.Error {
	pErr := &provider.Error{
		HTTPStatus: statusErr.StatusCode,
		Provider:   providerName,
		Message:    statusErr.ErrorMessage,
		RawError:   statusErr,
	}

	switch statusErr.StatusCode {
	case http.StatusNotFound:
		pErr.ErrorType = provider.ErrorTypeModelNotFound
		pErr.IsRetriable = false
	case http.StatusUnauthorized, http.StatusForbidden:
		pErr.ErrorType = provider.ErrorTypeAuthFailure
		pErr.IsRetriable = false
	case http.StatusServiceUnavailable:
		msg := strings.ToLower(statusErr.ErrorMessage)
		if strings.Contains(msg, "loading") || strings.Contains(msg, "busy") || strings.Contains(msg, "overloaded") {
			pErr.ErrorType = provider.ErrorTypeOverload
		} else {
			pErr.ErrorType = provider.ErrorTypeServerError
		}
		pErr.IsRetriable = true
	case http.StatusTooManyRequests:
		pErr.ErrorType = provider.ErrorTypeRateLimit
		pErr.IsRetriable = true
	default:
		pErr.ErrorType = provider.ErrorTypeServerError
		pErr.IsRetriable = provider.IsRetriableErrorType(provider.ErrorTypeServerError)
	}

	return pErr
}

// boolPtr returns a pointer to the given boolean value.
//
// Expected:
//   - b is any boolean value.
//
// Returns:
//   - A pointer to a boolean with the same value.
//
// Side effects:
//   - None.
func boolPtr(b bool) *bool {
	return &b
}

// convertMessages converts provider messages to the Ollama API message format,
// preserving tool calls on assistant messages.
//
// Expected:
//   - msgs is a slice of provider Message values, any of which may carry ToolCalls.
//
// Returns:
//   - An Ollama Message slice with Role, Content, and ToolCalls populated.
//
// Side effects:
//   - None.
func convertMessages(msgs []provider.Message) []ollamaAPI.Message {
	pairs := shared.ConvertMessagesToRolePairs(msgs)
	result := make([]ollamaAPI.Message, 0, len(pairs))
	for i, p := range pairs {
		ollamaMsg := ollamaAPI.Message{
			Role:    p.Role,
			Content: p.Content,
		}
		for _, tc := range msgs[i].ToolCalls {
			args := ollamaAPI.NewToolCallFunctionArguments()
			for k, v := range tc.Arguments {
				args.Set(k, v)
			}
			ollamaMsg.ToolCalls = append(ollamaMsg.ToolCalls, ollamaAPI.ToolCall{
				Function: ollamaAPI.ToolCallFunction{
					Name:      tc.Name,
					Arguments: args,
				},
			})
		}
		result = append(result, ollamaMsg)
	}
	return result
}

// buildOllamaTools converts provider tools to Ollama API tool definitions.
//
// Expected:
//   - tools is a slice of provider Tool values with schema information.
//
// Returns:
//   - An Ollama Tools slice with function definitions and properties.
//
// Side effects:
//   - None.
func buildOllamaTools(tools []provider.Tool) ollamaAPI.Tools {
	result := make(ollamaAPI.Tools, len(tools))
	for i, t := range tools {
		base := shared.BuildBaseToolSchema(t)
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
				Name:        base.Name,
				Description: base.Description,
				Parameters: ollamaAPI.ToolFunctionParameters{
					Type:       t.Schema.Type,
					Properties: props,
					Required:   base.Required,
				},
			},
		}
	}
	return result
}

// parseToolCalls converts Ollama tool calls to provider tool call format.
//
// Expected:
//   - toolCalls is a slice of Ollama ToolCall values.
//
// Returns:
//   - A slice of provider ToolCall values with ID, name, and arguments.
//
// Side effects:
//   - None.
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
