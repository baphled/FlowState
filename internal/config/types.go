package config

import "github.com/baphled/flowstate/internal/oauth"

// Config is the root FlowState configuration document.
type Config struct {
	Providers map[string]*ProviderConfig `yaml:"providers"`
}

// ProviderConfig configures a single AI provider.
type ProviderConfig struct {
	Host   string `yaml:"host"`
	APIKey string `yaml:"api_key,omitempty"`
	Model  string `yaml:"model"`
	// Plan selects the delivery route for providers with plan-tiered
	// endpoints; zai honours "coding" (Anthropic-compatible route).
	Plan     string         `yaml:"plan,omitempty"`
	AuthType oauth.AuthType `yaml:"auth_type"`
	OAuth    *oauth.Config  `yaml:"oauth,omitempty"`
}
