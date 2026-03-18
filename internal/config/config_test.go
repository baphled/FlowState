package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("Config", func() {
	var tempDir string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "config-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("DefaultConfig", func() {
		It("returns config with sensible defaults", func() {
			cfg := config.DefaultConfig()

			Expect(cfg).NotTo(BeNil())
			Expect(cfg.Providers.Default).To(Equal("ollama"))
			Expect(cfg.LogLevel).To(Equal("info"))
			Expect(cfg.DefaultAgent).To(Equal("worker"))
		})

		It("sets Ollama provider defaults", func() {
			cfg := config.DefaultConfig()

			Expect(cfg.Providers.Ollama.Host).To(Equal("http://localhost:11434"))
			Expect(cfg.Providers.Ollama.Model).To(Equal("llama3.2"))
		})

		It("sets OpenAI provider defaults", func() {
			cfg := config.DefaultConfig()

			Expect(cfg.Providers.OpenAI.Model).To(Equal("gpt-4o"))
		})

		It("sets Anthropic provider defaults", func() {
			cfg := config.DefaultConfig()

			Expect(cfg.Providers.Anthropic.Model).To(Equal("claude-sonnet-4-20250514"))
		})

		It("sets data directories relative to home", func() {
			cfg := config.DefaultConfig()
			homeDir, _ := os.UserHomeDir()
			expectedDataDir := filepath.Join(homeDir, ".flowstate")

			Expect(cfg.DataDir).To(Equal(expectedDataDir))
			Expect(cfg.AgentDir).To(Equal(filepath.Join(expectedDataDir, "agents")))
			Expect(cfg.SkillDir).To(Equal(filepath.Join(expectedDataDir, "skills")))
		})
	})

	Describe("LoadConfigFromPath", func() {
		Context("when config file does not exist", func() {
			It("returns default config", func() {
				cfg, err := config.LoadConfigFromPath(filepath.Join(tempDir, "nonexistent.yaml"))

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg).NotTo(BeNil())
				Expect(cfg.Providers.Default).To(Equal("ollama"))
			})
		})

		Context("when config file exists", func() {
			It("loads and merges with defaults", func() {
				configContent := `
providers:
  default: openai
  openai:
    api_key: test-key
log_level: debug
`
				configPath := filepath.Join(tempDir, "config.yaml")
				err := os.WriteFile(configPath, []byte(configContent), 0o644)
				Expect(err).NotTo(HaveOccurred())

				cfg, err := config.LoadConfigFromPath(configPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.Providers.Default).To(Equal("openai"))
				Expect(cfg.Providers.OpenAI.APIKey).To(Equal("test-key"))
				Expect(cfg.LogLevel).To(Equal("debug"))
				Expect(cfg.Providers.Ollama.Host).To(Equal("http://localhost:11434"))
			})

			It("applies defaults for missing fields", func() {
				configContent := `
log_level: warn
`
				configPath := filepath.Join(tempDir, "config.yaml")
				err := os.WriteFile(configPath, []byte(configContent), 0o644)
				Expect(err).NotTo(HaveOccurred())

				cfg, err := config.LoadConfigFromPath(configPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.LogLevel).To(Equal("warn"))
				Expect(cfg.Providers.Default).To(Equal("ollama"))
				Expect(cfg.DefaultAgent).To(Equal("worker"))
			})

			It("preserves all provider configurations", func() {
				configContent := `
providers:
  ollama:
    host: http://custom:11434
    model: custom-model
  openai:
    api_key: openai-key
    model: gpt-4-turbo
  anthropic:
    api_key: anthropic-key
    model: claude-3
`
				configPath := filepath.Join(tempDir, "config.yaml")
				err := os.WriteFile(configPath, []byte(configContent), 0o644)
				Expect(err).NotTo(HaveOccurred())

				cfg, err := config.LoadConfigFromPath(configPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.Providers.Ollama.Host).To(Equal("http://custom:11434"))
				Expect(cfg.Providers.Ollama.Model).To(Equal("custom-model"))
				Expect(cfg.Providers.OpenAI.APIKey).To(Equal("openai-key"))
				Expect(cfg.Providers.OpenAI.Model).To(Equal("gpt-4-turbo"))
				Expect(cfg.Providers.Anthropic.APIKey).To(Equal("anthropic-key"))
				Expect(cfg.Providers.Anthropic.Model).To(Equal("claude-3"))
			})
		})

		Context("when config file is invalid", func() {
			It("returns error for invalid YAML", func() {
				invalidYAML := `
providers:
  - this is invalid
  default: [broken
`
				configPath := filepath.Join(tempDir, "invalid.yaml")
				err := os.WriteFile(configPath, []byte(invalidYAML), 0o644)
				Expect(err).NotTo(HaveOccurred())

				cfg, err := config.LoadConfigFromPath(configPath)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("parsing config file"))
				Expect(cfg).To(BeNil())
			})
		})

		Context("with directory paths", func() {
			It("applies defaults for empty directories", func() {
				configContent := `
log_level: info
`
				configPath := filepath.Join(tempDir, "config.yaml")
				err := os.WriteFile(configPath, []byte(configContent), 0o644)
				Expect(err).NotTo(HaveOccurred())

				cfg, err := config.LoadConfigFromPath(configPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.AgentDir).NotTo(BeEmpty())
				Expect(cfg.SkillDir).NotTo(BeEmpty())
				Expect(cfg.DataDir).NotTo(BeEmpty())
			})

			It("preserves custom directory paths", func() {
				configContent := `
agent_dir: /custom/agents
skill_dir: /custom/skills
data_dir: /custom/data
`
				configPath := filepath.Join(tempDir, "config.yaml")
				err := os.WriteFile(configPath, []byte(configContent), 0o644)
				Expect(err).NotTo(HaveOccurred())

				cfg, err := config.LoadConfigFromPath(configPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.AgentDir).To(Equal("/custom/agents"))
				Expect(cfg.SkillDir).To(Equal("/custom/skills"))
				Expect(cfg.DataDir).To(Equal("/custom/data"))
			})
		})
	})
})
