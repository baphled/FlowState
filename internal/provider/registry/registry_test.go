package registry_test

import (
	"os"
	"path/filepath"
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

var _ = Describe("zai config wiring", func() {
	var configDir string

	BeforeEach(func() {
		configDir = GinkgoT().TempDir()
		GinkgoT().Setenv("FLOWSTATE_CONFIG_DIR", configDir)
		GinkgoT().Setenv("ZAI_API_KEY", "cfg-key")
	})

	writeConfig := func(yaml string) {
		if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0o600); err != nil {
			panic(err)
		}
	}
	resolveCoding := func() bool {
		p, err := registry.Resolve("z.ai")
		if err != nil {
			panic(err)
		}
		zp, ok := p.(*zai.Provider)
		if !ok {
			panic("registry did not resolve a zai provider")
		}
		return zp.Coding()
	}

	Context("with config plan coding and no ZAI_PLAN", func() {
		var observedCoding bool
		BeforeEach(func() {
			GinkgoT().Setenv("ZAI_API_KEY", "")
			GinkgoT().Setenv("ZAI_PLAN", "")
			writeConfig("providers:\n  zai:\n    plan: coding\n    api_key: config-key\n")
			observedCoding = resolveCoding()
		})
		It("selects the anthropic route from config", func() { Expect(observedCoding).To(BeTrue()) })
	})

	Context("with no plan in config", func() {
		var observedCoding bool
		BeforeEach(func() {
			writeConfig("providers:\n  zai:\n    model: glm-5.3\n")
			observedCoding = resolveCoding()
		})
		It("keeps the default openai route", func() { Expect(observedCoding).To(BeFalse()) })
	})

	Context("with ZAI_PLAN=coding and config plan general", func() {
		var observedCoding bool
		BeforeEach(func() {
			GinkgoT().Setenv("ZAI_PLAN", "coding")
			writeConfig("providers:\n  zai:\n    plan: general\n")
			observedCoding = resolveCoding()
		})
		It("lets env override config to coding", func() { Expect(observedCoding).To(BeTrue()) })
	})

	Context("with ZAI_PLAN=general and config plan coding", func() {
		var observedCoding bool
		BeforeEach(func() {
			GinkgoT().Setenv("ZAI_PLAN", "general")
			writeConfig("providers:\n  zai:\n    plan: coding\n")
			observedCoding = resolveCoding()
		})
		It("lets env override config away from coding", func() { Expect(observedCoding).To(BeFalse()) })
	})
})
