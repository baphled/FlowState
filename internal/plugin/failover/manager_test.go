package failover_test

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
)

var errMockNotImplemented = errors.New("not implemented")

var _ = Describe("Manager", func() {
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

	Describe("NewManager", func() {
		It("returns a valid Manager with empty preferences", func() {
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

		It("returns empty when no preferences are set", func() {
			Expect(mgr.Candidates()).To(BeEmpty())
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
