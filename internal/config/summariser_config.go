package config

import "github.com/baphled/flowstate/internal/engine"

// DefaultZaiSummariserModel is the model used for the preferred Z.AI
// summariser hop when the operator has not pinned one via
// summariser.model.
const DefaultZaiSummariserModel = "glm-4.7"

// SummariserConfig configures the compaction SummariserChain via the
// `summariser` YAML block.
type SummariserConfig struct {
	// Provider is the preferred summariser provider name (e.g. "zai").
	// Empty defers to the Z.AI-when-credentials-present default.
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
	// Model is the model identifier for the preferred hop.
	Model string `json:"model,omitempty" yaml:"model,omitempty"`
	// OllamaModel is the model for the last-resort Ollama hop.
	OllamaModel string `json:"ollama_model,omitempty" yaml:"ollama_model,omitempty"`
	// AnthropicModel is the model for the second-priority Anthropic hop.
	AnthropicModel string `json:"anthropic_model,omitempty" yaml:"anthropic_model,omitempty"`
}

// ProviderModelConfig carries the per-provider model and credential
// fields the summariser chain resolution needs.
type ProviderModelConfig struct {
	APIKey string
	Model  string
}

// ProvidersConfig is the per-provider configuration subset used by the
// summariser chain.
type ProvidersConfig struct {
	ZAI       ProviderModelConfig
	Anthropic ProviderModelConfig
	Ollama    ProviderModelConfig
	OpenAI    ProviderModelConfig
}

// AppConfig is the application configuration surface scoped to the
// summariser chain on this branch.
type AppConfig struct {
	Providers  ProvidersConfig
	Summariser SummariserConfig
}

// SummariserChainEntries resolves the ordered fallback matrix for the
// compaction SummariserChain: the configured preferred provider first
// (Z.AI when its credentials are present), Anthropic second, and
// Ollama with a configurable model as the last resort. Only providers
// whose configuration plausibly works are included.
func (c *AppConfig) SummariserChainEntries() []engine.SummariserChainEntry {
	entries := make([]engine.SummariserChainEntry, 0, 3)

	if c.Summariser.Provider != "" && c.Summariser.Provider != "ollama" {
		model := c.Summariser.Model
		if model == "" {
			model = c.providerDefaultModel(c.Summariser.Provider)
		}
		entries = append(entries, engine.SummariserChainEntry{Provider: c.Summariser.Provider, Model: model})
	} else if c.Providers.ZAI.APIKey != "" {
		model := c.Summariser.Model
		if model == "" {
			model = c.Providers.ZAI.Model
		}
		if model == "" {
			model = DefaultZaiSummariserModel
		}
		entries = append(entries, engine.SummariserChainEntry{Provider: "zai", Model: model})
	}

	anthropicModel := c.Summariser.AnthropicModel
	if anthropicModel == "" {
		anthropicModel = c.Providers.Anthropic.Model
	}
	entries = append(entries, engine.SummariserChainEntry{Provider: "anthropic", Model: anthropicModel})

	ollamaModel := c.Summariser.OllamaModel
	if ollamaModel == "" {
		ollamaModel = c.Providers.Ollama.Model
	}
	entries = append(entries, engine.SummariserChainEntry{Provider: "ollama", Model: ollamaModel})

	return entries
}

// providerDefaultModel resolves the configured model for a named
// provider.
func (c *AppConfig) providerDefaultModel(name string) string {
	switch name {
	case "zai":
		return c.Providers.ZAI.Model
	case "anthropic":
		return c.Providers.Anthropic.Model
	case "ollama":
		return c.Providers.Ollama.Model
	case "openai":
		return c.Providers.OpenAI.Model
	default:
		return ""
	}
}
