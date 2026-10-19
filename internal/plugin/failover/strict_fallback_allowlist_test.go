package failover

import (
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// strictPair builds a ModelPreference shorthand.
func strictPair(p, m string) provider.ModelPreference {
	return provider.ModelPreference{Provider: p, Model: m}
}

// NewHealthManager-for-test shim: hard-down + rate-limit markers.
func newStrictTestHealth() *HealthManager {
	return NewHealthManager()
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

	got := StrictChainWithHealthyFallbackDelivery(strict, global, hm, DefaultDeliveryAllowlist())

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

	got := StrictChainWithHealthyFallbackDelivery(strict, global, hm, DefaultDeliveryAllowlist())
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

	got := StrictChainWithHealthyFallbackDelivery(strict, global, hm, DefaultDeliveryAllowlist())
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

	got := StrictChainWithHealthyFallbackDelivery(strict, global, hm, nil)
	if len(got) != 2 {
		t.Fatalf("nil allowlist must preserve legacy tail, got %d: %+v", len(got), got)
	}
}
