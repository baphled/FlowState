package config_test

import (
	"testing"

	"github.com/baphled/flowstate/internal/config"
)

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

// TestSummariserChainScopedEmptyFailsClosed pins the M01 fail-closed
// fix: with the delivery scope installed and no zai credential, the
// scoped chain must be EMPTY (durable unavailable), never the legacy
// anthropic/ollama matrix — even when those providers have live
// models configured.
func TestSummariserChainScopedEmptyFailsClosed(t *testing.T) {
	config.SetSummariserAllowlist(config.DefaultSummariserDeliveryAllowlist())
	t.Cleanup(func() { config.SetSummariserAllowlist(nil) })
	cfg := &config.AppConfig{}
	cfg.Providers.Anthropic.Model = "claude-live"
	cfg.Providers.Ollama.Model = "llama3.2-live"
	entries := cfg.SummariserChainEntriesScoped()
	if len(entries) != 0 {
		t.Fatalf("scoped chain must be empty (fail closed), got %v", entries)
	}
}
