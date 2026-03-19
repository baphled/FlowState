// Package oauth provides OAuth 2.0 authentication implementations for FlowState providers.
package oauth

import (
	"context"
	"time"
)

// Provider defines the interface for OAuth 2.0 providers.
type Provider interface {
	// InitiateFlow starts the OAuth device flow and returns device code response.
	InitiateFlow(ctx context.Context) (*DeviceCodeResponse, error)
	// PollToken polls for authorization status until approved or expired.
	PollToken(ctx context.Context, deviceCode string, interval int) (*FlowResult, error)
}

// DeviceCodeResponse contains the device code and user verification instructions.
type DeviceCodeResponse struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	ExpiresIn       int
	Interval        int
}

// TokenResponse contains the OAuth access token and metadata.
type TokenResponse struct {
	AccessToken string
	TokenType   string
	ExpiresIn   int
	ExpiresAt   time.Time
}

// TokenStore defines the interface for storing and retrieving OAuth tokens.
type TokenStore interface {
	// Store saves an encrypted token for the provider.
	Store(provider string, token *TokenResponse) error
	// Retrieve loads and decrypts a token for the provider.
	Retrieve(provider string) (*TokenResponse, error)
	// Delete removes the stored token for the provider.
	Delete(provider string) error
	// HasToken checks if a token exists for the provider.
	HasToken(provider string) bool
}

// FlowState represents the current state of an OAuth device flow.
type FlowState int

// OAuth flow state constants.
const (
	StatePending FlowState = iota
	StateApproved
	StateExpired
	StateRateLimited
	StateError
)

// FlowResult represents the result of an OAuth device flow operation.
type FlowResult struct {
	State        FlowState
	Token        *TokenResponse
	RetryAfter   int
	ErrorMessage string
}

// CopilotScopes returns the required OAuth scopes for GitHub Copilot.
//
// Returns:
//   - A slice of strings containing "copilot" scope.
//
// Side effects:
//   - None.
func CopilotScopes() []string {
	return []string{"copilot"}
}
