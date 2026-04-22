package app

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("resolveFailoverTiers", func() {
	It("returns config tiers when non-empty", func() {
		configTiers := map[string]string{
			"anthropic": failover.Tier0,
			"openai":    failover.Tier1,
		}

		result := resolveFailoverTiers(configTiers)

		Expect(result).To(Equal(configTiers))
	})

	It("returns default tiers when config is empty", func() {
		result := resolveFailoverTiers(map[string]string{})

		Expect(result).To(Equal(defaultFailoverTiers()))
	})

	It("returns default tiers when config is nil", func() {
		result := resolveFailoverTiers(nil)

		Expect(result).To(Equal(defaultFailoverTiers()))
	})
})

var _ = Describe("buildConfigProviderPreferences", func() {
	It("includes zai, copilot, and openzen when configured with models", func() {
		cfg := &config.AppConfig{
			Providers: config.ProvidersConfig{
				Default: "zai",
				Ollama:  config.ProviderConfig{Model: "llama3.2"},
				ZAI:     config.ProviderConfig{Model: "glm-4.7"},
				GitHub:  config.ProviderConfig{Model: "gpt-4o"},
				OpenZen: config.ProviderConfig{Model: "qwen-coder"},
			},
		}

		prefs := buildConfigProviderPreferences(cfg)

		providerNames := make([]string, 0, len(prefs))
		for _, p := range prefs {
			providerNames = append(providerNames, p.Provider)
		}
		Expect(providerNames).To(ContainElement("zai"),
			"zai must be present in preferences when providers.zai.model is configured")
		Expect(providerNames).To(ContainElement("openzen"),
			"openzen must be present in preferences when providers.openzen.model is configured")
	})

	It("places the default provider first when default is zai", func() {
		cfg := &config.AppConfig{
			Providers: config.ProvidersConfig{
				Default:   "zai",
				Ollama:    config.ProviderConfig{Model: "llama3.2"},
				Anthropic: config.ProviderConfig{Model: "claude-sonnet-4"},
				ZAI:       config.ProviderConfig{Model: "glm-4.7"},
			},
		}

		prefs := buildConfigProviderPreferences(cfg)

		Expect(prefs).ToNot(BeEmpty())
		Expect(prefs[0]).To(Equal(provider.ModelPreference{
			Provider: "zai",
			Model:    "glm-4.7",
		}), "providers.default: zai must be the first preference, not ollama")
	})

	It("skips providers with no model configured", func() {
		cfg := &config.AppConfig{
			Providers: config.ProvidersConfig{
				Default: "ollama",
				Ollama:  config.ProviderConfig{Model: "llama3.2"},
				ZAI:     config.ProviderConfig{Model: ""},
			},
		}

		prefs := buildConfigProviderPreferences(cfg)

		for _, p := range prefs {
			Expect(p.Provider).ToNot(Equal("zai"),
				"zai must be omitted when no model is configured")
		}
	})
})
