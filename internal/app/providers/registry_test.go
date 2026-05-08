package providers_test

import (
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app/providers"
	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("providers.BuildWithFailures", func() {
	BeforeEach(func() {
		// Reset all provider env vars so each spec starts from a known
		// state — provider.Registry construction reads them eagerly and
		// a leak from another suite would silently change which
		// providers register.
		for _, k := range []string{
			"OPENAI_API_KEY",
			"ANTHROPIC_API_KEY",
			"GITHUB_TOKEN",
			"ZAI_API_KEY",
			"OPENZEN_API_KEY",
			"OLLAMA_CLOUD_API_KEY",
		} {
			os.Unsetenv(k)
		}
	})

	Context("when no API keys are provided", func() {
		It("returns a non-nil registry with OpenAI failure recorded", func() {
			cfg := config.DefaultConfig()
			cfg.Providers.OpenAI.APIKey = ""
			cfg.Providers.Anthropic.APIKey = ""

			registry, _, failures := providers.BuildWithFailures(cfg)

			Expect(registry).NotTo(BeNil())
			Expect(failures).To(HaveKey("openai"))
			Expect(errors.Is(failures["openai"], providers.ErrOpenAINoKey)).To(BeTrue())
		})
	})

	Context("when ANTHROPIC_API_KEY env var is set", func() {
		It("registers the anthropic provider", func() {
			os.Setenv("ANTHROPIC_API_KEY", "env-anthropic-key")
			DeferCleanup(func() { os.Unsetenv("ANTHROPIC_API_KEY") })

			cfg := config.DefaultConfig()

			registry, _, _ := providers.BuildWithFailures(cfg)

			Expect(registry).NotTo(BeNil())
			p, err := registry.Get("anthropic")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Context("when only the config supplies the Anthropic key", func() {
		It("registers the anthropic provider from config", func() {
			cfg := config.DefaultConfig()
			cfg.Providers.Anthropic.APIKey = "config-only-key"

			registry, _, _ := providers.BuildWithFailures(cfg)

			Expect(registry).NotTo(BeNil())
			p, err := registry.Get("anthropic")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})
})

var _ = Describe("providers.Build", func() {
	It("delegates to BuildWithFailures and discards the failures map", func() {
		cfg := config.DefaultConfig()

		registry, _ := providers.Build(cfg)

		Expect(registry).NotTo(BeNil())
	})
})

var _ = Describe("providers.ResolveProviderKey", func() {
	const envVar = "PROVIDERS_RESOLVE_TEST_KEY"

	BeforeEach(func() {
		os.Unsetenv(envVar)
	})

	It("prefers the environment variable when set", func() {
		os.Setenv(envVar, "from-env")
		DeferCleanup(func() { os.Unsetenv(envVar) })

		Expect(providers.ResolveProviderKey(envVar, "from-cfg")).To(Equal("from-env"))
	})

	It("falls back to the config value when env var is empty", func() {
		Expect(providers.ResolveProviderKey(envVar, "from-cfg")).To(Equal("from-cfg"))
	})

	It("returns the empty string when neither env nor config is set", func() {
		Expect(providers.ResolveProviderKey(envVar, "")).To(Equal(""))
	})
})

var _ = Describe("providers.BuildConfigPreferences", func() {
	It("orders the default provider first", func() {
		cfg := config.DefaultConfig()
		cfg.Providers.Default = "anthropic"
		cfg.Providers.Anthropic.Model = "claude-test"
		cfg.Providers.OpenAI.Model = "gpt-test"
		cfg.Providers.Ollama.Model = "ollama-test"

		prefs := providers.BuildConfigPreferences(cfg)

		Expect(prefs).NotTo(BeEmpty())
		Expect(prefs[0].Provider).To(Equal("anthropic"))
	})

	It("skips providers that have no model configured", func() {
		cfg := config.DefaultConfig()
		cfg.Providers.Default = "ollama"
		cfg.Providers.Anthropic.Model = ""
		cfg.Providers.OpenAI.Model = ""
		cfg.Providers.Ollama.Model = "qwen3"
		cfg.Providers.OllamaCloud.Model = ""
		cfg.Providers.GitHub.Model = ""
		cfg.Providers.ZAI.Model = ""
		cfg.Providers.OpenZen.Model = ""

		prefs := providers.BuildConfigPreferences(cfg)

		Expect(prefs).To(HaveLen(1))
		Expect(prefs[0].Provider).To(Equal("ollama"))
		Expect(prefs[0].Model).To(Equal("qwen3"))
	})

	It("includes zai, copilot, and openzen when configured with models", func() {
		cfg := &config.AppConfig{
			Providers: config.ProvidersConfig{
				Default: "zai",
				Ollama:  config.ProviderConfig{Model: "llama3.2"},
				ZAI:     config.ProviderConfig{Model: "glm-4.7"},
				GitHub:  config.ProviderConfig{Model: "gpt-4o"},
				OpenZen: config.ProviderConfig{Model: "qwen-coder"},
			},
		}

		prefs := providers.BuildConfigPreferences(cfg)

		providerNames := make([]string, 0, len(prefs))
		for _, p := range prefs {
			providerNames = append(providerNames, p.Provider)
		}
		Expect(providerNames).To(ContainElement("zai"))
		Expect(providerNames).To(ContainElement("openzen"))
	})

	It("places the zai default first when configured", func() {
		cfg := &config.AppConfig{
			Providers: config.ProvidersConfig{
				Default:   "zai",
				Ollama:    config.ProviderConfig{Model: "llama3.2"},
				Anthropic: config.ProviderConfig{Model: "claude-sonnet-4"},
				ZAI:       config.ProviderConfig{Model: "glm-4.7"},
			},
		}

		prefs := providers.BuildConfigPreferences(cfg)

		Expect(prefs).ToNot(BeEmpty())
		Expect(prefs[0].Provider).To(Equal("zai"))
		Expect(prefs[0].Model).To(Equal("glm-4.7"))
	})
})

var _ = Describe("providers.ResolveDefault", func() {
	Context("when the default provider is registered", func() {
		It("returns nil", func() {
			cfg := config.DefaultConfig()
			reg, _, failures := providers.BuildWithFailures(cfg)

			Expect(providers.ResolveDefault(reg, failures, "ollama")).To(Succeed())
		})
	})

	Context("when the default provider is missing", func() {
		It("returns an error wrapping the registered provider list and per-provider failure reasons", func() {
			cfg := config.DefaultConfig()
			cfg.Providers.Anthropic.APIKey = ""
			reg, _, failures := providers.BuildWithFailures(cfg)

			err := providers.ResolveDefault(reg, failures, "anthropic")

			Expect(err).To(HaveOccurred())
			// Anthropic is not registered when no key is supplied; the
			// diagnostic must include the lookup-failure substring and
			// the list of providers that did register (ollama is always
			// constructed from default config).
			Expect(err.Error()).To(ContainSubstring("anthropic"))
			Expect(err.Error()).To(ContainSubstring("registered:"))
		})
	})
})

var _ = Describe("providers.DescribeResolutionFailure", func() {
	It("formats failures in deterministic order", func() {
		failures := map[string]error{
			"anthropic": errors.New("anthropic boom"),
			"openai":    errors.New("openai boom"),
			"zai":       errors.New("zai boom"),
		}

		err := providers.DescribeResolutionFailure(
			"anthropic",
			[]string{"ollama"},
			failures,
			errors.New("provider not registered"),
		)

		Expect(err).To(HaveOccurred())
		// The "other failures" section sorts alphabetically; openai must
		// appear before zai so the message is stable in error logs.
		msg := err.Error()
		Expect(msg).To(ContainSubstring("anthropic failure: anthropic boom"))
		Expect(msg).To(ContainSubstring("other failures:"))
		openaiIdx := indexOf(msg, "openai: openai boom")
		zaiIdx := indexOf(msg, "zai: zai boom")
		Expect(openaiIdx).To(BeNumerically(">", -1))
		Expect(zaiIdx).To(BeNumerically(">", -1))
		Expect(openaiIdx).To(BeNumerically("<", zaiIdx))
	})
})

var _ = Describe("OpenCode credential migration", Label("opencode"), func() {
	var tempDir string
	var originalHome string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "providers-opencode-migration-*")
		Expect(err).NotTo(HaveOccurred())

		originalHome = os.Getenv("HOME")
		os.Setenv("HOME", tempDir)

		// Reset auth env so the OpenCode WARN-trigger condition (every
		// authenticated provider failing) is reachable.
		os.Unsetenv("ANTHROPIC_API_KEY")
		os.Unsetenv("GITHUB_TOKEN")
		os.Unsetenv("ZAI_API_KEY")
		os.Unsetenv("OPENZEN_API_KEY")
	})

	AfterEach(func() {
		os.Setenv("HOME", originalHome)
		os.RemoveAll(tempDir)
	})

	Context("when OpenCode auth.json is present but no FlowState provider authenticates", func() {
		It("does not register any provider from OpenCode auth.json", func() {
			opencodePath := filepath.Join(tempDir, ".local", "share", "opencode")
			Expect(os.MkdirAll(opencodePath, 0o755)).To(Succeed())
			authPath := filepath.Join(opencodePath, "auth.json")
			Expect(os.WriteFile(authPath, []byte(`{"anthropic":{"type":"oauth","access":"sk-ant-oat01-test"}}`), 0o600)).To(Succeed())

			cfg := config.DefaultConfig()
			cfg.Providers.Anthropic.APIKey = ""

			registry, _, _ := providers.BuildWithFailures(cfg)

			Expect(registry).NotTo(BeNil())
			_, err := registry.Get("anthropic")
			Expect(err).To(HaveOccurred())
		})
	})
})

// indexOf returns the byte index of the first occurrence of substr in s,
// or -1 when not found. Inlined here to avoid pulling in strings.Index
// just for ordering assertions.
func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
