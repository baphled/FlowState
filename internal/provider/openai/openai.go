// Package openai provides an OpenAI provider implementation.
package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
	"github.com/baphled/flowstate/internal/provider/shared"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

var (
	errAPIKeyRequired     = errors.New("OpenAI API key is required")
	errOAuthTokenRequired = errors.New("OpenAI OAuth token is required")
)

// oauthTokenPrefix is the prefix for OpenAI OAuth access tokens. OpenAI OAuth
// tokens are JWTs that do not carry a fixed prefix like Anthropic's
// "sk-ant-oat01-". We detect OAuth by checking the UseOAuth config flag rather
// than a token prefix.
const oauthTokenPrefix = "eyJ"

// streamGuardHeaderTimeout is the time-to-first-byte (response-header) ceiling
// applied to the OpenAI client via shared.StreamGuardHTTPClient. The openai-go
// SDK passes NO per-attempt request timeout of its own, so without this a
// flapping provider that accepts the connection but never responds hangs the
// caller indefinitely (proven: internal/provider/openaicompat/flap_stall_diag_test.go).
// It does NOT cap total stream duration. Overridable in tests via
// SetStreamGuardHeaderTimeoutForTest (export_test.go).
var streamGuardHeaderTimeout = shared.DefaultResponseHeaderTimeout

// TokenManager handles OpenAI OAuth token lifecycle, mirroring the
// anthropic.TokenManager pattern. It provides EnsureToken() for
// obtaining a valid access token and supports direct (non-refreshing)
// and auto-refreshing modes.
type TokenManager struct {
	accessToken  string
	refreshToken string
	expiresAt    int64
	refresher    TokenRefresher
	authFilePath string
	mu           sync.Mutex

	// consecutiveFailures is the number of consecutive token refresh
	// failures since the last successful refresh. Reset to 0 after
	// every successful refresh. Used by the failover hook (S2) to
	// determine when to give up on this provider.
	consecutiveFailures int
	// lastRefreshAt is the wall-clock time of the most recent token
	// refresh attempt (successful or failed). Zero value means no
	// attempt has been made since process start.
	lastRefreshAt time.Time
}

// TokenRefresher defines the interface for refreshing an OAuth token.
type TokenRefresher interface {
	Refresh(ctx context.Context, refreshToken string) (RefreshResult, error)
}

// RefreshResult carries the tokens and expiry returned by a token refresh.
type RefreshResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
}

// NewTokenManager creates a TokenManager for OAuth token refresh.
//
// Expected:
//   - accessToken is a non-empty OpenAI OAuth access token.
//   - refreshToken is a non-empty refresh token (may be empty when refresh is unsupported).
//   - expiresAt is Unix milliseconds when the access token expires.
//   - refresher is a valid TokenRefresher implementation (may be nil for direct tokens).
//   - tokenFilePath is a FlowState-owned JSON file for persisting refreshed credentials.
//
// Returns:
//   - A configured TokenManager.
//
// Side effects:
//   - None.
func NewTokenManager(
	accessToken string,
	refreshToken string,
	expiresAt int64,
	refresher TokenRefresher,
	tokenFilePath string,
) *TokenManager {
	return &TokenManager{
		accessToken:  accessToken,
		refreshToken: refreshToken,
		expiresAt:    expiresAt,
		refresher:    refresher,
		authFilePath: tokenFilePath,
	}
}

// NewDirectTokenManager creates a TokenManager that never refreshes.
//
// Expected:
//   - token is a non-empty OAuth access token.
//
// Returns:
//   - A TokenManager with a far-future expiry.
//
// Side effects:
//   - None.
func NewDirectTokenManager(token string) *TokenManager {
	return &TokenManager{
		accessToken: token,
		expiresAt:   time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(),
	}
}

// EnsureToken returns a valid access token, refreshing if needed.
//
// Expected:
//   - ctx is a valid context for request cancellation.
//
// Returns:
//   - (token, nil) if a valid token exists or refresh succeeds.
//   - ("", error) if the refresh fails.
//
// Side effects:
//   - Acquires and releases the internal mutex.
//   - May perform an HTTP token refresh.
func (tm *TokenManager) EnsureToken(ctx context.Context) (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.needsRefresh() {
		return tm.accessToken, nil
	}

	if tm.refresher == nil {
		return tm.accessToken, nil
	}

	tm.lastRefreshAt = time.Now()

	result, err := tm.refresher.Refresh(ctx, tm.refreshToken)
	if err != nil {
		tm.consecutiveFailures++
		return "", fmt.Errorf("refreshing openai token: %w", err)
	}

	tm.accessToken = result.AccessToken
	tm.refreshToken = result.RefreshToken
	tm.expiresAt = result.ExpiresAt
	tm.consecutiveFailures = 0

	return tm.accessToken, nil
}

// needsRefresh reports whether the access token is within 5 minutes of expiry.
//
// Expected: parameters for needsRefresh.
// Returns: result of needsRefresh.
// Side effects: None.
func (tm *TokenManager) needsRefresh() bool {
	return time.Now().UnixMilli() >= tm.expiresAt-5*60*1000
}

