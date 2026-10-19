package failover

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// R2-01 regression suite: SetDeliveryAllowlist must be a SINGLE final
// admission policy. Overrides (explicit model pins), strict heads and
// nested child overrides pass the same Z.ai-only filter as the base
// chain. Every test keeps a healthy forbidden provider available so
// only the policy — not health — prevents the crossing.

// dispatchRecorder records every downstream attempt the StreamHook
// makes.
type dispatchRecorder struct {
	calls []provider.ModelPreference
}

func (r *dispatchRecorder) handler(nextErr func(p provider.ModelPreference) error) hookHandler {
	return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		r.calls = append(r.calls, provider.ModelPreference{Provider: req.Provider, Model: req.Model})
		if nextErr != nil {
			if err := nextErr(provider.ModelPreference{Provider: req.Provider, Model: req.Model}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
}

type hookHandler = func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error)

// R2-01-a: EMPTY base + healthy forbidden override (openai/gpt-5) must
// produce ZERO downstream calls and an explicit unavailable/policy
// outcome under the installed default delivery policy.
func TestR201EmptyBaseForbiddenOverrideNeverDispatched(t *testing.T) {
	m := NewManager(nil, NewHealthManager(), time.Second)
	m.SetDeliveryAllowlist(DefaultDeliveryAllowlist())
	m.SetBasePreferences(nil) // empty base
	m.SetOverride(&provider.ModelPreference{Provider: "openai", Model: "gpt-5"})

	rec := &dispatchRecorder{}
	h := NewStreamHook(m, nil, "r2-01")
	wrapped := h.Execute(rec.handler(nil))
	_, err := wrapped(context.Background(), &provider.ChatRequest{})
	if err == nil {
		t.Fatal("forbidden override with empty base must yield an explicit error")
	}
	if !strings.Contains(err.Error(), "no healthy providers available") {
		t.Fatalf("expected explicit unavailable outcome, got: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("forbidden override was dispatched: %+v", rec.calls)
	}
}

// R2-01-b: a FORBIDDEN STRICT HEAD cannot bypass final admission —
// StrictChainWithHealthyFallbackDelivery drops it and the wrapped
// handler never receives it, even when the head is hard-down and
// healthy non-Z.ai global entries tempt a fallback.
func TestR201ForbiddenStrictHeadCannotBypassAdmission(t *testing.T) {
	hm := NewHealthManager()
	strict := []provider.ModelPreference{{Provider: "openai", Model: "gpt-5"}}
	global := []provider.ModelPreference{
		{Provider: "anthropic", Model: "claude-opus"},
		{Provider: "zai", Model: "glm-4-flash"},
	}
	hm.MarkPermanentFailure("openai", "gpt-5", provider.ErrorTypeBilling, "1113")

	chain := StrictChainWithHealthyFallbackDelivery(strict, global, hm, DefaultDeliveryAllowlist())
	for _, hop := range chain {
		if !DefaultDeliveryAllowlist().Allowed(hop.Provider, hop.Model) {
			t.Fatalf("forbidden strict head survived admission: %+v", chain)
		}
	}

	// Full boundary: manager over the resolved chain + recorder.
	m := NewManager(nil, hm, time.Second)
	m.SetDeliveryAllowlist(DefaultDeliveryAllowlist())
	m.SetBasePreferences(chain)
	rec := &dispatchRecorder{}
	h := NewStreamHook(m, nil, "r2-01")
	wrapped := h.Execute(rec.handler(nil))
	if _, err := wrapped(context.Background(), &provider.ChatRequest{}); err != nil {
		t.Fatalf("permitted quick-tier fallback should succeed: %v", err)
	}
	for _, c := range rec.calls {
		if c.Provider != "zai" {
			t.Fatalf("dispatch crossed policy: %+v", rec.calls)
		}
	}
}

// R2-01-c: NESTED CHILD OVERRIDE — a child-scoped manager (delivery
// allowlist installed, own base containing permitted pairs) that pins a
// forbidden override must not admit it; the child inherits the same
// single admission policy because the filter is applied at admission,
// covering the override regardless of nesting depth.
func TestR201NestedChildOverrideFilteredAtAdmission(t *testing.T) {
	hm := NewHealthManager()
	child := NewManager(nil, hm, time.Second)
	child.SetDeliveryAllowlist(DefaultDeliveryAllowlist())
	child.SetBasePreferences([]provider.ModelPreference{
		{Provider: "zai", Model: "glm-5.2"},
		{Provider: "openai", Model: "gpt-5"}, // healthy forbidden base entry probe
	})
	// Child pins a forbidden override — must be dropped at admission.
	child.SetOverride(&provider.ModelPreference{Provider: "anthropic", Model: "claude-opus"})

	got := child.Candidates()
	if len(got) != 1 || got[0].Provider != "zai" || got[0].Model != "glm-5.2" {
		t.Fatalf("child override/base crossed policy: %+v", got)
	}

	// Boundary: the recorder must never see the child's forbidden pin.
	rec := &dispatchRecorder{}
	h := NewStreamHook(child, nil, "r2-01-child")
	wrapped := h.Execute(rec.handler(nil))
	if _, err := wrapped(context.Background(), &provider.ChatRequest{}); err != nil {
		t.Fatalf("permitted zai candidate should have dispatched: %v", err)
	}
	if len(rec.calls) != 1 || rec.calls[0].Provider != "zai" {
		t.Fatalf("child dispatch crossed policy: %+v", rec.calls)
	}
}

// R2-01-d: PERMITTED Z.ai quick-tier fallback stays usable — the
// single policy must not delete intentional delivery capability: with
// the primary failing transiently the healthy quick tier is dispatched.
func TestR201PermittedQuickTierFallbackStillWorks(t *testing.T) {
	hm := NewHealthManager()
	m := NewManager(nil, hm, time.Second)
	m.SetDeliveryAllowlist(DefaultDeliveryAllowlist())
	m.SetBasePreferences([]provider.ModelPreference{
		{Provider: "zai", Model: "glm-5.2"},
		{Provider: "zai", Model: "glm-4-flash"},
	})
	hm.MarkRateLimited("zai", "glm-5.2", time.Now().Add(time.Hour))

	rec := &dispatchRecorder{}
	h := NewStreamHook(m, nil, "r2-01")
	wrapped := h.Execute(rec.handler(nil))
	if _, err := wrapped(context.Background(), &provider.ChatRequest{}); err != nil {
		t.Fatalf("quick-tier fallback must remain usable: %v", err)
	}
	if len(rec.calls) != 1 || rec.calls[0].Model != "glm-4-flash" {
		t.Fatalf("expected single glm-4-flash dispatch, got %+v", rec.calls)
	}
}
