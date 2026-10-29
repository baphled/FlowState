package config

import "github.com/baphled/flowstate/internal/oauth"

// Config is the root FlowState configuration document.
type Config struct {
	Providers map[string]*ProviderConfig `yaml:"providers"`
}

// ProviderConfig configures a single AI provider.
type ProviderConfig struct {
	Host     string         `yaml:"host"`
	APIKey   string         `yaml:"api_key,omitempty"`
	Model    string         `yaml:"model"`
	AuthType oauth.AuthType `yaml:"auth_type"`
	OAuth    *oauth.Config  `yaml:"oauth,omitempty"`
}
