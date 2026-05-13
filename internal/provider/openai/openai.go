// Package openai provides an OpenAI provider implementation.
package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

var errAPIKeyRequired = errors.New("OpenAI API key is required")

// Provider implements the provider.Provider interface for OpenAI.
type Provider struct {
	client openaiAPI.Client

	// responseObserver is called on every 2xx (success-path) response
	// from Chat or the stream handshake with the response headers.
	// Nil by default — set via SetResponseObserver from the per-
	// provider quota.go adapter (PR3 of the Provider Quota and Spend
	// Visibility plan; mirrors anthropic.Provider's same field from
	// PR1, commit 36cee69c).
	//
	// Per memory feedback_grep_for_behaviour_pinning_before_red: the
	// existing error-path RateLimit parsing inside openaicompat
	// (extractRateLimitHeadersFromError) stays unchanged; this is an
	// additive observer that lights up success-path parsing without
	// flipping the error-path contract.
	responseObserver func(http.Header)
}

// SetResponseObserver registers a callback the Provider invokes on
// every 2xx response with the response headers. Passing nil clears
// the observer (defensive — engine reconfigures may unwire and
// rewire the adapter).
//
// Mirrors anthropic.Provider.SetResponseObserver from Quota PR1.
//
// Concurrency: in v1 the observer is set once at boot and never
// changes during a session, so no locking is needed. v2 hot-reload
// work will need to revisit this seam.
func (p *Provider) SetResponseObserver(fn func(http.Header)) {
	p.responseObserver = fn
}

// notifyResponseObserver invokes the registered observer with the
// response headers iff: (a) an observer is registered, AND (b) the
// raw response pointer is non-nil (defensive — SDK contract is to
// populate it before returning, but a nil here would crash the
// happy path).
func (p *Provider) notifyResponseObserver(raw *http.Response) {
	if p.responseObserver == nil || raw == nil {
		return
	}
	p.responseObserver(raw.Header)
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
	// Attachment-size pre-flight gate (plan §6 task-11 — shared 25 MB
	// ceiling at the engine seam, mirroring the Anthropic provider's
	// gate). Surfaces the typed error to the caller before any
	// network round-trip.
	if err := openaicompat.GateAttachmentRequestSize(req); err != nil {
		return nil, err
	}
	params := openaicompat.BuildParams(req)
	// PR3 success-path lift: thread the quota response observer (if
	// bound) through openaicompat.RunStreamWithObserver so the chip's
	// live RateLimit Snapshot updates on the streaming handshake, not
	// just on error returns. Observer is nil-safe (passing nil is
	// identical to the legacy RunStream).
	return openaicompat.RunStreamWithObserver(ctx, p.client, params, p.Name(), p.responseObserver), nil
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
	// Attachment-size pre-flight gate (plan §6 task-11) — same shared
	// ceiling as Stream so multipart-image requests fail loudly before
	// hitting the wire.
	if err := openaicompat.GateAttachmentRequestSize(req); err != nil {
		return provider.ChatResponse{}, err
	}
	params := openaicompat.BuildParams(req)

	// PR3 success-path lift: when an observer is bound, ask the SDK to
	// populate rawResp so we can hand the response headers to the
	// quota observer once the 2xx returns. Mirrors
	// anthropic.go:Chat:441-463.
	var rawResp *http.Response
	var chatOpts []option.RequestOption
	if p.responseObserver != nil {
		chatOpts = append(chatOpts, option.WithResponseInto(&rawResp))
	}

	resp, err := p.client.Chat.Completions.New(ctx, params, chatOpts...)
	if err != nil {
		return provider.ChatResponse{}, openaicompat.WrapChatError(p.Name(), err)
	}

	// Success path: notify the quota observer before returning so the
	// chip's Snapshot is fresh on every 2xx response, not just on the
	// 429 error path.
	p.notifyResponseObserver(rawResp)

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
	// OutputLimit values from OpenAI's published model documentation
	// (gpt-4o family ships 16384-token max output; gpt-3.5-turbo ships
	// 4096). Surfaced via Slice 1 of the Phase-4 follow-ups so the
	// engine's overflow gate sizes its output reserve per-model.
	//
	// Phase-5 Slice β added gpt-5 (400K context, 128K max output, per
	// OpenAI's published gpt-5 specs) — without the explicit entry,
	// callers selecting the model fell through to the engine's
	// ctxstore.DefaultModelContextFallback (16K) and forced spurious
	// overflow refusals.
	return []provider.Model{
		{ID: "gpt-5", Provider: "openai", ContextLength: 400000, OutputLimit: 128000},
		{ID: "gpt-4o", Provider: "openai", ContextLength: 128000, OutputLimit: 16384},
		{ID: "gpt-4o-mini", Provider: "openai", ContextLength: 128000, OutputLimit: 16384},
		{ID: "gpt-4-turbo", Provider: "openai", ContextLength: 128000, OutputLimit: 4096},
		{ID: "gpt-3.5-turbo", Provider: "openai", ContextLength: 16385, OutputLimit: 4096},
	}, nil
}
