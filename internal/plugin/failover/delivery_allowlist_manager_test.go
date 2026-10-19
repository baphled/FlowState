package failover

import (
	"context"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// Manager admission tests (M01): the delivery allowlist, once installed
// on the Manager, must gate the candidate pool that the retry/stream
// failover hooks and the engine's mid-stream failover read via
// Candidates() — synthetic non-zai global entries must never be admitted
// when the allowlist is set, and removing every allowed pair (e.g. both
// zai models rate-limited) must yield an EMPTY candidate pool (the
// explicit durable "no healthy providers available" outcome), not a
// crossing onto ollama/opencode-go.

func newAllowlistManager(base []provider.ModelPreference, allow *DeliveryAllowlist) (*Manager, *HealthManager) {
	hm := NewHealthManager()
	m := NewManager(nil, hm, time.Second)
	m.SetBasePreferences(base)
	m.SetDeliveryAllowlist(allow)
	return m, hm
}

// TestManagerCandidatesExcludedNonAllowlisted — rate-limit on the zai
// head: candidate pool must not admit ollama/opencode-go from the base
// chain; glm-4-flash remains.
func TestManagerCandidatesExcludedNonAllowlisted(t *testing.T) {
	base := []provider.ModelPreference{
		strictPair("zai", "glm-5.2"),
		strictPair("ollama", "llama3.2"),
		strictPair("opencode-go", "qwen3-coder"),
		strictPair("zai", "glm-4-flash"),
	}
	m, hm := newAllowlistManager(base, DefaultDeliveryAllowlist())
	hm.MarkRateLimited("zai", "glm-5.2", time.Now().Add(time.Hour))

	got := m.Candidates()
	if len(got) != 1 {
		t.Fatalf("expected only glm-4-flash candidate, got %d: %+v", len(got), got)
	}
	if got[0].Provider != "zai" || got[0].Model != "glm-4-flash" {
		t.Fatalf("expected zai/glm-4-flash, got %+v", got[0])
	}
}

// TestManagerCandidatesEmptyWhenAllAllowedRateLimited — both allowlisted
// pairs rate-limited: Candidates() must be EMPTY (explicit unavailable),
// never falling back to the non-allowlisted base tail. This is the
// fail-closed negative: no crossing under exhaustion.
func TestManagerCandidatesEmptyWhenAllAllowedRateLimited(t *testing.T) {
	base := []provider.ModelPreference{
		strictPair("zai", "glm-5.2"),
		strictPair("zai", "glm-4-flash"),
		strictPair("ollama", "llama3.2"),
	}
	m, hm := newAllowlistManager(base, DefaultDeliveryAllowlist())
	hm.MarkRateLimited("zai", "glm-5.2", time.Now().Add(time.Hour))
	hm.MarkRateLimited("zai", "glm-4-flash", time.Now().Add(time.Hour))

	if got := m.Candidates(); len(got) != 0 {
		t.Fatalf("expected empty candidate pool (no crossing), got %+v", got)
	}
}

// TestManagerCandidatesEmptyGuardDoesNotRescueNonAllowlisted — the
// capability-filter's empty-guard ("if filtering empties the list,
// return the unfiltered base") must NOT rescue non-allowlisted pairs
// once a delivery allowlist is installed. Fail closed, not open.
func TestManagerCandidatesEmptyGuardDoesNotRescueNonAllowlisted(t *testing.T) {
	base := []provider.ModelPreference{
		strictPair("ollama", "llama3.2"),
		strictPair("opencode-go", "qwen3-coder"),
	}
	m, hm := newAllowlistManager(base, DefaultDeliveryAllowlist())
	_ = hm

	if got := m.Candidates(); len(got) != 0 {
		t.Fatalf("non-allowlisted base must yield empty candidates, got %+v", got)
	}
}

// TestManagerCandidatesNilAllowlistLegacy — nil allowlist preserves the
// legacy unfiltered pool (product parity for non-delivery callers).
func TestManagerCandidatesNilAllowlistLegacy(t *testing.T) {
	base := []provider.ModelPreference{strictPair("ollama", "llama3.2")}
	m, _ := newAllowlistManager(base, nil)
	if got := m.Candidates(); len(got) != 1 {
		t.Fatalf("nil allowlist must preserve legacy pool, got %+v", got)
	}
}

// TestStreamHookExecuteFailsClosedWhenAllowlistExhausted — the retry
// surface: with the allowlist installed and every allowed pair
// rate-limited, StreamHook.Execute must return the explicit
// "no healthy providers available" error rather than dispatching a
// non-allowlisted provider.
func TestStreamHookExecuteFailsClosedWhenAllowlistExhausted(t *testing.T) {
	base := []provider.ModelPreference{
		strictPair("zai", "glm-5.2"),
		strictPair("zai", "glm-4-flash"),
		strictPair("ollama", "llama3.2"),
	}
	m, hm := newAllowlistManager(base, DefaultDeliveryAllowlist())
	hm.MarkRateLimited("zai", "glm-5.2", time.Now().Add(time.Hour))
	hm.MarkRateLimited("zai", "glm-4-flash", time.Now().Add(time.Hour))

	hook := NewStreamHook(m, nil, "test-agent")
	hook.SetRetryBackoff(RetryBackoffConfig{MaxRounds: 0})
	called := false
	handler := hook.Execute(func(context.Context, *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		called = true
		return nil, nil
	})
	_, err := handler(context.Background(), &provider.ChatRequest{Provider: "zai", Model: "glm-5.2"})
	if err == nil {
		t.Fatal("expected explicit unavailable error when allowlist exhausted")
	}
	if called {
		t.Fatal("downstream handler must not be invoked with a non-allowlisted provider")
	}
}
