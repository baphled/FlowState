package failover_test

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"testing"

	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
)

var errMockNotImplemented = errors.New("not implemented")

var _ = Describe("failover.Manager", func() {
	var (
		registry *provider.Registry
		health   *failover.HealthManager
		mgr      *failover.Manager
		timeout  time.Duration
	)

	BeforeEach(func() {
		registry = provider.NewRegistry()
		health = failover.NewHealthManager()
		timeout = 30 * time.Second
		mgr = failover.NewManager(registry, health, timeout)
	})

	Describe("failover.NewManager", func() {
		It("returns a valid failover.Manager with empty preferences", func() {
			Expect(mgr).NotTo(BeNil())
			Expect(mgr.Preferences()).To(BeEmpty())
		})
	})

	Describe("SetBasePreferences", func() {
		It("stores and returns correct preferences", func() {
			prefs := []provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
				{Provider: "ollama", Model: "llama3.2"},
			}
			mgr.SetBasePreferences(prefs)
			Expect(mgr.Preferences()).To(Equal(prefs))
		})

		It("replaces previous base preferences", func() {
			mgr.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
			})
			newPrefs := []provider.ModelPreference{
				{Provider: "openai", Model: "gpt-4"},
			}
			mgr.SetBasePreferences(newPrefs)
			Expect(mgr.Preferences()).To(Equal(newPrefs))
		})
	})

	Describe("SetOverride", func() {
		It("prepends override to base preferences", func() {
			base := []provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
				{Provider: "ollama", Model: "llama3.2"},
			}
			mgr.SetBasePreferences(base)

			override := provider.ModelPreference{Provider: "openai", Model: "gpt-4"}
			mgr.SetOverride(override)

			expected := []provider.ModelPreference{
				{Provider: "openai", Model: "gpt-4"},
				{Provider: "anthropic", Model: "claude-3"},
				{Provider: "ollama", Model: "llama3.2"},
			}
			Expect(mgr.Preferences()).To(Equal(expected))
		})

		It("replaces previous override when set again", func() {
			base := []provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
			}
			mgr.SetBasePreferences(base)

			mgr.SetOverride(provider.ModelPreference{Provider: "openai", Model: "gpt-4"})
			mgr.SetOverride(provider.ModelPreference{Provider: "ollama", Model: "llama3.2"})

			expected := []provider.ModelPreference{
				{Provider: "ollama", Model: "llama3.2"},
				{Provider: "anthropic", Model: "claude-3"},
			}
			Expect(mgr.Preferences()).To(Equal(expected))
		})
	})

	Describe("ClearOverride", func() {
		It("restores base preferences only", func() {
			base := []provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
				{Provider: "ollama", Model: "llama3.2"},
			}
			mgr.SetBasePreferences(base)
			mgr.SetOverride(provider.ModelPreference{Provider: "openai", Model: "gpt-4"})
			mgr.ClearOverride()

			Expect(mgr.Preferences()).To(Equal(base))
		})

		It("is safe to call when no override is set", func() {
			base := []provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
			}
			mgr.SetBasePreferences(base)
			mgr.ClearOverride()

			Expect(mgr.Preferences()).To(Equal(base))
		})
	})

	Describe("Candidates", func() {
		It("returns all preferences when none are rate-limited", func() {
			prefs := []provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
				{Provider: "ollama", Model: "llama3.2"},
			}
			mgr.SetBasePreferences(prefs)

			Expect(mgr.Candidates()).To(Equal(prefs))
		})

		It("filters rate-limited providers", func() {
			prefs := []provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
				{Provider: "openai", Model: "gpt-4"},
				{Provider: "ollama", Model: "llama3.2"},
			}
			mgr.SetBasePreferences(prefs)
			health.MarkRateLimited("anthropic", "claude-3", time.Now().Add(1*time.Hour))

			expected := []provider.ModelPreference{
				{Provider: "openai", Model: "gpt-4"},
				{Provider: "ollama", Model: "llama3.2"},
			}
			Expect(mgr.Candidates()).To(Equal(expected))
		})

		It("returns empty when all are rate-limited", func() {
			prefs := []provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
				{Provider: "openai", Model: "gpt-4"},
			}
			mgr.SetBasePreferences(prefs)
			health.MarkRateLimited("anthropic", "claude-3", time.Now().Add(1*time.Hour))
			health.MarkRateLimited("openai", "gpt-4", time.Now().Add(1*time.Hour))

			Expect(mgr.Candidates()).To(BeEmpty())
		})

		It("includes override and filters rate-limited from combined list", func() {
			base := []provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
				{Provider: "ollama", Model: "llama3.2"},
			}
			mgr.SetBasePreferences(base)
			mgr.SetOverride(provider.ModelPreference{Provider: "openai", Model: "gpt-4"})
			health.MarkRateLimited("openai", "gpt-4", time.Now().Add(1*time.Hour))

			expected := []provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
				{Provider: "ollama", Model: "llama3.2"},
			}
			Expect(mgr.Candidates()).To(Equal(expected))
		})

		It("ranks healthy candidates by failure history while excluding active cooldowns", func() {
			prefs := []provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
				{Provider: "openai", Model: "gpt-4"},
				{Provider: "zai", Model: "glm"},
			}
			mgr.SetBasePreferences(prefs)

			for range 3 {
				health.MarkRateLimited("anthropic", "claude-3", time.Now().Add(1*time.Hour))
			}
			health.MarkRateLimited("anthropic", "claude-3", time.Now().Add(-1*time.Minute))
			health.MarkRateLimited("zai", "glm", time.Now().Add(1*time.Hour))

			Expect(mgr.Candidates()).To(Equal([]provider.ModelPreference{
				{Provider: "openai", Model: "gpt-4"},
				{Provider: "anthropic", Model: "claude-3"},
			}))
		})

		It("keeps equal-score candidates in preference order", func() {
			prefs := []provider.ModelPreference{
				{Provider: "openai", Model: "gpt-4"},
				{Provider: "openzen", Model: "gpt-4"},
			}
			mgr.SetBasePreferences(prefs)

			Expect(mgr.Candidates()).To(Equal(prefs))
		})

		It("returns empty when no preferences are set", func() {
			Expect(mgr.Candidates()).To(BeEmpty())
		})
	})

	Describe("Equivalent provider affinity", func() {
		It("keeps the last successful same-tier candidate first while it remains healthy", func() {
			mgr.SetModelTiers(map[string]string{
				"claude-a":  failover.Tier0,
				"anthropic": failover.Tier1,
				"copilot":   failover.Tier0,
				"gpt-4o":    failover.Tier1,
			})
			mgr.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-a"},
				{Provider: "copilot", Model: "claude-a"},
				{Provider: "openai", Model: "gpt-4o"},
			})

			Expect(mgr.Candidates()).To(Equal([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-a"},
				{Provider: "copilot", Model: "claude-a"},
				{Provider: "openai", Model: "gpt-4o"},
			}))

			mgr.RecordAttempt("anthropic", "claude-a")

			Expect(mgr.Candidates()).To(Equal([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-a"},
				{Provider: "copilot", Model: "claude-a"},
				{Provider: "openai", Model: "gpt-4o"},
			}))

			mgr.SetLast("copilot", "claude-a")

			Expect(mgr.Candidates()).To(Equal([]provider.ModelPreference{
				{Provider: "copilot", Model: "claude-a"},
				{Provider: "anthropic", Model: "claude-a"},
				{Provider: "openai", Model: "gpt-4o"},
			}))
		})

		It("keeps a singleton tier unchanged", func() {
			mgr.SetModelTiers(map[string]string{
				"claude-a": failover.Tier0,
			})
			mgr.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-a"},
			})

			mgr.RecordAttempt("anthropic", "claude-a")

			Expect(mgr.Candidates()).To(Equal([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-a"},
			}))
		})
	})

	Describe("Candidates capability filtering", func() {
		// keepCapable models the engine's IsToolCapableModel bound to the
		// operator's allow/deny lists: a tool-incapable local model
		// (llama3.2) must be dropped from the auto-failover tail while a
		// capable cloud model (glm-5.1) is kept. The predicate is injected
		// from the layer above (app/engine) so the failover package keeps
		// no dependency on the engine's capability tables.
		keepCapable := func(_ string, model string) bool {
			return model != "llama3.2"
		}

		It("excludes a tool-incapable base candidate and keeps a capable one", func() {
			mgr.SetCapabilityFilter(keepCapable)
			mgr.SetBasePreferences([]provider.ModelPreference{
				{Provider: "zai", Model: "glm-5.1"},
				{Provider: "ollama", Model: "llama3.2"},
			})

			Expect(mgr.Candidates()).To(Equal([]provider.ModelPreference{
				{Provider: "zai", Model: "glm-5.1"},
			}))
		})

		It("falls back to the unfiltered base list when filtering leaves zero candidates", func() {
			mgr.SetCapabilityFilter(keepCapable)
			// Every base candidate is tool-incapable. Rather than strand the
			// caller with a hard nil ("no healthy providers available"), the
			// empty-guard returns the unfiltered list — a last-resort weak
			// model beats no model at all.
			base := []provider.ModelPreference{
				{Provider: "ollama", Model: "llama3.2"},
			}
			mgr.SetBasePreferences(base)

			Expect(mgr.Candidates()).To(Equal(base))
		})

		It("does not apply the capability filter to an explicit override", func() {
			mgr.SetCapabilityFilter(keepCapable)
			mgr.SetBasePreferences([]provider.ModelPreference{
				{Provider: "zai", Model: "glm-5.1"},
			})
			// The override is the user's explicit head choice. Even when it
			// is a tool-incapable model, a deliberate selection must be
			// honoured — only the auto-failover tail is capability-filtered.
			mgr.SetOverride(provider.ModelPreference{Provider: "ollama", Model: "llama3.2"})

			Expect(mgr.Candidates()).To(Equal([]provider.ModelPreference{
				{Provider: "ollama", Model: "llama3.2"},
				{Provider: "zai", Model: "glm-5.1"},
			}))
		})

		It("leaves candidates untouched when no capability filter is set", func() {
			prefs := []provider.ModelPreference{
				{Provider: "zai", Model: "glm-5.1"},
				{Provider: "ollama", Model: "llama3.2"},
			}
			mgr.SetBasePreferences(prefs)

			Expect(mgr.Candidates()).To(Equal(prefs))
		})
	})

	Describe("LastProvider and LastModel", func() {
		It("returns empty strings initially", func() {
			Expect(mgr.LastProvider()).To(BeEmpty())
			Expect(mgr.LastModel()).To(BeEmpty())
		})

		It("returns correct values after SetLast", func() {
			mgr.SetLast("anthropic", "claude-3")
			Expect(mgr.LastProvider()).To(Equal("anthropic"))
			Expect(mgr.LastModel()).To(Equal("claude-3"))
		})

		It("updates when SetLast is called again", func() {
			mgr.SetLast("anthropic", "claude-3")
			mgr.SetLast("openai", "gpt-4")
			Expect(mgr.LastProvider()).To(Equal("openai"))
			Expect(mgr.LastModel()).To(Equal("gpt-4"))
		})
	})

	Describe("ListModels", func() {
		It("delegates to registry", func() {
			mockProvider := &mockListProvider{
				name: "anthropic",
				models: []provider.Model{
					{ID: "claude-3", Provider: "anthropic", ContextLength: 200000},
				},
			}
			registry.Register(mockProvider)

			models, err := mgr.ListModels()
			Expect(err).NotTo(HaveOccurred())
			Expect(models).To(HaveLen(1))
			Expect(models[0].ID).To(Equal("claude-3"))
		})

		It("aggregates models from multiple providers", func() {
			registry.Register(&mockListProvider{
				name: "anthropic",
				models: []provider.Model{
					{ID: "claude-3", Provider: "anthropic", ContextLength: 200000},
				},
			})
			registry.Register(&mockListProvider{
				name: "ollama",
				models: []provider.Model{
					{ID: "llama3.2", Provider: "ollama", ContextLength: 128000},
				},
			})

			models, err := mgr.ListModels()
			Expect(err).NotTo(HaveOccurred())
			Expect(models).To(HaveLen(2))
		})

		It("returns error when no models are available", func() {
			_, err := mgr.ListModels()
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("StreamTimeout", func() {
		It("returns configured value", func() {
			Expect(mgr.StreamTimeout()).To(Equal(30 * time.Second))
		})

		It("returns the timeout set at construction", func() {
			customMgr := failover.NewManager(registry, health, 90*time.Second)
			Expect(customMgr.StreamTimeout()).To(Equal(90 * time.Second))
		})
	})

	Describe("thread safety", func() {
		It("handles concurrent SetBasePreferences and Candidates without race", func() {
			var wg sync.WaitGroup
			prefs := []provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
				{Provider: "ollama", Model: "llama3.2"},
			}

			for range 50 {
				wg.Add(2)
				go func() {
					defer wg.Done()
					mgr.SetBasePreferences(prefs)
				}()
				go func() {
					defer wg.Done()
					_ = mgr.Candidates()
				}()
			}
			wg.Wait()
		})

		It("handles concurrent SetLast and LastProvider without race", func() {
			var wg sync.WaitGroup

			for range 50 {
				wg.Add(2)
				go func() {
					defer wg.Done()
					mgr.SetLast("anthropic", "claude-3")
				}()
				go func() {
					defer wg.Done()
					_ = mgr.LastProvider()
				}()
			}
			wg.Wait()
		})

		It("handles concurrent SetOverride and ClearOverride without race", func() {
			var wg sync.WaitGroup
			mgr.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
			})

			for range 50 {
				wg.Add(2)
				go func() {
					defer wg.Done()
					mgr.SetOverride(provider.ModelPreference{Provider: "openai", Model: "gpt-4"})
				}()
				go func() {
					defer wg.Done()
					mgr.ClearOverride()
				}()
			}
			wg.Wait()
		})
	})

	Describe("ResolveContextLength", func() {
		const (
			defaultFallback = 16384
			operatorPin     = 32768
		)

		It("returns the model-supplied ContextLength when the provider knows it", func() {
			registry.Register(&mockListProvider{
				name: "anthropic",
				models: []provider.Model{
					{ID: "claude-sonnet-4-6", Provider: "anthropic", ContextLength: 200000},
				},
			})

			Expect(mgr.ResolveContextLength("anthropic", "claude-sonnet-4-6")).To(Equal(200000))
		})

		It("returns the default 16K fallback for unknown providers", func() {
			Expect(mgr.ResolveContextLength("missing", "any-model")).To(Equal(defaultFallback))
		})

		It("returns the default 16K fallback for unknown models on a known provider", func() {
			registry.Register(&mockListProvider{
				name:   "anthropic",
				models: []provider.Model{{ID: "claude-sonnet-4-6", Provider: "anthropic", ContextLength: 200000}},
			})

			Expect(mgr.ResolveContextLength("anthropic", "claude-opus-99")).To(Equal(defaultFallback))
		})

		It("returns the operator-pinned fallback after SetContextFallback", func() {
			mgr.SetContextFallback(operatorPin)

			Expect(mgr.ResolveContextLength("missing", "any-model")).To(Equal(operatorPin))
		})

		It("ignores a non-positive fallback override", func() {
			mgr.SetContextFallback(0)
			mgr.SetContextFallback(-5)

			Expect(mgr.ResolveContextLength("missing", "any-model")).To(Equal(defaultFallback))
		})
	})
})

