package engine

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("CategoryResolver with ModelLister", func() {
	Context("when ModelLister is configured", func() {
		It("resolves 'fast' descriptor to smallest context model", func() {
			models := []provider.Model{
				{ID: "big-model", Provider: "test", ContextLength: 128000},
				{ID: "small-model", Provider: "test", ContextLength: 4096},
			}
			resolver := NewCategoryResolver(nil).
				WithModelLister(func() ([]provider.Model, error) { return models, nil })
			cfg, err := resolver.Resolve("quick")
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("small-model"))
		})

		It("resolves 'reasoning' descriptor to largest context model", func() {
			models := []provider.Model{
				{ID: "haiku", Provider: "anthropic", ContextLength: 8000},
				{ID: "opus", Provider: "anthropic", ContextLength: 200000},
			}
			resolver := NewCategoryResolver(nil).
				WithModelLister(func() ([]provider.Model, error) { return models, nil })
			cfg, err := resolver.Resolve("deep")
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("opus"))
		})

		It("resolves 'vision' descriptor to first model", func() {
			models := []provider.Model{
				{ID: "first-model", Provider: "test", ContextLength: 10000},
				{ID: "second-model", Provider: "test", ContextLength: 20000},
			}
			resolver := NewCategoryResolver(nil).
				WithModelLister(func() ([]provider.Model, error) { return models, nil })
			cfg, err := resolver.Resolve("visual-engineering")
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("first-model"))
		})

		It("resolves 'balanced' descriptor to median context model", func() {
			overrides := map[string]CategoryConfig{
				"test-balanced": {Model: "balanced"},
			}
			models := []provider.Model{
				{ID: "small", Provider: "test", ContextLength: 4096},
				{ID: "medium", Provider: "test", ContextLength: 32000},
				{ID: "large", Provider: "test", ContextLength: 128000},
			}
			resolver := NewCategoryResolver(overrides).
				WithModelLister(func() ([]provider.Model, error) { return models, nil })
			cfg, err := resolver.Resolve("test-balanced")
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("medium"))
		})

		It("falls back to descriptor when lister returns error", func() {
			resolver := NewCategoryResolver(nil).
				WithModelLister(func() ([]provider.Model, error) {
					return nil, errors.New("provider unavailable")
				})
			cfg, err := resolver.Resolve("quick")
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("fast"))
		})

		It("falls back to descriptor when lister returns empty list", func() {
			resolver := NewCategoryResolver(nil).
				WithModelLister(func() ([]provider.Model, error) {
					return []provider.Model{}, nil
				})
			cfg, err := resolver.Resolve("quick")
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("fast"))
		})

		It("does not override explicit model name set in config override", func() {
			overrides := map[string]CategoryConfig{
				"quick": {Model: "claude-3-5-haiku-latest"},
			}
			models := []provider.Model{
				{ID: "some-other-model", Provider: "test", ContextLength: 4096},
			}
			resolver := NewCategoryResolver(overrides).
				WithModelLister(func() ([]provider.Model, error) { return models, nil })
			cfg, err := resolver.Resolve("quick")
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("claude-3-5-haiku-latest"))
		})
	})

	Context("without ModelLister", func() {
		It("returns abstract descriptor model name unchanged", func() {
			resolver := NewCategoryResolver(nil)
			cfg, err := resolver.Resolve("quick")
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("fast"))
		})
	})
})
