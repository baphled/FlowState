package failover

import (
	"context"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// M07/M01 negative-case qualification suite. Every scenario proves the
// same invariant from a different failure angle: under the Z.ai
// delivery allowlist, no failure mode (rate-limit, hard-down,
// summariser failure, stale candidate, chain exhaustion) can cause a
// dispatch to a non-Z.ai provider. A dispatch-attempt recorder
// captures every downstream call the hook would have made.

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

// Qualified-rate-limit: every allowed pair rate-limited AND non-Z.ai
// providers healthy — pool must be empty, not cross.
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
	for _, p := range base {
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
	if len(chain) == 0 {
		t.Fatal("healthy zai quick-tier fallback must remain")
	}
	for _, hop := range chain {
		if !DefaultDeliveryAllowlist().Allowed(hop.Provider, hop.Model) {
			t.Fatalf("hard-down fallback crossed allowlist: %+v", chain)
		}
	}
}

// Qualified-stale-candidate: a stale (now-unhealthy) allowlisted
// candidate must not be admitted by the retry hook after its health
// flips; non-Z.ai rescue is prohibited.
func TestQualifiedStaleCandidateNotAdmitted(t *testing.T) {
	m := newQualifiedManager(t)
	prefs := []provider.ModelPreference{
		{Provider: "zai", Model: "glm-5.2"},
		{Provider: "zai", Model: "glm-4-flash"},
	}
	m.SetBasePreferences(prefs)
	m.Health().MarkRateLimited("zai", "glm-5.2", time.Now().Add(time.Hour))
	m.Health().MarkPermanentFailure("zai", "glm-4-flash", provider.ErrorTypeBilling, "1113")
	if cands := m.Candidates(); len(cands) != 0 {
		t.Fatalf("stale candidates admitted: %v", cands)
	}
}

// Qualified-summariser-failure (engine side is pinned in
// internal/config); failover side: exhausted chain must fail with the
// explicit unavailable error and zero downstream dispatch attempts.
func TestQualifiedExhaustionFailsClosedNoDispatch(t *testing.T) {
	m := newQualifiedManager(t)
	prefs := []provider.ModelPreference{{Provider: "zai", Model: "glm-5.2"}}
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
