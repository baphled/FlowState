package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("CategoryRouting config", func() {
	It("merges user overrides with hardcoded defaults on load", func() {
		tmpDir := GinkgoT().TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")

		Expect(os.WriteFile(configPath, []byte(`
category_routing:
  quick:
    model: flash
    temperature: 0.1
  custom:
    model: specialised
    provider: anthropic
    temperature: 0.7
    max_tokens: 2048
`), 0o600)).To(Succeed())

		cfg, err := config.LoadConfigFromPath(configPath)

		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.CategoryRouting).To(HaveKey("quick"))
		Expect(cfg.CategoryRouting["quick"].Model).To(Equal("flash"))
		Expect(cfg.CategoryRouting["quick"].Temperature).To(Equal(0.1))
		Expect(cfg.CategoryRouting).To(HaveKey("deep"))
		Expect(cfg.CategoryRouting).To(HaveKey("custom"))
		Expect(cfg.CategoryRouting["custom"].Provider).To(Equal("anthropic"))
	})
})
