package engine_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
)

var _ = Describe("CategoryResolver", func() {
	Describe("Resolve", func() {
		It("returns default config for a known category", func() {
			resolver := engine.NewCategoryResolver(nil)
			cfg, err := resolver.Resolve("quick")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).NotTo(BeEmpty())
		})

		It("returns an error for an unknown category", func() {
			resolver := engine.NewCategoryResolver(nil)
			_, err := resolver.Resolve("unknown-category")

			Expect(err).To(HaveOccurred())
		})

		It("resolves medium category to balanced descriptor", func() {
			resolver := engine.NewCategoryResolver(nil)
			cfg, err := resolver.Resolve("medium")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("balanced"))
			Expect(cfg.Temperature).To(Equal(0.5))
			Expect(cfg.MaxTokens).To(Equal(2048))
		})

		It("applies user config overrides", func() {
			overrides := map[string]engine.CategoryConfig{
				"quick": {Model: "gpt-4.1", Provider: "openai"},
			}
			resolver := engine.NewCategoryResolver(overrides)
			cfg, err := resolver.Resolve("quick")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("gpt-4.1"))
			Expect(cfg.Provider).To(Equal("openai"))
		})

		It("falls back to default when category not in overrides", func() {
			overrides := map[string]engine.CategoryConfig{
				"summarise": {Model: "gpt-3.5", Provider: "openai"},
			}
			resolver := engine.NewCategoryResolver(overrides)
			cfg, err := resolver.Resolve("quick")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).NotTo(BeEmpty())
		})

		It("resolves low category to fast descriptor", func() {
			resolver := engine.NewCategoryResolver(nil)
			cfg, err := resolver.Resolve("low")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("fast"))
			Expect(cfg.Temperature).To(Equal(0.2))
			Expect(cfg.MaxTokens).To(Equal(2048))
		})

		It("resolves standard category to balanced descriptor", func() {
			resolver := engine.NewCategoryResolver(nil)
			cfg, err := resolver.Resolve("standard")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("balanced"))
		})

		It("resolves deep category to reasoning descriptor", func() {
			resolver := engine.NewCategoryResolver(nil)
			cfg, err := resolver.Resolve("deep")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("reasoning"))
		})
	})
})
