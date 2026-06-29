package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("ProviderConfig.EffectiveMaxConcurrent", func() {
	Context("when MaxConcurrentRequests is explicitly set", func() {
		It("returns the configured value", func() {
			pc := config.ProviderConfig{MaxConcurrentRequests: 5}
			Expect(pc.EffectiveMaxConcurrent("zai")).To(Equal(5))
		})

		It("returns the configured value even for zai, overriding the default", func() {
			pc := config.ProviderConfig{MaxConcurrentRequests: 4}
			Expect(pc.EffectiveMaxConcurrent("zai")).To(Equal(4))
		})
	})

	Context("when MaxConcurrentRequests is unset (zero)", func() {
		It("returns the zai placeholder default of 1 for zai", func() {
			pc := config.ProviderConfig{}
			Expect(pc.EffectiveMaxConcurrent("zai")).To(Equal(config.DefaultZAIMaxConcurrent))
			Expect(pc.EffectiveMaxConcurrent("zai")).To(Equal(1))
		})

		It("returns 0 (unlimited) for any provider without a configured default", func() {
			pc := config.ProviderConfig{}
			Expect(pc.EffectiveMaxConcurrent("anthropic")).To(Equal(0))
			Expect(pc.EffectiveMaxConcurrent("openai")).To(Equal(0))
			Expect(pc.EffectiveMaxConcurrent("ollama")).To(Equal(0))
		})
	})
})
