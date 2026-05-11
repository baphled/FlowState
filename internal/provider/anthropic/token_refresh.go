package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultTokenEndpoint = "https://console.anthropic.com/v1/oauth/token"
	anthropicClientID    = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	refreshBufferMs      = 5 * 60 * 1000
	authFilePermissions  = 0o600
)

var errEmptyAccessToken = errors.New(
	"token refresh returned empty access token",
)

// RefreshResult holds the tokens returned by a successful refresh.
type RefreshResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
}

// TokenRefresher exchanges a refresh token for new access credentials.
type TokenRefresher interface {
	// Refresh exchanges a refresh token for new credentials.
	Refresh(
		ctx context.Context,
		refreshToken string,
	) (RefreshResult, error)
}

// HTTPTokenRefresher implements TokenRefresher via an HTTP POST.
type HTTPTokenRefresher struct {
	Client        *http.Client
	TokenEndpoint string
}

// tokenRefreshResponse holds the raw JSON fields from the Anthropic token endpoint.
type tokenRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// Refresh exchanges a refresh token for new access credentials.
//
// Expected:
//   - ctx is a valid context for request cancellation.
//   - refreshToken is a non-empty Anthropic OAuth refresh token.
//
// Returns:
//   - (RefreshResult, nil) on success.
//   - (RefreshResult{}, error) if the request fails.
//
// Side effects:
//   - Makes an HTTP POST to the Anthropic token endpoint.
func (r *HTTPTokenRefresher) Refresh(
	ctx context.Context,
	refreshToken string,
) (RefreshResult, error) {
	body := r.buildRefreshBody(refreshToken)
	resp, err := r.doRefreshRequest(ctx, body)
	if err != nil {
		return RefreshResult{}, err
	}
	return r.parseRefreshResponse(resp)
}

// buildRefreshBody constructs the URL-encoded form body for the token refresh request.
//
// Expected:
//   - refreshToken is a non-empty Anthropic OAuth refresh token.
//
// Returns:
//   - A URL-encoded string with grant_type, refresh_token, and client_id.
//
// Side effects:
//   - None.
func (r *HTTPTokenRefresher) buildRefreshBody(
	refreshToken string,
) string {
	params := url.Values{}
	params.Set("grant_type", "refresh_token")
	params.Set("refresh_token", refreshToken)
	params.Set("client_id", anthropicClientID)
	return params.Encode()
}

// doRefreshRequest sends the HTTP POST to the token endpoint and validates the status.
//
// Expected:
//   - ctx is a valid context for request cancellation.
//   - body is a URL-encoded form string.
//
// Returns:
//   - (*http.Response, nil) on a 200 OK response.
//   - (nil, error) if the request fails or returns a non-200 status.
//
// Side effects:
//   - Makes an HTTP POST to the configured token endpoint.
func (r *HTTPTokenRefresher) doRefreshRequest(
	ctx context.Context,
	body string,
) (*http.Response, error) {
	endpoint := r.TokenEndpoint
	if endpoint == "" {
		endpoint = defaultTokenEndpoint
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint,
		strings.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("creating refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing token refresh: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf(
				"token refresh: status %d: reading body: %w",
				resp.StatusCode, readErr,
			)
		}
		trimmed := strings.TrimSpace(string(body))
		if trimmed == "" {
			return nil, fmt.Errorf(
				"token refresh: status %d", resp.StatusCode,
			)
		}
		return nil, fmt.Errorf(
			"token refresh: status %d: %s",
			resp.StatusCode, trimmed,
		)
	}
	return resp, nil
}

// parseRefreshResponse decodes the JSON token response into a RefreshResult.
//
// Expected:
//   - resp is a non-nil HTTP response with a JSON body.
//
// Returns:
//   - (RefreshResult, nil) on success.
//   - (RefreshResult{}, error) if decoding fails or the access token is empty.
//
// Side effects:
//   - Closes the response body.
func (r *HTTPTokenRefresher) parseRefreshResponse(
	resp *http.Response,
) (RefreshResult, error) {
	defer resp.Body.Close()

	var raw tokenRefreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return RefreshResult{}, fmt.Errorf(
			"decoding refresh response: %w", err,
		)
	}

	if raw.AccessToken == "" {
		return RefreshResult{}, errEmptyAccessToken
	}

	return RefreshResult{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		ExpiresAt:    time.Now().UnixMilli() + raw.ExpiresIn*1000,
	}, nil
}

// TokenManager handles Anthropic OAuth token lifecycle.
type TokenManager struct {
	accessToken  string
	refreshToken string
	expiresAt    int64
	refresher    TokenRefresher
	authFilePath string
	mu           sync.Mutex
}

