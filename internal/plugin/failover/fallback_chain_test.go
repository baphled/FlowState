package failover_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"testing"

	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("FallbackChain", func() {
	var (
		fc     *failover.FallbackChain
		health *failover.HealthManager
		chain  []failover.ProviderModel
	)

	BeforeEach(func() {
		chain = []failover.ProviderModel{
			{Provider: "anthropic", Model: "claude-3"},
			{Provider: "github-copilot", Model: "claude-3"},
			{Provider: "openai", Model: "gpt-4"},
			{Provider: "ollama", Model: "llama3.2"},
		}
		fc = failover.NewFallbackChain(chain, nil)
		health = failover.NewHealthManager()
	})

	Context("tier ordering", func() {
		It("respects tier ordering when selecting next provider", func() {
			current := chain[0]
			next, err := fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[1]))

			current = chain[1]
			next, err = fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[2]))

			current = chain[2]
			next, err = fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[3]))
		})
	})

	Context("skipping rate-limited providers", func() {
		It("skips rate-limited providers and returns next healthy one", func() {
			current := chain[0]
			health.MarkRateLimited("github-copilot", "claude-3", time.Now().Add(1*time.Hour))
			next, err := fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[2]))
		})

		It("ranks healthy remaining providers by failure history", func() {
			current := chain[0]
			for range 3 {
				health.MarkRateLimited("github-copilot", "claude-3", time.Now().Add(1*time.Hour))
			}
			health.MarkRateLimited("github-copilot", "claude-3", time.Now().Add(-1*time.Minute))
			next, err := fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[2]))
		})
	})

	Context("with configurable tiers", func() {
		It("uses default tiers when nil is provided", func() {
			fc = failover.NewFallbackChain(chain, nil)
			current := chain[0]
			next, err := fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[1]))
		})

		It("accepts custom tier mappings", func() {
			customTiers := map[string]string{
				"anthropic":      failover.Tier0,
				"github-copilot": failover.Tier1,
				"openai":         failover.Tier2,
				"ollama":         failover.Tier3,
			}
			fc = failover.NewFallbackChain(chain, customTiers)
			current := chain[0]
			next, err := fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[1]))
		})

		It("uses defaults when empty map is provided", func() {
			fc = failover.NewFallbackChain(chain, map[string]string{})
			current := chain[0]
			next, err := fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[1]))
		})
	})

	Context("exhausted chain", func() {
		It("returns error when all providers are rate-limited", func() {
			current := chain[0]
			health.MarkRateLimited("github-copilot", "claude-3", time.Now().Add(1*time.Hour))
			health.MarkRateLimited("openai", "gpt-4", time.Now().Add(1*time.Hour))
			health.MarkRateLimited("ollama", "llama3.2", time.Now().Add(1*time.Hour))
			_, err := fc.NextHealthy(current, health)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("all providers rate-limited or exhausted"))
		})
	})
})

// strictPair builds a ModelPreference shorthand.
func strictPair(p, m string) provider.ModelPreference {
	return provider.ModelPreference{Provider: p, Model: m}
}

// NewHealthManager-for-test shim: hard-down + rate-limit markers.
func newStrictTestHealth() *failover.HealthManager {
	return failover.NewHealthManager()
}

// TestStrictFallbackAllowlistedTailOnly — M01 negative: when the pinned
// zai pair is hard-down and the appended GLOBAL chain contains synthetic
// non-zai entries (ollama/opencode-go), the fallback tail must contain
// ONLY allowlisted pairs.
func TestStrictFallbackAllowlistedTailOnly(t *testing.T) {
	hm := newStrictTestHealth()
	hm.MarkHardDown("zai", "glm-5.2", "test: billing")

	strict := []provider.ModelPreference{strictPair("zai", "glm-5.2")}
	global := []provider.ModelPreference{
		strictPair("ollama", "llama3.2"),
		strictPair("opencode-go", "qwen3-coder"),
		strictPair("zai", "glm-4-flash"),
	}

	got := failover.StrictChainWithHealthyFallbackDelivery(strict, global, hm, failover.DefaultDeliveryAllowlist())

	if len(got) != 2 {
		t.Fatalf("expected strict head + 1 allowlisted fallback, got %d: %+v", len(got), got)
	}
	if got[0].Provider != "zai" || got[0].Model != "glm-5.2" {
		t.Fatalf("strict head must be retained, got %+v", got[0])
	}
	if got[1].Provider != "zai" || got[1].Model != "glm-4-flash" {
		t.Fatalf("fallback tail must be allowlisted zai pair, got %+v", got[1])
	}
	for _, p := range got[1:] {
		if p.Provider == "ollama" || p.Provider == "opencode-go" {
			t.Fatalf("non-allowlisted provider %q leaked into fallback tail", p.Provider)
		}
	}
}

// TestStrictFallbackNoAllowlistedRoute — M01 negative: hard-down pinned
// zai pair with only non-zai healthy globals — the resolved chain must
// NOT admit any non-zai entry (explicit unavailable outcome is the
// caller's contract: empty allowlisted tail means the turn fails loudly
// rather than crossing providers).
func TestStrictFallbackNoAllowlistedRoute(t *testing.T) {
	hm := newStrictTestHealth()
	hm.MarkHardDown("zai", "glm-5.2", "test: billing")

	strict := []provider.ModelPreference{strictPair("zai", "glm-5.2")}
	global := []provider.ModelPreference{
		strictPair("ollama", "llama3.2"),
		strictPair("opencode-go", "qwen3-coder"),
	}

	got := failover.StrictChainWithHealthyFallbackDelivery(strict, global, hm, failover.DefaultDeliveryAllowlist())
	if len(got) != 1 {
		t.Fatalf("expected strict head only (no crossing), got %d: %+v", len(got), got)
	}
}

// TestStrictFallbackRateLimitedAllowlistedPairExcluded — M01 negative:
// glm-5.2 rate-limited in the tail: only glm-4-flash appended.
func TestStrictFallbackRateLimitedAllowlistedPairExcluded(t *testing.T) {
	hm := newStrictTestHealth()
	hm.MarkHardDown("zai", "glm-5.2", "test: billing")
	hm.MarkRateLimited("zai", "glm-5.2", time.Now().Add(time.Hour))

	strict := []provider.ModelPreference{strictPair("zai", "glm-5.2")}
	global := []provider.ModelPreference{
		strictPair("zai", "glm-5.2"),
		strictPair("zai", "glm-4-flash"),
		strictPair("ollama", "llama3.2"),
	}

	got := failover.StrictChainWithHealthyFallbackDelivery(strict, global, hm, failover.DefaultDeliveryAllowlist())
	if len(got) != 2 || got[1].Model != "glm-4-flash" {
		t.Fatalf("expected head + glm-4-flash only, got %+v", got)
	}
}

// TestStrictFallbackNilAllowlistPreservesLegacy — nil allowlist keeps
// the pre-change global-tail behaviour (legacy callers unaffected).
func TestStrictFallbackNilAllowlistPreservesLegacy(t *testing.T) {
	hm := newStrictTestHealth()
	hm.MarkHardDown("zai", "glm-5.2", "test: billing")

	strict := []provider.ModelPreference{strictPair("zai", "glm-5.2")}
	global := []provider.ModelPreference{strictPair("ollama", "llama3.2")}

	got := failover.StrictChainWithHealthyFallbackDelivery(strict, global, hm, nil)
	if len(got) != 2 {
		t.Fatalf("nil allowlist must preserve legacy tail, got %d: %+v", len(got), got)
	}
}