type mockListProvider struct {
	name   string
	models []provider.Model
}

func (m *mockListProvider) Name() string { return m.name }

func (m *mockListProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errMockNotImplemented
}

func (m *mockListProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errMockNotImplemented
}

func (m *mockListProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, errMockNotImplemented
}

func (m *mockListProvider) Models() ([]provider.Model, error) {
	return m.models, nil
}

// failover.Manager admission tests (M01): the delivery allowlist, once installed
// on the failover.Manager, must gate the candidate pool that the retry/stream
// failover hooks and the engine's mid-stream failover read via
// Candidates() — synthetic non-zai global entries must never be admitted
// when the allowlist is set, and removing every allowed pair (e.g. both
// zai models rate-limited) must yield an EMPTY candidate pool (the
// explicit durable "no healthy providers available" outcome), not a
// crossing onto ollama/opencode-go.

func newAllowlistManager(base []provider.ModelPreference, allow *failover.DeliveryAllowlist) (*failover.Manager, *failover.HealthManager) {
	hm := failover.NewHealthManager()
	m := failover.NewManager(nil, hm, time.Second)
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
	m, hm := newAllowlistManager(base, failover.DefaultDeliveryAllowlist())
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
	m, hm := newAllowlistManager(base, failover.DefaultDeliveryAllowlist())
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
	m, hm := newAllowlistManager(base, failover.DefaultDeliveryAllowlist())
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
	m, hm := newAllowlistManager(base, failover.DefaultDeliveryAllowlist())
	hm.MarkRateLimited("zai", "glm-5.2", time.Now().Add(time.Hour))
	hm.MarkRateLimited("zai", "glm-4-flash", time.Now().Add(time.Hour))

	hook := failover.NewStreamHook(m, nil, "test-agent")
	hook.SetRetryBackoff(failover.RetryBackoffConfig{MaxRounds: 0})
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
