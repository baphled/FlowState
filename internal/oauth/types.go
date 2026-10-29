package oauth

import (
	"context"
	"time"
)

// AuthType identifies how a provider authenticates requests.
type AuthType string

// Supported provider authentication types.
const (
	// AuthTypeAPIKey authenticates with a static API key.
	AuthTypeAPIKey AuthType = "api_key"
	// AuthTypeOAuth authenticates via the OAuth device flow.
	AuthTypeOAuth AuthType = "oauth"
)

// Token holds provider OAuth credentials.
type Token struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Scopes       []string
}

// DeviceCodeResponse is the device-flow authorisation grant issued by a
// provider.
type DeviceCodeResponse struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	ExpiresIn       int
	Interval        int
}

// OAuthProvider performs the provider side of the OAuth device flow.
type OAuthProvider interface {
	// RequestDeviceCode starts device-flow authorisation for the
	// provider.
	RequestDeviceCode(ctx context.Context) (*DeviceCodeResponse, error)
	// PollForToken exchanges a device code for tokens once the user
	// approves.
	PollForToken(ctx context.Context, deviceCode string, interval int) (*Token, error)
	// RefreshToken exchanges a refresh token for fresh credentials.
	RefreshToken(ctx context.Context, refreshToken string) (*Token, error)
}

// TokenStorage persists provider tokens.
type TokenStorage interface {
	// Save stores the token for the named provider.
	Save(providerName string, token *Token) error
	// Load retrieves the token for the named provider.
	Load(providerName string) (*Token, error)
	// Delete removes the stored token for the named provider.
	Delete(providerName string) error
	// Exists reports whether a token is stored for the provider.
	Exists(providerName string) bool
}

// OAuthConfig configures a provider's OAuth client.
type OAuthConfig struct {
	ClientID    string   `yaml:"client_id"`
	Scopes      []string `yaml:"scopes,omitempty"`
	RedirectURI string   `yaml:"redirect_uri,omitempty"`
}
