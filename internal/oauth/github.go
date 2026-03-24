package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	githubDeviceCodeURL = "https://github.com/login/device/code"
	githubTokenURL      = "https://github.com/login/oauth/access_token"
)

type GitHubOAuthProvider struct {
	clientID   string
	httpClient *http.Client
}

func NewGitHubOAuthProvider(clientID string) *GitHubOAuthProvider {
	return &GitHubOAuthProvider{
		clientID: clientID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type githubDeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type githubTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error,omitempty"`
	ErrorDesc   string `json:"error_description,omitempty"`
}

func (g *GitHubOAuthProvider) RequestDeviceCode(ctx context.Context) (*DeviceCodeResponse, error) {
	data := url.Values{}
	data.Set("client_id", g.clientID)
	data.Set("scope", "user")

	req, err := http.NewRequestWithContext(ctx, "POST", githubDeviceCodeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.URL.RawQuery = data.Encode()

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to request device code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device code request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var ghResp githubDeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&ghResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &DeviceCodeResponse{
		DeviceCode:      ghResp.DeviceCode,
		UserCode:        ghResp.UserCode,
		VerificationURI: ghResp.VerificationURI,
		ExpiresIn:       ghResp.ExpiresIn,
		Interval:        ghResp.Interval,
	}, nil
}

func (g *GitHubOAuthProvider) PollForToken(ctx context.Context, deviceCode string, interval int) (*Token, error) {
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			token, err := g.attemptTokenExchange(ctx, deviceCode)
			if err != nil {
				return nil, err
			}
			if token != nil {
				return token, nil
			}
		}
	}
}

func (g *GitHubOAuthProvider) attemptTokenExchange(ctx context.Context, deviceCode string) (*Token, error) {
	data := url.Values{}
	data.Set("client_id", g.clientID)
	data.Set("device_code", deviceCode)
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

	req, err := http.NewRequestWithContext(ctx, "POST", githubTokenURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.URL.RawQuery = data.Encode()

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to poll for token: %w", err)
	}
	defer resp.Body.Close()

	var ghResp githubTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&ghResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	switch ghResp.Error {
	case "":
		return &Token{
			AccessToken: ghResp.AccessToken,
			ExpiresAt:   time.Now().Add(8760 * time.Hour),
			Scopes:      []string{ghResp.Scope},
		}, nil
	case "authorization_pending":
		return nil, nil
	case "slow_down":
		time.Sleep(5 * time.Second)
		return nil, nil
	case "expired_token":
		return nil, fmt.Errorf("device code expired")
	case "access_denied":
		return nil, fmt.Errorf("user denied authorization")
	default:
		return nil, fmt.Errorf("token exchange failed: %s", ghResp.ErrorDesc)
	}
}

func (g *GitHubOAuthProvider) RefreshToken(ctx context.Context, refreshToken string) (*Token, error) {
	return nil, fmt.Errorf("GitHub OAuth does not support refresh tokens")
}
