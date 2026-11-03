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
	// endpoints; zai honours "coding" (default OpenAI-compatible coding
	// endpoint).
	Plan string `yaml:"plan,omitempty"`
	// Route selects the coding-plan wire format: "openai" (default) or
	// "anthropic" (https://api.z.ai/api/anthropic, live-verified).
	Route    string         `yaml:"route,omitempty"`
	AuthType oauth.AuthType `yaml:"auth_type"`
	OAuth    *oauth.Config  `yaml:"oauth,omitempty"`
}
