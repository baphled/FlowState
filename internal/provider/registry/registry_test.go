package registry_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider/registry"
	"github.com/baphled/flowstate/internal/provider/zai"
)

func TestRegistry(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Provider Registry Suite")
}

var _ = Describe("Registry", func() {
	It("resolves z.ai from env to the concrete provider type", func() {
		GinkgoT().Setenv("ZAI_API_KEY", "k")
		p, err := registry.Resolve("z.ai")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(BeAssignableToTypeOf(&zai.Provider{}))
	})

	It("resolves z.ai from env reporting the provider name", func() {
		GinkgoT().Setenv("ZAI_API_KEY", "k")
		p, err := registry.Resolve("z.ai")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Name()).To(Equal("z.ai"))
	})

	It("errors for an unknown provider name", func() {
		_, err := registry.Resolve("nope")
		Expect(err).To(HaveOccurred())
	})

	It("errors when z.ai is resolved without an api key", func() {
		_, err := registry.Resolve("z.ai")
		Expect(err).To(HaveOccurred())
	})
})
