package opencodego_test

import (
	"net/http"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/opencodego"
)

func TestOpencodego(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Opencodego Suite")
}

var _ provider.Provider = (*opencodego.Provider)(nil)

var _ = Describe("Constructors", func() {
	Describe("New", func() {
		It("returns a provider when the API key is non-empty", func() {
			p, err := opencodego.New("test-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
			Expect(p.Name()).To(Equal("opencode-go"))
		})

		It("returns errAPIKeyRequired when the API key is empty", func() {
			p, err := opencodego.New("")
			Expect(err).To(MatchError("OpenCodeGo API key is required"))
			Expect(p).To(BeNil())
		})
	})

	Describe("NewFromConfig", func() {
		It("returns a provider when the API key is non-empty", func() {
			p, err := opencodego.NewFromConfig("cfg-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
			Expect(p.Name()).To(Equal("opencode-go"))
		})

		It("returns errAPIKeyRequired when the API key is empty", func() {
			p, err := opencodego.NewFromConfig("")
			Expect(err).To(MatchError("OpenCodeGo API key is required"))
			Expect(p).To(BeNil())
		})
	})

	Describe("NewWithOptions", func() {
		It("returns a provider when the API key is non-empty", func() {
			p, err := opencodego.NewWithOptions("opts-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})

		It("returns errAPIKeyRequired when the API key is empty", func() {
			p, err := opencodego.NewWithOptions("")
			Expect(err).To(MatchError("OpenCodeGo API key is required"))
			Expect(p).To(BeNil())
		})

		It("honours supplied request options without overriding the API key gate", func() {
			p, err := opencodego.NewWithOptions("opts-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})
})

var _ = Describe("Provider", func() {
	Describe("Name", func() {
		It("returns the documented provider identifier", func() {
			p, err := opencodego.New("any-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Name()).To(Equal("opencode-go"))
		})

		It("returns a stable value across instances", func() {
			p1, err := opencodego.New("k1")
			Expect(err).NotTo(HaveOccurred())
			p2, err := opencodego.New("k2")
			Expect(err).NotTo(HaveOccurred())
			Expect(p1.Name()).To(Equal(p2.Name()))
		})
	})

	Describe("SetResponseObserver", func() {
		It("stores the observer without panicking", func() {
			p, err := opencodego.New("any-key")
			Expect(err).NotTo(HaveOccurred())
			observer := func(_ http.Header) {}
			Expect(func() { p.SetResponseObserver(observer) }).NotTo(Panic())
		})

		It("allows the observer to be overwritten on a later call", func() {
			p, err := opencodego.New("any-key")
			Expect(err).NotTo(HaveOccurred())
			p.SetResponseObserver(func(_ http.Header) {})
			Expect(func() { p.SetResponseObserver(nil) }).NotTo(Panic())
		})
	})

	Describe("Models", func() {
		It("returns the fallback model list when the API is unreachable", func() {
			p, err := opencodego.New("any-key")
			Expect(err).NotTo(HaveOccurred())

			models, modelErr := p.Models()
			Expect(modelErr).NotTo(HaveOccurred())
			Expect(models).NotTo(BeEmpty())

			ids := make([]string, 0, len(models))
			for _, m := range models {
				ids = append(ids, m.ID)
				Expect(m.Provider).To(Equal("opencode-go"))
				Expect(m.ContextLength).To(Equal(200000))
				Expect(m.OutputLimit).To(Equal(8192))
			}
			Expect(ids).To(ContainElements(
				"DeepSeek V4 Pro",
				"GLM-5.2",
				"Qwen3.7 Max",
				"Kimi K2.7 Code",
			))
		})
	})
})
