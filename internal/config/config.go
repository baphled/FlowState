package config

// DefaultConfig returns an empty configuration with initialised maps.
func DefaultConfig() *Config {
	return &Config{
		Providers: make(map[string]*ProviderConfig),
	}
}

// AddProvider registers or replaces a provider configuration by name.
func (c *Config) AddProvider(name string, config *ProviderConfig) {
	if c.Providers == nil {
		c.Providers = make(map[string]*ProviderConfig)
	}
	c.Providers[name] = config
}

// GetProvider returns the provider configuration registered under name.
func (c *Config) GetProvider(name string) *ProviderConfig {
	return c.Providers[name]
}

// RemoveProvider deletes the provider configuration registered under name.
func (c *Config) RemoveProvider(name string) {
	delete(c.Providers, name)
}
