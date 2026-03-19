package oauth

import (
	"context"
	"time"
)

type AuthType string

const (
	AuthTypeAPIKey AuthType = "api_key"
	AuthTypeOAuth  AuthType = "oauth"
)

type Token struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Scopes       []string
}

type DeviceCodeResponse struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	ExpiresIn       int
	Interval        int
}

type OAuthProvider interface {
	RequestDeviceCode(ctx context.Context) (*DeviceCodeResponse, error)
	PollForToken(ctx context.Context, deviceCode string, interval int) (*Token, error)
	RefreshToken(ctx context.Context, refreshToken string) (*Token, error)
}

type TokenStorage interface {
	Save(providerName string, token *Token) error
	Load(providerName string) (*Token, error)
	Delete(providerName string) error
	Exists(providerName string) bool
}

type OAuthConfig struct {
	ClientID    string   `yaml:"client_id"`
	Scopes      []string `yaml:"scopes,omitempty"`
	RedirectURI string   `yaml:"redirect_uri,omitempty"`
}
