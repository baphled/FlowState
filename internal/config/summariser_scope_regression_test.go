package config_test

import (
	"sync"
	"testing"

	"github.com/baphled/flowstate/internal/config"
)

// R2-05 regressions: summariser scope safety for the common session
// engine. The scope must be an immutable, pair-level, concurrency-safe
// policy that cannot be silently unscoped by an empty reset, and whose
// pair set matches the failover delivery allowlist across ALL call
// classes (roots, children, retries, compaction).

// R2-05-a: pair-level filtering — the legacy glm-4.7 default (and any
// other off-policy zai model) must NOT be admitted under delivery
// scope; provider-name-only admission was the defect.
func TestR205PairLevelFilteringRejectsOffPolicyModel(t *testing.T) {
	t.Cleanup(config.ClearSummariserDeliveryScope)
	config.SetSummariserAllowlist(config.DefaultSummariserDeliveryAllowlist())
	cfg := &config.AppConfig{}
	cfg.Providers.ZAI.APIKey = "k"
	// No summariser.model pinned: unscoped default resolves to glm-4.7.
	// Under delivery scope the glm-4.7 hop must be dropped -> empty
	// chain = durable ErrSummariserUnavailable, never an off-policy
	// compaction call and never a crossing to anthropic/ollama.
	entries := cfg.SummariserChainEntriesScoped()
	if len(entries) != 0 {
		t.Fatalf("glm-4.7 default must not survive the pair-scoped policy: %+v", entries)
	}
}

// R2-05-b: permitted pairs stay usable — glm-5.2 and glm-4-flash hops
// are admitted, quick tier included.
func TestR205PermittedPairsRemainUsable(t *testing.T) {
	t.Cleanup(config.ClearSummariserDeliveryScope)
	config.SetSummariserAllowlist(config.DefaultSummariserDeliveryAllowlist())
	cfg := &config.AppConfig{}
	cfg.Providers.ZAI.APIKey = "k"
	cfg.Summariser.Provider = "zai"
	cfg.Summariser.Model = "glm-5.2"
	entries := cfg.SummariserChainEntriesScoped()
	if len(entries) == 0 {
		t.Fatal("glm-5.2 hop must be admitted under delivery scope")
	}
	for _, e := range entries {
		if e.Provider != "zai" || (e.Model != "glm-5.2" && e.Model != "glm-4-flash") {
			t.Fatalf("off-policy hop admitted: %+v", entries)
		}
	}
}

// R2-05-c: an empty assignment can no longer silently UNSCOPE a
// delivery-scoped process (the reset hazard the review flagged: one
// session's reset restoring the unscoped anthropic/ollama matrix for
// every other session). Explicit ClearSummariserDeliveryScope is
// required.
func TestR205EmptyResetCannotUnscopeInstalledPolicy(t *testing.T) {
	t.Cleanup(config.ClearSummariserDeliveryScope)
	config.SetSummariserAllowlist(config.DefaultSummariserDeliveryAllowlist())
	config.SetSummariserAllowlist(config.NewSummariserAllowlist()) // empty reset attempt
	cfg := &config.AppConfig{}
	cfg.Providers.ZAI.APIKey = "k"
	// Unscoped this chain would include anthropic/ollama hops; the
	// ignored reset must keep the scope installed so the chain stays
	// zai-only (glm-4.7 default dropped).
	entries := cfg.SummariserChainEntriesScoped()
	for _, e := range entries {
		if e.Provider != "zai" {
			t.Fatalf("empty reset unscoped the policy: %+v", entries)
		}
	}
}

// R2-05-d: unscoped product mode is preserved — with no scope
// installed the legacy chain (including provider defaults) is emitted
// unchanged.
func TestR205UnscopedProductModePreserved(t *testing.T) {
	config.ClearSummariserDeliveryScope()
	t.Cleanup(config.ClearSummariserDeliveryScope)
	cfg := &config.AppConfig{}
	cfg.Providers.ZAI.APIKey = "k"
	cfg.Providers.Anthropic.Model = "claude-opus" // make a legacy hop visible
	entries := cfg.SummariserChainEntriesScoped()
	found := false
	for _, e := range entries {
		if e.Provider == "anthropic" && e.Model == "claude-opus" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unscoped mode must preserve the legacy chain: %+v", entries)
	}
}

// R2-05-e: isolation/race coverage — concurrent scope installs and
// chain resolutions must not race (mutex-guarded immutable snapshot)
// and must never observe a torn scope.
func TestR205ConcurrentScopeAccessIsolated(t *testing.T) {
	t.Cleanup(config.ClearSummariserDeliveryScope)
	cfg := &config.AppConfig{}
	cfg.Providers.ZAI.APIKey = "k"
	cfg.Summariser.Provider = "zai"
	cfg.Summariser.Model = "glm-5.2"
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			config.SetSummariserAllowlist(config.DefaultSummariserDeliveryAllowlist())
		}()
		go func() {
			defer wg.Done()
			for _, e := range cfg.SummariserChainEntriesScoped() {
				if e.Provider != "zai" {
					t.Errorf("torn scope observed: %+v", e)
					return
				}
			}
		}()
	}
	wg.Wait()
}
