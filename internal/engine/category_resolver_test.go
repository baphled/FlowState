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
	})
})
