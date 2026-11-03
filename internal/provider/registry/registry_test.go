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
		Expect(os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0o600)).To(Succeed())
	}

	It("selects the anthropic coding route from config plan without ZAI_PLAN", func() {
		GinkgoT().Setenv("ZAI_API_KEY", "")
		writeConfig("providers:\n  zai:\n    plan: coding\n    api_key: config-key\n")
		p, err := registry.Resolve("z.ai")
		Expect(err).NotTo(HaveOccurred())
		zp, ok := p.(*zai.Provider)
		Expect(ok).To(BeTrue())
		Expect(zp.Coding()).To(BeTrue())
	})

	It("keeps the default route when config sets no plan", func() {
		writeConfig("providers:\n  zai:\n    model: glm-5.3\n")
		p, err := registry.Resolve("z.ai")
		Expect(err).NotTo(HaveOccurred())
		zp, ok := p.(*zai.Provider)
		Expect(ok).To(BeTrue())
		Expect(zp.Coding()).To(BeFalse())
	})

	It("lets ZAI_PLAN env override the config plan", func() {
		GinkgoT().Setenv("ZAI_PLAN", "coding")
		writeConfig("providers:\n  zai:\n    plan: general\n")
		p, err := registry.Resolve("z.ai")
		Expect(err).NotTo(HaveOccurred())
		zp, ok := p.(*zai.Provider)
		Expect(ok).To(BeTrue())
		Expect(zp.Coding()).To(BeTrue())
	})

	It("lets ZAI_PLAN env disable coding set only in config", func() {
		GinkgoT().Setenv("ZAI_PLAN", "general")
		writeConfig("providers:\n  zai:\n    plan: coding\n")
		p, err := registry.Resolve("z.ai")
		Expect(err).NotTo(HaveOccurred())
		zp, ok := p.(*zai.Provider)
		Expect(ok).To(BeTrue())
		Expect(zp.Coding()).To(BeFalse())
	})
})
