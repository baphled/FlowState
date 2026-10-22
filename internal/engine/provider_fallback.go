// Package engine provides the core FlowState orchestration engine.
package engine

// ProviderFallbackEntry defines a single entry in the provider fallback
// chain. Each entry pins a provider name and, optionally, a model to use
// when higher-priority routing tiers cannot resolve a concrete route.
type ProviderFallbackEntry struct {
	// Provider is the configured provider name (e.g. "anthropic", "ollama").
	// Must match a key in providers.<name> in the config.
	Provider string `json:"provider" yaml:"provider"`
	// Model is the model identifier to use with this provider. When empty
	// the session's current model or the category routing model is used.
	Model string `json:"model,omitempty" yaml:"model,omitempty"`
}

// ProviderFallbackConfig defines the ordered fallback matrix for each
// workload purpose. The summariser walks its purpose's list in order,
// trying each (provider, model) pair until one succeeds.
type ProviderFallbackConfig struct {
	// Summariser lists the fallback entries the L2 auto-compactor uses
	// when category routing and session hints fail to produce a concrete
	// (provider, model) pair. The first entry is tried first; the last
	// entry is the ultimate fallback.
	Summariser []ProviderFallbackEntry `json:"summariser" yaml:"summariser"`
}

// DefaultProviderFallback returns the default fallback matrix shipped
// with FlowState. These values are the "we, the developers" defaults
// — they live in Go code as a single source of truth but are overridable
// via the provider_fallback YAML config block.
//
// The default pins every summariser fallback to Ollama + llama3.2,
// matching the historical behaviour that the app.go wiring derived from
// cfg.Providers.Ollama.Model. Operators who want a different fallback
// chain add entries to provider_fallback.summariser in their config.yaml
// (higher-priority providers first, the catch-all last).
//
// Returns:
//   - A ProviderFallbackConfig with a single Ollama entry.
//
// Side effects:
//   - None.
func DefaultProviderFallback() ProviderFallbackConfig {
	return ProviderFallbackConfig{
		Summariser: []ProviderFallbackEntry{
			{Provider: "ollama", Model: "llama3.2"},
		},
	}
}
