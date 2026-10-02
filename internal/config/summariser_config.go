package config

import "github.com/baphled/flowstate/internal/engine"

// DefaultZaiSummariserModel is the model used for the preferred Z.AI
// summariser hop when the operator has not pinned one via
// summariser.model.
const DefaultZaiSummariserModel = "glm-4.7"

// SummariserConfig configures the Phase 1 compaction SummariserChain
// via the `summariser` YAML block. Provider names the preferred
// summariser provider (Z.AI when its credentials are present); Model
// pins the model for that hop; OllamaModel configures the last-resort
// Ollama hop so deployments are never hard-wired to llama3.2.
type SummariserConfig struct {
	// Provider is the preferred summariser provider name (e.g. "zai").
	// Empty defers to the Z.AI-when-credentials-present default.
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
	// Model is the model identifier for the preferred hop. Empty uses
	// the provider's configured default model.
	Model string `json:"model,omitempty" yaml:"model,omitempty"`
	// OllamaModel is the model for the last-resort Ollama hop. Empty
	// falls back to providers.ollama.model, then llama3.2.
	OllamaModel string `json:"ollama_model,omitempty" yaml:"ollama_model,omitempty"`
	// AnthropicModel is the model for the second-priority Anthropic
	// hop. Empty falls back to providers.anthropic.model.
	AnthropicModel string `json:"anthropic_model,omitempty" yaml:"anthropic_model,omitempty"`
}

// SummariserChainEntries resolves the ordered fallback matrix for the
// compaction SummariserChain: the configured preferred provider first
// (Z.AI when its credentials are present), Anthropic second, and
// Ollama with a configurable model as the last resort. Only providers
// whose configuration plausibly works are included, so a chain never
// wastes a hop on a provider with no credentials.
//
// Expected:
//   - cfg is the fully loaded AppConfig; may not be nil.
//
// Returns:
//   - The ordered []engine.SummariserChainEntry, highest priority
//     first. Never empty — the Ollama hop is always present as the
//     deterministic last resort.
//
// Side effects:
//   - None.
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
// provider without a provider-specific switch at the call site.
//
// Expected:
//   - name is a provider key such as "zai" or "anthropic".
//
// Returns:
//   - The configured model, or empty when the provider or its model
//     is unconfigured.
//
// Side effects:
//   - None.
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