// RefreshNow forces an immediate token refresh, bypassing the
// proactive expiry check inside EnsureToken.
//
// Expected:
//   - ctx is a valid context for request cancellation.
//
// Returns:
//   - nil on success (new token acquired and cached).
//   - error if the refresh attempt fails.
//
// Concurrency:
//   - Acquires and releases the internal mutex.
//   - May perform an HTTP token refresh.
//
// Side effects: None.
func (tm *TokenManager) RefreshNow(ctx context.Context) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.lastRefreshAt = time.Now()

	if tm.refresher == nil {
		tm.consecutiveFailures++
		return fmt.Errorf("openai token: no refresher configured")
	}

	result, err := tm.refresher.Refresh(ctx, tm.refreshToken)
	if err != nil {
		tm.consecutiveFailures++
		return fmt.Errorf("refreshing openai token: %w", err)
	}

	tm.accessToken = result.AccessToken
	tm.refreshToken = result.RefreshToken
	tm.expiresAt = result.ExpiresAt
	tm.consecutiveFailures = 0
	return nil
}

// RefreshStatus returns the last refresh attempt time and the
// consecutive failure count.
//
// Returns:
//   - lastAttempt is the wall-clock time of the most recent
//     RefreshNow or EnsureToken refresh attempt. Zero when no
//     attempt has been made since process start.
//   - consecutiveFailures is the number of consecutive refresh
//     failures since the last successful refresh.
//
// Expected: parameters for RefreshStatus.
// Side effects: None.
func (tm *TokenManager) RefreshStatus() (time.Time, int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.lastRefreshAt, tm.consecutiveFailures
}

// Provider implements the provider.Provider interface for OpenAI.
type Provider struct {
	client       openaiAPI.Client
	isOAuth      bool
	tokenManager *TokenManager
	currentToken string

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
//
// Expected: parameters for SetResponseObserver.
// Returns: result of SetResponseObserver.
// Side effects: None.
func (p *Provider) SetResponseObserver(fn func(http.Header)) {
	p.responseObserver = fn
}

// notifyResponseObserver invokes the registered observer with the
// response headers iff: (a) an observer is registered, AND (b) the
// raw response pointer is non-nil (defensive — SDK contract is to
// populate it before returning, but a nil here would crash the
// happy path).
//
// Expected: parameters for notifyResponseObserver.
// Returns: result of notifyResponseObserver.
// Side effects: None.
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
	// Route through NewWithOptions so the bare constructor shares the
	// single guarded client-build path (api key + stream-guard http client).
	return NewWithOptions(apiKey)
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
	allOpts := append([]option.RequestOption{
		option.WithAPIKey(apiKey),
		// Stream-guard client first so caller-supplied opts (and tests
		// passing their own WithHTTPClient) still override it.
		option.WithHTTPClient(shared.StreamGuardHTTPClient(streamGuardHeaderTimeout)),
	}, opts...)
	client := openaiAPI.NewClient(allOpts...)
	return &Provider{
		client: client,
	}, nil
}

// IsOAuthToken reports whether the given token is an OpenAI OAuth token.
// OpenAI OAuth access tokens are JWTs beginning with "eyJ".
//
// Expected:
//   - token is a string that may be an API key or OAuth token.
//
// Returns:
//   - true if the token looks like a JWT (OAuth bearer token).
//   - false otherwise.
//
// Side effects:
//   - None.
func IsOAuthToken(token string) bool {
	return strings.HasPrefix(token, oauthTokenPrefix)
}

// NewOAuth creates a new OpenAI provider configured for OAuth bearer authentication.
//
// Expected:
//   - token is a non-empty OpenAI OAuth access token.
//
// Returns:
//   - A configured Provider on success.
//   - An error if the token is empty.
//
// Side effects:
//   - None.
func NewOAuth(token string) (*Provider, error) {
	if token == "" {
		return nil, errOAuthTokenRequired
	}
	return &Provider{
		client:       newOAuthClient(token),
		isOAuth:      true,
		tokenManager: NewDirectTokenManager(token),
		currentToken: token,
	}, nil
}

// NewOAuthWithRefresh creates an OAuth provider with automatic token refresh.
//
// Expected:
//   - tm is a non-nil TokenManager with valid credentials.
//
// Returns:
//   - A configured Provider that refreshes tokens automatically.
//   - An error if the initial token cannot be obtained.
//
// Side effects:
//   - May perform an HTTP token refresh.
func NewOAuthWithRefresh(tm *TokenManager) (*Provider, error) {
	token, err := tm.EnsureToken(context.Background())
	if err != nil {
		return nil, fmt.Errorf(
			"openai OAuth token refresh failed "+
				"(re-authenticate via `flowstate auth openai`): %w",
			err,
		)
	}
	return &Provider{
		client:       newOAuthClient(token),
		isOAuth:      true,
		tokenManager: tm,
		currentToken: token,
	}, nil
}

