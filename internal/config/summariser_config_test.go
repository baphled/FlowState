package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"testing"

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

// M01 negative tests — summariser chain must not cross the delivery
// allowlist. The operator's config (see
// manifest-audit/evidence/config-pre-M01-2026-10-19.yaml) has
// summariser.provider=zai, model=glm-5.2, ollama_model=''. With the
// Z.ai credential present the chain must contain ONLY zai hops: no
// anthropic, no ollama last-resort crossing even when the preferred
// hop fails — chain construction itself must respect the allowlist so
// the last resort never lands outside Z.ai.

func deliverySummariserChain() []struct{ Provider, Model string } {
	config.SetSummariserAllowlist(config.DefaultSummariserDeliveryAllowlist())
	cfg := &config.AppConfig{}
	cfg.Providers.ZAI.APIKey = "test-key"
	cfg.Providers.ZAI.Model = "glm-5.2"
	cfg.Summariser.Provider = "zai"
	cfg.Summariser.Model = "glm-5.2"
	cfg.Summariser.OllamaModel = ""
	entries := cfg.SummariserChainEntriesScoped()
	out := make([]struct{ Provider, Model string }, 0, len(entries))
	for _, e := range entries {
		out = append(out, struct{ Provider, Model string }{e.Provider, e.Model})
	}
	return out
}

func TestSummariserChainZaiOnlyUnderDeliveryScope(t *testing.T) {
	chain := deliverySummariserChain()
	if len(chain) == 0 {
		t.Fatal("chain must not be empty with zai credentials present")
	}
	for _, hop := range chain {
		if hop.Provider != "zai" {
			t.Fatalf("summariser chain crossed delivery allowlist: hop %+v", hop)
		}
	}
}

func TestSummariserChainLastResortIsZai(t *testing.T) {
	chain := deliverySummariserChain()
	last := chain[len(chain)-1]
	if last.Provider != "zai" {
		t.Fatalf("last-resort hop must remain within the zai allowlist, got %+v", last)
	}
}

func TestSummariserChainNoCredentialsYieldsTypedUnavailability(t *testing.T) {
	// Without Z.ai credentials and with ollama/anthropic models blanked
	// by the operator's global change, the resolved chain must not
	// silently include a live non-zai route: the ollama hop degenerates
	// to model "" (unusable), documenting the durable unavailable
	// outcome for compaction.
	cfg := &config.AppConfig{}
	cfg.Summariser.Provider = "zai"
	cfg.Summariser.OllamaModel = ""
	entries := cfg.SummariserChainEntriesScoped()
	for _, e := range entries {
		if e.Provider != "zai" && e.Model != "" {
			t.Fatalf("non-zai hop with a live model crossed the allowlist: %+v", e)
		}
	}
}
