package anthropic_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
)

var _ = Describe("Anthropic Provider", func() {
	Describe("New", func() {
		Context("when API key is empty", func() {
			It("returns an error", func() {
				p, err := anthropic.New("")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("API key is required"))
				Expect(p).To(BeNil())
			})
		})

		Context("when API key is provided", func() {
			It("returns a provider instance", func() {
				p, err := anthropic.New("test-api-key")
				Expect(err).NotTo(HaveOccurred())
				Expect(p).NotTo(BeNil())
			})
		})
	})

	Describe("Name", func() {
		It("returns anthropic", func() {
			p, err := anthropic.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Name()).To(Equal("anthropic"))
		})
	})

	Describe("Embed", func() {
		It("returns ErrNotSupported", func() {
			p, err := anthropic.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			ctx := context.Background()
			_, embedErr := p.Embed(ctx, provider.EmbedRequest{
				Input: "test input",
				Model: "test-model",
			})
			Expect(embedErr).To(MatchError(anthropic.ErrNotSupported))
		})
	})

	Describe("Models", func() {
		It("returns a non-empty slice of models", func() {
			p, err := anthropic.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			models, err := p.Models()
			Expect(err).NotTo(HaveOccurred())
			Expect(models).NotTo(BeEmpty())
		})

		It("sets provider to anthropic for all models", func() {
			p, err := anthropic.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			models, err := p.Models()
			Expect(err).NotTo(HaveOccurred())

			for _, m := range models {
				Expect(m.Provider).To(Equal("anthropic"))
			}
		})
	})
})