// NewFromConfig creates an OpenAI provider from a configured credential.
// The credential is treated as an OAuth token when it begins with "eyJ"
// (JWT prefix), otherwise as an API key.
//
// Expected:
//   - credential is an OpenAI API key or OAuth access token; empty when
//     no credential is configured.
//
// Returns:
//   - A configured Provider on success.
//   - errAPIKeyRequired when credential is empty.
//
// Side effects:
//   - None.
func NewFromConfig(credential string) (*Provider, error) {
	if credential == "" {
		return nil, errAPIKeyRequired
	}
	if IsOAuthToken(credential) {
		return NewOAuth(credential)
	}
	return New(credential)
}

// newOAuthClient creates an OpenAI API client configured for OAuth bearer authentication.
//
// Expected:
//   - token is a non-empty OAuth bearer token.
//
// Returns:
//   - A configured OpenAI API client with bearer auth and stream-guard HTTP client.
//
// Side effects:
//   - None.
func newOAuthClient(token string) openaiAPI.Client {
	opts := []option.RequestOption{
		option.WithAPIKey(token),
		option.WithHTTPClient(shared.StreamGuardHTTPClient(streamGuardHeaderTimeout)),
	}
	return openaiAPI.NewClient(opts...)
}

// refreshClientIfNeeded ensures the OAuth token is current and rebuilds the client on change.
//
// Expected:
//   - ctx is a valid context for potential token refresh.
//
// Returns:
//   - nil if the token is valid or was refreshed successfully.
//   - An error if token refresh fails.
//
// Side effects:
//   - May perform an HTTP token refresh.
//   - May replace the internal OpenAI API client.
func (p *Provider) refreshClientIfNeeded(ctx context.Context) error {
	if p.tokenManager == nil {
		return nil
	}
	token, err := p.tokenManager.EnsureToken(ctx)
	if err != nil {
		return err
	}
	if token != p.currentToken {
		p.client = newOAuthClient(token)
		p.currentToken = token
	}
	return nil
}

// Name returns the provider name.
//
// Returns:
//   - The string "openai".
//
// Side effects:
//   - None.
//
// Expected: parameters for Name.
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
	if err := p.refreshClientIfNeeded(ctx); err != nil {
		return nil, err
	}
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
	if err := p.refreshClientIfNeeded(ctx); err != nil {
		return provider.ChatResponse{}, err
	}
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
//
// Expected: parameters for Models.
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
	//
	// gpt-5.x context-truncation fix: the static catalog enumerated only
	// the bare "gpt-5" id, so every point-release and size variant
	// (gpt-5.5, gpt-5-mini, …) fell through to the fallback. A planning-
	// swarm plan-writer on gpt-5.5 hit limit=32768 (SystemPromptBudget),
	// truncated at percentage=100 before emitting its plan document, and
	// failed the swarm's plan gate. The gpt-5 family ships the same
	// 400K-context / 128K-max-output budget (OpenAI published specs), so
	// enumerate the family explicitly — mirroring zai.go's hardcoded
	// defaultContextLength catalog — rather than leaning on the fallback.
	// gpt5ContextLength / gpt5OutputLimit name the shared family budget so
	// future point-releases inherit it from a single source of truth.
	const (
		gpt5ContextLength = 400000
		gpt5OutputLimit   = 128000
	)
	return []provider.Model{
		{ID: "gpt-5", Provider: "openai", ContextLength: gpt5ContextLength, OutputLimit: gpt5OutputLimit},
		{ID: "gpt-5.1", Provider: "openai", ContextLength: gpt5ContextLength, OutputLimit: gpt5OutputLimit},
		{ID: "gpt-5.2", Provider: "openai", ContextLength: gpt5ContextLength, OutputLimit: gpt5OutputLimit},
		{ID: "gpt-5.4", Provider: "openai", ContextLength: gpt5ContextLength, OutputLimit: gpt5OutputLimit},
		{ID: "gpt-5.4-mini", Provider: "openai", ContextLength: gpt5ContextLength, OutputLimit: gpt5OutputLimit},
		{ID: "gpt-5.5", Provider: "openai", ContextLength: gpt5ContextLength, OutputLimit: gpt5OutputLimit},
		{ID: "gpt-5-mini", Provider: "openai", ContextLength: gpt5ContextLength, OutputLimit: gpt5OutputLimit},
		{ID: "gpt-5-nano", Provider: "openai", ContextLength: gpt5ContextLength, OutputLimit: gpt5OutputLimit},
		{ID: "gpt-4o", Provider: "openai", ContextLength: 128000, OutputLimit: 16384},
		{ID: "gpt-4o-mini", Provider: "openai", ContextLength: 128000, OutputLimit: 16384},
		{ID: "gpt-4-turbo", Provider: "openai", ContextLength: 128000, OutputLimit: 4096},
		{ID: "gpt-3.5-turbo", Provider: "openai", ContextLength: 16385, OutputLimit: 4096},
	}, nil
}
