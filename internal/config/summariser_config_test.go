package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

// SummariserChainEntries tests cover the Phase 1 compaction fallback
// matrix resolution: the configured provider (Z.AI when credentials
// are present) first, Anthropic second, and a configurable-model
// Ollama as the deterministic last resort.
var _ = Describe("AppConfig.SummariserChainEntries", func() {
	It("puts Z.AI first when its credentials are present and nothing is pinned", func() {
		cfg := config.DefaultConfig()
		cfg.Providers.ZAI.APIKey = "test-key"

		entries := cfg.SummariserChainEntries()

		Expect(entries).To(HaveLen(3))
		Expect(entries[0].Provider).To(Equal("zai"))
		Expect(entries[0].Model).To(Equal(config.DefaultZaiSummariserModel))
		Expect(entries[1].Provider).To(Equal("anthropic"))
		Expect(entries[2].Provider).To(Equal("ollama"))
	})

	It("skips Z.AI when no credentials are configured", func() {
		cfg := config.DefaultConfig()
		cfg.Providers.ZAI.APIKey = ""

		entries := cfg.SummariserChainEntries()

		Expect(entries).To(HaveLen(2))
		Expect(entries[0].Provider).To(Equal("anthropic"))
		Expect(entries[1].Provider).To(Equal("ollama"))
	})

	It("honours summariser.provider and summariser.model overrides", func() {
		cfg := config.DefaultConfig()
		cfg.Summariser.Provider = "zai"
		cfg.Summariser.Model = "glm-5"

		entries := cfg.SummariserChainEntries()

		Expect(entries[0].Provider).To(Equal("zai"))
		Expect(entries[0].Model).To(Equal("glm-5"))
	})

	It("uses the configured Ollama model instead of hardcoding llama3.2", func() {
		cfg := config.DefaultConfig()
		cfg.Summariser.OllamaModel = "qwen3:14b"

		entries := cfg.SummariserChainEntries()

		Expect(entries).To(HaveLen(2))
		Expect(entries[1].Provider).To(Equal("ollama"))
		Expect(entries[1].Model).To(Equal("qwen3:14b"))
	})

	It("falls back to the provider-configured Ollama model", func() {
		cfg := config.DefaultConfig()
		cfg.Providers.Ollama.Model = "devstral"

		entries := cfg.SummariserChainEntries()

		Expect(entries[len(entries)-1].Model).To(Equal("devstral"))
	})
})
