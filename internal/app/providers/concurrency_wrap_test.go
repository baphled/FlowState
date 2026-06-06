package providers_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app/providers"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("providers.BuildWithFailures concurrency limiting", func() {
	BeforeEach(func() {
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

	Context("when a provider has an effective max-concurrent > 0", func() {
		It("wraps the registered zai provider in a ConcurrencyLimitedProvider by default", func() {
			cfg := config.DefaultConfig()
			cfg.Providers.ZAI.APIKey = "test-zai-key"

			registry, _, _ := providers.BuildWithFailures(cfg)

			p, err := registry.Get("zai")
			Expect(err).NotTo(HaveOccurred())
			_, ok := p.(*provider.ConcurrencyLimitedProvider)
			Expect(ok).To(BeTrue(),
				"zai should be wrapped because its effective max-concurrent default is 2")
		})
	})

	Context("when a provider has max-concurrent of 0 (unlimited)", func() {
		It("does not wrap the registered anthropic provider", func() {
			cfg := config.DefaultConfig()
			cfg.Providers.Anthropic.APIKey = "test-anthropic-key"

			registry, _, _ := providers.BuildWithFailures(cfg)

			p, err := registry.Get("anthropic")
			Expect(err).NotTo(HaveOccurred())
			_, ok := p.(*provider.ConcurrencyLimitedProvider)
			Expect(ok).To(BeFalse(),
				"anthropic has no configured cap, so it should pass through unwrapped")
		})
	})

	Context("when zai max-concurrent is explicitly configured", func() {
		It("still wraps zai when the explicit value is > 0", func() {
			cfg := config.DefaultConfig()
			cfg.Providers.ZAI.APIKey = "test-zai-key"
			cfg.Providers.ZAI.MaxConcurrentRequests = 3

			registry, _, _ := providers.BuildWithFailures(cfg)

			p, err := registry.Get("zai")
			Expect(err).NotTo(HaveOccurred())
			_, ok := p.(*provider.ConcurrencyLimitedProvider)
			Expect(ok).To(BeTrue())
		})
	})
})