// NewTokenManager creates a TokenManager for OAuth token refresh.
//
// Expected:
//   - accessToken is a non-empty Anthropic OAuth access token.
//   - refreshToken is a non-empty refresh token.
//   - expiresAt is Unix milliseconds when the access token expires.
//   - refresher is a valid TokenRefresher implementation.
//   - tokenFilePath is a FlowState-owned JSON file for persisting refreshed
//     credentials. Empty means in-memory only (no persistence).
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
//   - token is a non-empty API token.
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
//   - May update auth.json on disk.
func (tm *TokenManager) EnsureToken(
	ctx context.Context,
) (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.needsRefresh() {
		return tm.accessToken, nil
	}

	if tm.refresher == nil {
		return tm.accessToken, nil
	}

	result, err := tm.refresher.Refresh(ctx, tm.refreshToken)
	if err != nil {
		return "", fmt.Errorf("refreshing anthropic token: %w", err)
	}

	tm.accessToken = result.AccessToken
	tm.refreshToken = result.RefreshToken
	tm.expiresAt = result.ExpiresAt

	// F3: persistTokens now surfaces errors. Log-and-continue here so
	// the in-memory refresh succeeds even when disk persistence fails
	// (the operator still gets the slog warning emitted from inside
	// persistTokens). This is the smallest-scope change: the hot
	// refresh path keeps its existing "in-memory state is the source
	// of truth for this process" contract; only the next-process
	// recovery story improves.
	if perr := tm.persistTokens(); perr != nil {
		slog.Warn("anthropic token refresh: persist failed; in-memory tokens still valid for this process",
			"error", perr)
	}
	return tm.accessToken, nil
}

// needsRefresh reports whether the access token is expired or within the refresh buffer.
//
// Returns:
//   - true if the token is empty or expires within the 5-minute buffer.
//   - false if the token is still valid.
//
// Side effects:
//   - None.
func (tm *TokenManager) needsRefresh() bool {
	if tm.accessToken == "" {
		return true
	}
	return time.Now().UnixMilli()+refreshBufferMs > tm.expiresAt
}

// AccessToken returns the current access token (for testing only).
//
// Returns:
//   - The current access token string.
//
// Side effects:
//   - Acquires and releases the internal mutex.
func (tm *TokenManager) AccessToken() string {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.accessToken
}

// ExpiresAt returns the expiry time in Unix milliseconds (for testing).
//
// Returns:
//   - The current token expiry as Unix milliseconds.
//
// Side effects:
//   - Acquires and releases the internal mutex.
func (tm *TokenManager) ExpiresAt() int64 {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.expiresAt
}

// SetExpiresAt overrides the expiry time (for testing only).
//
// Expected:
//   - ms is a Unix millisecond timestamp.
//
// Side effects:
//   - Acquires and releases the internal mutex.
func (tm *TokenManager) SetExpiresAt(ms int64) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.expiresAt = ms
}

// persistTokens writes updated OAuth tokens to the configured token file
// atomically: it serialises the payload, writes to a `.tmp` sibling,
// then renames it over the target. Rename is atomic on POSIX so the
// reader never sees a half-written file even if the process is killed
// or the host loses power mid-write.
//
// The file format is a flat JSON object:
//
//	{"type":"oauth","access":"...","refresh":"...","expires":1234567890}
//
// This is FlowState's own credential format; the file is written under the
// FlowState data dir (typically <dataDir>/tokens/anthropic.json) and is not
// shared with any other tool.
//
// F3 (Bug Hunt Findings May 11 2026): the pre-F3 implementation used a
// direct `os.WriteFile` with `_ = ...` error swallow. A SIGKILL or
// power loss mid-write truncated the file; a disk-full / EACCES /
// EROFS error was silently dropped. Both modes blackballed the
// OAuth refresh on the next process restart. The fix mirrors the
// recall + session-fork persistence pattern (temp+rename) and surfaces
// the error via slog and the return value so the calling refresh path
// can react. Same shape as recall/store.go:201 (persist).
//
// Returns:
//   - nil on success or when authFilePath is empty (intentional no-op).
//   - An error when marshalling, mkdir, temp-write, or rename fails.
//
// Side effects:
//   - Writes <authFilePath>.tmp and renames it over <authFilePath>.
//   - Logs failures via slog.Warn before returning the error.
func (tm *TokenManager) persistTokens() error {
	if tm.authFilePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(tm.authFilePath), 0o700); err != nil {
		slog.Warn("anthropic token refresh: mkdir on token dir failed",
			"path", tm.authFilePath, "error", err)
		return fmt.Errorf("mkdir token dir: %w", err)
	}
	payload := map[string]interface{}{
		"type":    "oauth",
		"access":  tm.accessToken,
		"refresh": tm.refreshToken,
		"expires": tm.expiresAt,
	}
	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		slog.Warn("anthropic token refresh: marshal payload failed",
			"path", tm.authFilePath, "error", err)
		return fmt.Errorf("marshal token payload: %w", err)
	}
	tmpPath := tm.authFilePath + ".tmp"
	if werr := os.WriteFile(tmpPath, out, authFilePermissions); werr != nil {
		slog.Warn("anthropic token refresh: write temp token file failed",
			"path", tmpPath, "error", werr)
		return fmt.Errorf("write temp token file: %w", werr)
	}
	if rerr := os.Rename(tmpPath, tm.authFilePath); rerr != nil {
		// Best-effort cleanup so a half-rename does not leak a
		// stale `.tmp` next to the live token file.
		_ = os.Remove(tmpPath)
		slog.Warn("anthropic token refresh: rename temp token file failed",
			"tmp", tmpPath, "target", tm.authFilePath, "error", rerr)
		return fmt.Errorf("rename temp token file: %w", rerr)
	}
	return nil
}

// PersistTokensForTest exposes persistTokens to the package's test
// suite so the F3 atomicity + error-surfacing specs can drive the
// persist path without standing up a full EnsureToken flow.
func (tm *TokenManager) PersistTokensForTest() error {
	return tm.persistTokens()
}
