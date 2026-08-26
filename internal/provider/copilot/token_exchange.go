package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// TokenExchanger exchanges a GitHub OAuth token for a Copilot API token.
type TokenExchanger interface {
	// Exchange exchanges a GitHub OAuth token for a Copilot API token.
	Exchange(ctx context.Context, githubToken string) (token string, expiresAt int64, err error)
}

// TokenExchangerImpl implements TokenExchanger using an HTTP client and base URL.
type TokenExchangerImpl struct {
	Client  *http.Client
	BaseURL string
}

// tokenResponse holds the JSON response from the Copilot token exchange endpoint.
type tokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

// Exchange exchanges a GitHub OAuth token for a Copilot API token using the configured HTTP client.
//
// Expected:
//   - ctx is a valid context for request cancellation.
//   - githubToken is a non-empty GitHub authentication token.
//
// Returns:
//   - (token, expiresAt, nil) on successful exchange.
//   - ("", 0, error) if the request fails, returns non-200, or the response is invalid.
//
// Side effects:
//   - Makes an HTTP GET request to the Copilot token exchange endpoint.
func (e *TokenExchangerImpl) Exchange(ctx context.Context, githubToken string) (string, int64, error) {
	endpoint := e.BaseURL + "/copilot_internal/v2/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return "", 0, fmt.Errorf("creating token exchange request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+githubToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", copilotUserAgent)
	req.Header.Set("Editor-Version", copilotEditorVersion)
	req.Header.Set("Editor-Plugin-Version", copilotPluginVersion)
	req.Header.Set("Copilot-Integration-Id", copilotIntegrationID)

	resp, err := e.Client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("exchanging copilot token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("copilot token exchange: status %d", resp.StatusCode)
	}

	var result tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", 0, fmt.Errorf("decoding token exchange response: %w", err)
	}

	if result.Token == "" {
		return "", 0, errors.New("copilot token exchange returned empty token")
	}

	return result.Token, result.ExpiresAt, nil
}

// TokenManager handles token lifecycle (exchange, cache, refresh).
type TokenManager struct {
	githubToken string
	exchanger   TokenExchanger
	mu          sync.Mutex
	token       string
	expiresAt   int64

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

// NewTokenManager creates a TokenManager that exchanges GitHub tokens using the provided TokenExchanger.
//
// Expected:
//   - githubToken is a non-empty GitHub authentication token.
//   - exchanger is a valid TokenExchanger implementation.
//
// Returns:
//   - A TokenManager configured to exchange tokens on demand.
//
// Side effects:
//   - None.
func NewTokenManager(githubToken string, exchanger TokenExchanger) *TokenManager {
	return &TokenManager{
		githubToken: githubToken,
		exchanger:   exchanger,
	}
}

// NewDirectTokenManager creates a TokenManager that always returns the provided token, never refreshing.
//
// Expected:
//   - token is a non-empty Copilot API token.
//
// Returns:
//   - A TokenManager with the token pre-set and a far-future expiry.
//
// Side effects:
//   - None.
func NewDirectTokenManager(token string) *TokenManager {
	return &TokenManager{
		token:     token,
		expiresAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
	}
}

const tokenRefreshBuffer = 60

// SetExpiresAt sets the expiry time for the token (for testing only).
//
// Expected:
//   - ts is a Unix timestamp representing the desired expiry time.
//
// Side effects:
//   - Acquires and releases the internal mutex.
//   - Overwrites the current expiresAt value.
//
// Returns: result of SetExpiresAt.
func (tm *TokenManager) SetExpiresAt(ts int64) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.expiresAt = ts
}

// ExpiresAt returns the expiry time (for testing only).
//
// Returns:
//   - The current token expiry as a Unix timestamp.
//
// Side effects:
//   - Acquires and releases the internal mutex.
//
// Expected: parameters for ExpiresAt.
func (tm *TokenManager) ExpiresAt() int64 {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.expiresAt
}

// EnsureToken returns a valid Copilot API token, exchanging or refreshing as needed.
//
// Expected:
//   - ctx is a valid context for request cancellation.
//
// Returns:
//   - (token, nil) if a valid cached token exists or exchange succeeds.
//   - ("", error) if the token exchange fails.
//
// Side effects:
//   - Acquires and releases the internal mutex.
//   - May perform an HTTP token exchange if the cached token is expired.
func (tm *TokenManager) EnsureToken(ctx context.Context) (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tm.token != "" && time.Now().Unix()+tokenRefreshBuffer < tm.expiresAt {
		return tm.token, nil
	}

	if tm.exchanger == nil {
		return tm.token, nil
	}

	tm.lastRefreshAt = time.Now()

	token, expiresAt, err := tm.exchanger.Exchange(ctx, tm.githubToken)
	if err != nil {
		tm.consecutiveFailures++
		return "", fmt.Errorf("refreshing copilot token: %w", err)
	}

	tm.token = token
	tm.expiresAt = expiresAt
	tm.consecutiveFailures = 0
	return tm.token, nil
}

// RefreshNow forces an immediate token exchange, bypassing the
// proactive expiry check inside EnsureToken.
//
// Expected:
//   - ctx is a valid context for request cancellation.
//
// Returns:
//   - nil on success (new token acquired and cached).
//   - error if the exchange attempt fails.
//
// Concurrency:
//   - Acquires and releases the internal mutex.
//   - May perform an HTTP token exchange.
//
// Side effects: None.
func (tm *TokenManager) RefreshNow(ctx context.Context) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.lastRefreshAt = time.Now()

	if tm.exchanger == nil {
		tm.consecutiveFailures++
		return fmt.Errorf("copilot token: no exchanger configured")
	}

	token, expiresAt, err := tm.exchanger.Exchange(ctx, tm.githubToken)
	if err != nil {
		tm.consecutiveFailures++
		return fmt.Errorf("refreshing copilot token: %w", err)
	}

	tm.token = token
	tm.expiresAt = expiresAt
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
