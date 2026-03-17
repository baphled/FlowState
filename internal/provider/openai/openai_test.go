package openai_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider/openai"
)

var _ = Describe("OpenAI Provider", func() {
	Describe("New", func() {
		Context("when API key is empty", func() {
			It("returns an error", func() {
				p, err := openai.New("")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("API key is required"))
				Expect(p).To(BeNil())
			})
		})

		Context("when API key is provided", func() {
			It("returns a provider instance", func() {
				p, err := openai.New("test-api-key")
				Expect(err).NotTo(HaveOccurred())
				Expect(p).NotTo(BeNil())
			})
		})
	})

	Describe("Name", func() {
		It("returns openai", func() {
			p, err := openai.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Name()).To(Equal("openai"))
		})
	})

	Describe("Models", func() {
		It("returns a non-empty slice of models", func() {
			p, err := openai.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			models, err := p.Models()
			Expect(err).NotTo(HaveOccurred())
			Expect(models).NotTo(BeEmpty())
		})

		It("includes gpt-4o model", func() {
			p, err := openai.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			models, err := p.Models()
			Expect(err).NotTo(HaveOccurred())

			var modelIDs []string
			for _, m := range models {
				modelIDs = append(modelIDs, m.ID)
			}
			Expect(modelIDs).To(ContainElement("gpt-4o"))
		})

		It("sets provider to openai for all models", func() {
			p, err := openai.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			models, err := p.Models()
			Expect(err).NotTo(HaveOccurred())

			for _, m := range models {
				Expect(m.Provider).To(Equal("openai"))
			}
		})
	})
})
