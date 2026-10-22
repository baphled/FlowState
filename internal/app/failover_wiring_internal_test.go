package app

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("resolveFailoverTiers", func() {
	It("returns config tiers when non-empty", func() {
		configTiers := map[string]string{
			"anthropic": failover.Tier0,
			"openai":    failover.Tier1,
		}

		result := resolveFailoverTiers(configTiers)

		Expect(result).To(Equal(configTiers))
	})

	It("returns default tiers when config is empty", func() {
		result := resolveFailoverTiers(map[string]string{})

		Expect(result).To(Equal(defaultFailoverTiers()))
	})

	It("returns default tiers when config is nil", func() {
		result := resolveFailoverTiers(nil)

		Expect(result).To(Equal(defaultFailoverTiers()))
	})
})

var _ = Describe("applyFailoverPreferences", func() {
	It("wires configured tiers into the failover manager", func() {
		mgr := failover.NewManager(provider.NewRegistry(), failover.NewHealthManager(), time.Second)
		cfg := &config.AppConfig{
			Providers: config.ProvidersConfig{},
			Plugins: config.PluginsConfig{
				Failover: config.FailoverConfig{
					Tiers: map[string]string{
						"claude-a": failover.Tier0,
						"gpt-4o":   failover.Tier1,
					},
				},
			},
		}

		applyFailoverPreferences(mgr, cfg)
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
	})
})

var _ = Describe("setupPluginRuntime", func() {
	It("loads persisted provider health before building failover candidates", func() {
		tempDir := GinkgoT().TempDir()
		cacheHome := filepath.Join(tempDir, "cache")
		Expect(os.MkdirAll(cacheHome, 0o755)).To(Succeed())

		originalCacheHome, hadCacheHome := os.LookupEnv("XDG_CACHE_HOME")
		Expect(os.Setenv("XDG_CACHE_HOME", cacheHome)).To(Succeed())
		DeferCleanup(func() {
			if hadCacheHome {
				_ = os.Setenv("XDG_CACHE_HOME", originalCacheHome)
				return
			}
			_ = os.Unsetenv("XDG_CACHE_HOME")
		})

		seeded := failover.NewHealthManager()
		seeded.SetPersistPath(filepath.Join(cacheHome, "flowstate", "provider-health.json"))
		seeded.MarkRateLimited("openai", "gpt-4o", time.Now().Add(2*time.Hour))
		Expect(seeded.Flush()).To(Succeed())

		cfg := &config.AppConfig{
			Providers: config.ProvidersConfig{
				Default: "openai",
				Anthropic: config.ProviderConfig{
					APIKey: "anthropic-key",
					Model:  "claude-sonnet-4",
				},
				OpenAI: config.ProviderConfig{
					APIKey: "openai-key",
					Model:  "gpt-4o",
				},
			},
		}

		runtime := setupPluginRuntime(cfg)
		Expect(runtime).NotTo(BeNil())
		Expect(runtime.healthManager.IsRateLimited("openai", "gpt-4o")).To(BeTrue())

		registry := provider.NewRegistry()
		wireFailoverManager(runtime, registry)
		Expect(runtime.FailoverManager()).NotTo(BeNil())

		prefs := make([]provider.ModelPreference, 0, 2)
		for _, pref := range buildFailoverProviders(cfg) {
			prefs = append(prefs, provider.ModelPreference{Provider: pref.Provider, Model: pref.Model})
		}
		runtime.FailoverManager().SetBasePreferences(prefs)

		candidates := runtime.FailoverManager().Candidates()
		Expect(candidates).NotTo(ContainElement(provider.ModelPreference{Provider: "openai", Model: "gpt-4o"}))
		Expect(candidates).To(ContainElement(provider.ModelPreference{Provider: "anthropic", Model: "claude-sonnet-4"}))
	})
})
