package failover_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/failover"
)

var _ = Describe("FallbackChain", func() {
	var (
		fc     *failover.FallbackChain
		health *failover.HealthManager
		chain  []failover.ProviderModel
	)

	BeforeEach(func() {
		chain = []failover.ProviderModel{
			{Provider: "anthropic", Model: "claude-3"},
			{Provider: "github-copilot", Model: "claude-3"},
			{Provider: "openai", Model: "gpt-4"},
			{Provider: "ollama", Model: "llama3.2"},
		}
		fc = failover.NewFallbackChain(chain, nil)
		health = failover.NewHealthManager()
	})

	Context("tier ordering", func() {
		It("respects tier ordering when selecting next provider", func() {
			current := chain[0]
			next, err := fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[1]))

			current = chain[1]
			next, err = fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[2]))

			current = chain[2]
			next, err = fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[3]))
		})
	})

	Context("skipping rate-limited providers", func() {
		It("skips rate-limited providers and returns next healthy one", func() {
			current := chain[0]
			health.MarkRateLimited("github-copilot", "claude-3", time.Now().Add(1*time.Hour))
			next, err := fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[2]))
		})
	})

	Context("with configurable tiers", func() {
		It("uses default tiers when nil is provided", func() {
			fc = failover.NewFallbackChain(chain, nil)
			current := chain[0]
			next, err := fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[1]))
		})

		It("accepts custom tier mappings", func() {
			customTiers := map[string]string{
				"anthropic":      failover.Tier0,
				"github-copilot": failover.Tier1,
				"openai":         failover.Tier2,
				"ollama":         failover.Tier3,
			}
			fc = failover.NewFallbackChain(chain, customTiers)
			current := chain[0]
			next, err := fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[1]))
		})

		It("uses defaults when empty map is provided", func() {
			fc = failover.NewFallbackChain(chain, map[string]string{})
			current := chain[0]
			next, err := fc.NextHealthy(current, health)
			Expect(err).NotTo(HaveOccurred())
			Expect(next).To(Equal(chain[1]))
		})
	})

	Context("exhausted chain", func() {
		It("returns error when all providers are rate-limited", func() {
			current := chain[0]
			health.MarkRateLimited("github-copilot", "claude-3", time.Now().Add(1*time.Hour))
			health.MarkRateLimited("openai", "gpt-4", time.Now().Add(1*time.Hour))
			health.MarkRateLimited("ollama", "llama3.2", time.Now().Add(1*time.Hour))
			_, err := fc.NextHealthy(current, health)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("all providers rate-limited or exhausted"))
		})
	})
})
