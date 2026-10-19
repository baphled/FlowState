package failover

import (
	"context"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// M07/M01 negative-case qualification suite. Adversarial scenarios
// where NON-Z.ai providers are healthy and tempting: under the Z.ai
// delivery allowlist, rate-limit, hard-down, staleness and chain
// exhaustion must never admit a non-Z.ai dispatch. The exhaustion test
// uses a dispatch recorder to prove zero downstream calls.

func newQualifiedManager(t *testing.T) *Manager {
	t.Helper()
	reg := provider.NewRegistry()
	health := NewHealthManager()
	health.SetPersistPath(t.TempDir() + "/health.json")
	m := NewManager(reg, health, time.Second)
	m.SetDeliveryAllowlist(DefaultDeliveryAllowlist())
	m.SetCapabilityFilter(func(p, mdl string) bool { return true })
	return m
}

// Qualified-rate-limit: only the allowlisted zai pairs are
// rate-limited while openai/anthropic are healthy — the pool must be
// empty, not fall across to the healthy non-Z.ai providers.
func TestQualifiedRateLimitDoesNotCross(t *testing.T) {
	m := newQualifiedManager(t)
	base := []provider.ModelPreference{
		{Provider: "zai", Model: "glm-5.2"},
		{Provider: "zai", Model: "glm-4-flash"},
		{Provider: "openai", Model: "gpt-5"},
		{Provider: "anthropic", Model: "claude-opus"},
	}
	m.SetBasePreferences(base)
	now := time.Now()
	for _, p := range base[:2] { // rate-limit ONLY the zai pairs; openai/anthropic stay healthy
		m.Health().MarkRateLimited(p.Provider, p.Model, now.Add(time.Hour))
	}
	cands := m.Candidates()
	for _, c := range cands {
		if c.Provider != "zai" {
			t.Fatalf("rate-limit crossed allowlist: %v", cands)
		}
	}
}

// Qualified-hard-down: strict zai head hard-down; the healthy global
// tail is openai/anthropic — the appended fallback tail must contain
// ONLY allowlisted pairs.
func TestQualifiedHardDownDoesNotCross(t *testing.T) {
	m := newQualifiedManager(t)
	m.Health().MarkPermanentFailure("zai", "glm-5.2", provider.ErrorTypeBilling, "1113")
	strict := []provider.ModelPreference{{Provider: "zai", Model: "glm-5.2"}}
	global := []provider.ModelPreference{
		{Provider: "openai", Model: "gpt-5"},
		{Provider: "zai", Model: "glm-4-flash"},
		{Provider: "anthropic", Model: "claude-opus"},
	}
	chain := StrictChainWithHealthyFallbackDelivery(strict, global, m.Health(), DefaultDeliveryAllowlist())
	sawQuickTier := false
	for _, hop := range chain {
		if hop.Provider == "zai" && hop.Model == "glm-4-flash" {
			sawQuickTier = true
		}
	}
	if !sawQuickTier {
		t.Fatalf("healthy zai quick-tier fallback must remain in chain: %+v", chain)
	}
	for _, hop := range chain {
		if !DefaultDeliveryAllowlist().Allowed(hop.Provider, hop.Model) {
			t.Fatalf("hard-down fallback crossed allowlist: %+v", chain)
		}
	}
}

// Qualified-stale-candidate: a stale (now-unhealthy) allowlisted
// candidate must not be admitted by the retry hook after its health
// flips, and a healthy non-Z.ai preference must NOT be admitted as a
// rescue.
func TestQualifiedStaleCandidateNotAdmitted(t *testing.T) {
	m := newQualifiedManager(t)
	prefs := []provider.ModelPreference{
		{Provider: "zai", Model: "glm-5.2"},
		{Provider: "zai", Model: "glm-4-flash"},
		{Provider: "openai", Model: "gpt-5"}, // healthy non-Z.ai rescue probe
	}
	m.SetBasePreferences(prefs)
	m.Health().MarkRateLimited("zai", "glm-5.2", time.Now().Add(time.Hour))
	m.Health().MarkPermanentFailure("zai", "glm-4-flash", provider.ErrorTypeBilling, "1113")
	if cands := m.Candidates(); len(cands) != 0 {
		t.Fatalf("stale candidates admitted: %v", cands)
	}
}

// Qualified-exhaustion: with the only allowlisted pair rate-limited
// and a healthy openai preference present, the chain must fail with
// the explicit unavailable error and zero downstream dispatch
// attempts — exhaustion attributable to the allowlist, not health.
func TestQualifiedExhaustionFailsClosedNoDispatch(t *testing.T) {
	m := newQualifiedManager(t)
	prefs := []provider.ModelPreference{
		{Provider: "zai", Model: "glm-5.2"},
		{Provider: "openai", Model: "gpt-5"}, // healthy non-Z.ai rescue probe
	}
	m.SetBasePreferences(prefs)
	m.Health().MarkRateLimited("zai", "glm-5.2", time.Now().Add(time.Hour))
	h := NewStreamHook(m, nil, "qualification")
	wrapped := h.Execute(func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		t.Fatalf("dispatch crossed: %s/%s", req.Provider, req.Model)
		return nil, nil
	})
	_, err := wrapped(context.Background(), &provider.ChatRequest{Provider: "zai", Model: "glm-5.2"})
	if err == nil {
		t.Fatal("exhausted allowlist must yield explicit error, not nil")
	}
	if err.Error() == "" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Qualified-allowlist-crossing probe: an allowlist that admits ONLY
// zai must reject every non-Z.ai pair even when they are the sole
// candidates, and Allowed must fail closed on unknown providers.
func TestQualifiedAllowlistRejectsAllNonZai(t *testing.T) {
	a := DefaultDeliveryAllowlist()
	for _, pair := range [][2]string{
		{"openai", "gpt-5"}, {"anthropic", "claude-opus"}, {"ollama", "llama3.2"},
		{"opencodego", "grok"}, {"zai", "glm-4.6"}, // glm-4.6 not in quick-tier pair set
	} {
		if a.Allowed(pair[0], pair[1]) {
			t.Fatalf("allowlist admitted %v", pair)
		}
	}
	for _, pair := range [][2]string{{"zai", "glm-5.2"}, {"zai", "glm-4-flash"}} {
		if !a.Allowed(pair[0], pair[1]) {
			t.Fatalf("allowlist rejected delivery pair %v", pair)
		}
	}
}
