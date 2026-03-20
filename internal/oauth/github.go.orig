package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	githubDeviceCodeURL = "https://github.com/login/device/code"
	githubTokenURL      = "https://github.com/login/oauth/access_token"
	githubHeaderAccept  = "application/json"
	githubHeaderContent = "application/json"
)

// GitHub implements the OAuth 2.0 Device Flow for GitHub.
type GitHub struct {
	clientID string
	client   *http.Client
}

// NewGitHub creates a new GitHub OAuth provider with the given client ID.
//
// Expected:
//   - clientID is a valid GitHub OAuth application client ID.
//
// Returns:
//   - A configured GitHub OAuth provider.
//
// Side effects:
//   - None.
func NewGitHub(clientID string) *GitHub {
	return &GitHub{
		clientID: clientID,
		client:   &http.Client{},
	}
}

// InitiateFlow starts the GitHub Device Flow authentication process.
//
// Expected:
//   - ctx is a non-nil context for request cancellation.
//
// Returns:
//   - A DeviceCodeResponse with user code and verification URL, or an error on failure.
//
// Side effects:
//   - Makes an HTTP POST request to the GitHub device code endpoint.
func (g *GitHub) InitiateFlow(ctx context.Context) (*DeviceCodeResponse, error) {
	body, err := json.Marshal(map[string]string{
		"client_id": g.clientID,
		"scope":     strings.Join(CopilotScopes(), " "),
	})
	if err != nil {
		return nil, fmt.Errorf("marshalling device code request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubDeviceCodeURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating device code request: %w", err)
	}
	req.Header.Set("Content-Type", githubHeaderContent)
	req.Header.Set("Accept", githubHeaderAccept)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("device code request: status %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("device code request: status %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding device code response: %w", err)
	}

	return &DeviceCodeResponse{
		DeviceCode:      result.DeviceCode,
		UserCode:        result.UserCode,
		VerificationURI: result.VerificationURI,
		ExpiresIn:       result.ExpiresIn,
		Interval:        result.Interval,
	}, nil
}

// PollToken polls the token endpoint until the user approves or the flow expires.
//
// Expected:
//   - ctx is a non-nil context for request cancellation.
//   - deviceCode is a valid device code from InitiateFlow.
//   - interval is the polling interval in seconds (minimum 5).
//
// Returns:
//   - A FlowResult with the authorization state and token if approved, or an error on failure.
//
// Side effects:
//   - Polls the GitHub token endpoint at regular intervals.
//   - Blocks until authorization is complete, expired, or context is cancelled.
func (g *GitHub) PollToken(ctx context.Context, deviceCode string, interval int) (*FlowResult, error) {
	if interval < 5 {
		interval = 5
	}

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	timeout := time.After(5 * time.Minute)

	for {
		select {
		case <-ctx.Done():
			return &FlowResult{State: StateError, ErrorMessage: ctx.Err().Error()}, ctx.Err()
		case <-timeout:
			return &FlowResult{State: StateExpired, ErrorMessage: "authorization request expired"}, nil
		case <-ticker.C:
			result := g.checkTokenStatus(ctx, deviceCode)
			if result.State != StatePending {
				return result, nil
			}
		}
	}
}

// checkTokenStatus makes a single request to check token status.
//
// Expected:
//   - ctx is a non-nil context for request cancellation.
//   - deviceCode is a valid device code from InitiateFlow.
//
// Returns:
//   - A FlowResult with the current authorization state.
//
// Side effects:
//   - Makes an HTTP POST request to the GitHub token endpoint.
func (g *GitHub) checkTokenStatus(ctx context.Context, deviceCode string) *FlowResult {
	body, err := json.Marshal(map[string]string{
		"client_id":   g.clientID,
		"device_code": deviceCode,
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
	})
	if err != nil {
		return &FlowResult{State: StateError, ErrorMessage: "marshalling token request: " + err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubTokenURL, bytes.NewReader(body))
	if err != nil {
		return &FlowResult{State: StateError, ErrorMessage: "creating token request: " + err.Error()}
	}
	req.Header.Set("Content-Type", githubHeaderContent)
	req.Header.Set("Accept", githubHeaderAccept)

	resp, err := g.client.Do(req)
	if err != nil {
		return &FlowResult{State: StateError, ErrorMessage: "requesting token: " + err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &FlowResult{State: StateError, ErrorMessage: fmt.Sprintf("token request: status %d", resp.StatusCode)}
	}

	var result struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
		Interval    int    `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return &FlowResult{State: StateError, ErrorMessage: "decoding token response: " + err.Error()}
	}

	return parseTokenResponse(result)
}

// parseTokenResponse parses the GitHub token API response and returns the appropriate FlowResult.
//
// Expected:
//   - result contains the parsed JSON response from the GitHub token endpoint.
//
// Returns:
//   - A FlowResult with the authorization state and token if approved.
//
// Side effects:
//   - None.
func parseTokenResponse(result struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
	Interval    int    `json:"interval"`
}) *FlowResult {
	switch result.Error {
	case "":
		return &FlowResult{
			State: StateApproved,
			Token: &TokenResponse{
				AccessToken: result.AccessToken,
				TokenType:   result.TokenType,
				ExpiresIn:   result.ExpiresIn,
				ExpiresAt:   time.Now().Add(time.Duration(result.ExpiresIn) * time.Second),
			},
		}
	case "authorization_pending":
		return &FlowResult{State: StatePending}
	case "slow_down":
		interval := 5
		if result.Interval > 0 {
			interval = result.Interval
		}
		return &FlowResult{State: StatePending, RetryAfter: interval}
	case "expired_token":
		return &FlowResult{State: StateExpired, ErrorMessage: "device code expired"}
	case "access_denied":
		return &FlowResult{State: StateError, ErrorMessage: "access denied by user"}
	case "incorrect_device_code", "incorrect_client_id":
		return &FlowResult{State: StateError, ErrorMessage: result.ErrorDesc}
	default:
		return &FlowResult{State: StateError, ErrorMessage: result.ErrorDesc}
	}
}
