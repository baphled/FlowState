package config_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

func TestCategoryRoutingConfigMergesWithDefaults(t *testing.T) {
	RegisterTestingT(t)

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	err := os.WriteFile(configPath, []byte(`
category_routing:
  quick:
    model: flash
    temperature: 0.1
  custom:
    model: specialised
    provider: anthropic
    temperature: 0.7
    max_tokens: 2048
`), 0o600)
	Expect(err).NotTo(HaveOccurred())

	cfg, err := config.LoadConfigFromPath(configPath)
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg.CategoryRouting).To(HaveKey("quick"))
	Expect(cfg.CategoryRouting["quick"].Model).To(Equal("flash"))
	Expect(cfg.CategoryRouting["quick"].Temperature).To(Equal(0.1))
	Expect(cfg.CategoryRouting).To(HaveKey("deep"))
	Expect(cfg.CategoryRouting).To(HaveKey("custom"))
	Expect(cfg.CategoryRouting["custom"].Provider).To(Equal("anthropic"))
}
