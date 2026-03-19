package config

import "github.com/baphled/flowstate/internal/oauth"

type Config struct {
	Providers map[string]*ProviderConfig `yaml:"providers"`
}

type ProviderConfig struct {
	Host     string             `yaml:"host"`
	APIKey   string             `yaml:"api_key,omitempty"`
	Model    string             `yaml:"model"`
	AuthType oauth.AuthType     `yaml:"auth_type"`
	OAuth    *oauth.OAuthConfig `yaml:"oauth,omitempty"`
}

func DefaultConfig() *Config {
	return &Config{
		Providers: make(map[string]*ProviderConfig),
	}
}

func (c *Config) AddProvider(name string, config *ProviderConfig) {
	if c.Providers == nil {
		c.Providers = make(map[string]*ProviderConfig)
	}
	c.Providers[name] = config
}

func (c *Config) GetProvider(name string) *ProviderConfig {
	return c.Providers[name]
}

func (c *Config) RemoveProvider(name string) {
	delete(c.Providers, name)
}
