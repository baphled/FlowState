package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("CompressionConfig wiring", func() {
	var tempDir string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "compression-config-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("DefaultConfig", func() {
		It("ships all compression layers disabled", func() {
			cfg := config.DefaultConfig()

			Expect(cfg.Compression.MicroCompaction.Enabled).To(BeFalse())
			Expect(cfg.Compression.AutoCompaction.Enabled).To(BeFalse())
			Expect(cfg.Compression.SessionMemory.Enabled).To(BeFalse())
		})

		It("defaults auto-compaction threshold to 0.75", func() {
			cfg := config.DefaultConfig()

			Expect(cfg.Compression.AutoCompaction.Threshold).To(Equal(0.75))
		})

		It("defaults micro-compaction numeric fields", func() {
			cfg := config.DefaultConfig()

			Expect(cfg.Compression.MicroCompaction.HotTailSize).To(Equal(5))
			Expect(cfg.Compression.MicroCompaction.TokenThreshold).To(Equal(1000))
			Expect(cfg.Compression.MicroCompaction.PlaceholderTokens).To(Equal(50))
		})
	})

	Describe("applyCompressionDefaults", func() {
		It("populates every zero-valued field from defaults", func() {
			cfg := &config.AppConfig{}

			config.ApplyCompressionDefaultsForTest(cfg)

			Expect(cfg.Compression.MicroCompaction.HotTailSize).To(Equal(5))
			Expect(cfg.Compression.MicroCompaction.TokenThreshold).To(Equal(1000))
			Expect(cfg.Compression.MicroCompaction.PlaceholderTokens).To(Equal(50))
			Expect(cfg.Compression.MicroCompaction.StorageDir).To(Equal("~/.flowstate/compacted"))
			Expect(cfg.Compression.AutoCompaction.Threshold).To(Equal(0.75))
			Expect(cfg.Compression.SessionMemory.StorageDir).To(Equal("~/.flowstate/session-memory"))
		})

		It("preserves caller-provided overrides", func() {
			cfg := &config.AppConfig{}
			cfg.Compression.MicroCompaction.HotTailSize = 7
			cfg.Compression.MicroCompaction.TokenThreshold = 2000
			cfg.Compression.MicroCompaction.PlaceholderTokens = 80
			cfg.Compression.MicroCompaction.StorageDir = "/override/micro"
			cfg.Compression.AutoCompaction.Threshold = 0.6
			cfg.Compression.SessionMemory.StorageDir = "/override/memory"

			config.ApplyCompressionDefaultsForTest(cfg)

			Expect(cfg.Compression.MicroCompaction.HotTailSize).To(Equal(7))
			Expect(cfg.Compression.MicroCompaction.TokenThreshold).To(Equal(2000))
			Expect(cfg.Compression.MicroCompaction.PlaceholderTokens).To(Equal(80))
			Expect(cfg.Compression.MicroCompaction.StorageDir).To(Equal("/override/micro"))
			Expect(cfg.Compression.AutoCompaction.Threshold).To(Equal(0.6))
			Expect(cfg.Compression.SessionMemory.StorageDir).To(Equal("/override/memory"))
		})
	})

	Describe("LoadConfigFromPath", func() {
		Context("when the compression section is absent", func() {
			It("applies full defaults and expands tilde paths", func() {
				configContent := `
log_level: info
`
				configPath := filepath.Join(tempDir, "config.yaml")
				Expect(os.WriteFile(configPath, []byte(configContent), 0o600)).To(Succeed())

				cfg, err := config.LoadConfigFromPath(configPath)
				Expect(err).NotTo(HaveOccurred())

				homeDir, homeErr := os.UserHomeDir()
				Expect(homeErr).NotTo(HaveOccurred())

				Expect(cfg.Compression.MicroCompaction.Enabled).To(BeFalse())
				Expect(cfg.Compression.AutoCompaction.Enabled).To(BeFalse())
				Expect(cfg.Compression.SessionMemory.Enabled).To(BeFalse())

				Expect(cfg.Compression.MicroCompaction.HotTailSize).To(Equal(5))
				Expect(cfg.Compression.MicroCompaction.TokenThreshold).To(Equal(1000))
				Expect(cfg.Compression.MicroCompaction.PlaceholderTokens).To(Equal(50))
				Expect(cfg.Compression.AutoCompaction.Threshold).To(Equal(0.75))

				Expect(cfg.Compression.MicroCompaction.StorageDir).To(Equal(filepath.Join(homeDir, ".flowstate", "compacted")))
				Expect(cfg.Compression.SessionMemory.StorageDir).To(Equal(filepath.Join(homeDir, ".flowstate", "session-memory")))
			})
		})

		Context("when micro-compaction is enabled with hot_tail_size of zero", func() {
			// H4 regression. Under the current default-fill logic,
			// writing `hot_tail_size: 0` in YAML is indistinguishable
			// from omitting it — defaults fill the zero back to 5. The
			// attack surface is any codepath that bypasses the default
			// fill: direct struct construction, future pointer-tagged
			// YAML fields, or a drifting merge-helper. Validation at
			// load time is the backstop; it rejects the shape
			// regardless of how it arrived.
			It("returns a startup error rather than silently promoting every message to cold", func() {
				// Build a config whose HotTailSize survives the
				// applyCompressionDefaults pass: set Enabled true (so
				// the check fires) and HotTailSize to a non-zero
				// negative (−0 is indistinguishable from 0 at YAML
				// load, but negatives survive the default fill).
				cfg := &config.AppConfig{}
				cfg.Compression.MicroCompaction.Enabled = true
				cfg.Compression.MicroCompaction.HotTailSize = -1

				err := config.ValidateForTest(cfg)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("hot_tail_size"))
			})
		})

		Context("when the compression section overrides a subset of fields", func() {
			It("round-trips provided values and fills the rest from defaults", func() {
				configContent := `
compression:
  micro_compaction:
    enabled: true
    hot_tail_size: 10
    storage_dir: /tmp/micro
  auto_compaction:
    enabled: true
    threshold: 0.70
  session_memory:
    enabled: true
    storage_dir: /tmp/memory
`
				configPath := filepath.Join(tempDir, "config.yaml")
				Expect(os.WriteFile(configPath, []byte(configContent), 0o600)).To(Succeed())

				cfg, err := config.LoadConfigFromPath(configPath)
				Expect(err).NotTo(HaveOccurred())

				// Overridden values survive.
				Expect(cfg.Compression.MicroCompaction.Enabled).To(BeTrue())
				Expect(cfg.Compression.MicroCompaction.HotTailSize).To(Equal(10))
				Expect(cfg.Compression.MicroCompaction.StorageDir).To(Equal("/tmp/micro"))
				Expect(cfg.Compression.AutoCompaction.Enabled).To(BeTrue())
				Expect(cfg.Compression.AutoCompaction.Threshold).To(Equal(0.70))
				Expect(cfg.Compression.SessionMemory.Enabled).To(BeTrue())
				Expect(cfg.Compression.SessionMemory.StorageDir).To(Equal("/tmp/memory"))

				// Missing numeric fields still pick up defaults.
				Expect(cfg.Compression.MicroCompaction.TokenThreshold).To(Equal(1000))
				Expect(cfg.Compression.MicroCompaction.PlaceholderTokens).To(Equal(50))
			})
		})
	})
})
