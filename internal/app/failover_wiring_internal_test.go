package app

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/failover"
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
