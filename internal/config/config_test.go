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

	Describe("ConfigDir", func() {
		Context("when XDG_CONFIG_HOME is set", func() {
			It("returns XDG_CONFIG_HOME/flowstate", func() {
				xdgPath := filepath.Join(tempDir, "xdg-config")
				os.Setenv("XDG_CONFIG_HOME", xdgPath)
				DeferCleanup(func() { os.Unsetenv("XDG_CONFIG_HOME") })

				result := config.Dir()

				Expect(result).To(Equal(filepath.Join(xdgPath, "flowstate")))
			})
		})

		Context("when XDG_CONFIG_HOME is not set", func() {
			It("returns ~/.config/flowstate", func() {
				os.Unsetenv("XDG_CONFIG_HOME")
				DeferCleanup(func() {})

				result := config.Dir()
				homeDir, _ := os.UserHomeDir()

				Expect(result).To(Equal(filepath.Join(homeDir, ".config", "flowstate")))
			})
		})
	})

	Describe("DataDir", func() {
		Context("when XDG_DATA_HOME is set", func() {
			It("returns XDG_DATA_HOME/flowstate", func() {
				xdgPath := filepath.Join(tempDir, "xdg-data")
				os.Setenv("XDG_DATA_HOME", xdgPath)
				DeferCleanup(func() { os.Unsetenv("XDG_DATA_HOME") })

				result := config.DataDir()

				Expect(result).To(Equal(filepath.Join(xdgPath, "flowstate")))
			})
		})

		Context("when XDG_DATA_HOME is not set", func() {
			It("returns ~/.local/share/flowstate", func() {
				os.Unsetenv("XDG_DATA_HOME")
				DeferCleanup(func() {})

				result := config.DataDir()
				homeDir, _ := os.UserHomeDir()

				Expect(result).To(Equal(filepath.Join(homeDir, ".local", "share", "flowstate")))
			})
		})
	})

	Describe("DefaultConfig", func() {
		It("returns config with sensible defaults", func() {
			cfg := config.DefaultConfig()

			Expect(cfg).NotTo(BeNil())
			Expect(cfg.Providers.Default).To(Equal("ollama"))
			Expect(cfg.LogLevel).To(Equal("info"))
			Expect(cfg.DefaultAgent).To(Equal("executor"))
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

		It("sets data directories using DataDir()", func() {
			cfg := config.DefaultConfig()
			expectedDataDir := config.DataDir()

			Expect(cfg.DataDir).To(Equal(expectedDataDir))
			Expect(cfg.AgentDir).To(Equal(filepath.Join(expectedDataDir, "agents")))
			Expect(cfg.SkillDir).To(Equal(filepath.Join(expectedDataDir, "skills")))
		})
	})

	Describe("LoadConfig", func() {
		Context("when XDG_CONFIG_HOME is set and config exists there", func() {
			It("loads from XDG_CONFIG_HOME/flowstate/config.yaml", func() {
				xdgPath := filepath.Join(tempDir, "xdg-config")
				flowstatePath := filepath.Join(xdgPath, "flowstate")
				os.MkdirAll(flowstatePath, 0o755)

				configContent := `
providers:
  default: openai
log_level: debug
`
				configPath := filepath.Join(flowstatePath, "config.yaml")
				err := os.WriteFile(configPath, []byte(configContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				os.Setenv("XDG_CONFIG_HOME", xdgPath)
				DeferCleanup(func() { os.Unsetenv("XDG_CONFIG_HOME") })

				cfg, err := config.LoadConfig()

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.Providers.Default).To(Equal("openai"))
				Expect(cfg.LogLevel).To(Equal("debug"))
			})
		})

		Context("when no config file exists", func() {
			It("returns default config", func() {
				os.Unsetenv("XDG_CONFIG_HOME")
				DeferCleanup(func() {})

				cfg, err := config.LoadConfig()

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg).NotTo(BeNil())
				Expect(cfg.Providers.Default).To(Equal("ollama"))
			})
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
				err := os.WriteFile(configPath, []byte(configContent), 0o600)
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
				err := os.WriteFile(configPath, []byte(configContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				cfg, err := config.LoadConfigFromPath(configPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.LogLevel).To(Equal("warn"))
				Expect(cfg.Providers.Default).To(Equal("ollama"))
				Expect(cfg.DefaultAgent).To(Equal("executor"))
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
				err := os.WriteFile(configPath, []byte(configContent), 0o600)
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
				err := os.WriteFile(configPath, []byte(invalidYAML), 0o600)
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
				err := os.WriteFile(configPath, []byte(configContent), 0o600)
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
				err := os.WriteFile(configPath, []byte(configContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				cfg, err := config.LoadConfigFromPath(configPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.AgentDir).To(Equal("/custom/agents"))
				Expect(cfg.SkillDir).To(Equal("/custom/skills"))
				Expect(cfg.DataDir).To(Equal("/custom/data"))
			})
		})

		Context("with MCP servers", func() {
			It("parses mcp_servers section correctly", func() {
				configContent := `
mcp_servers:
  - name: test-server
    command: /usr/bin/test
    args:
      - --flag
    env:
      TEST_VAR: value
    enabled: true
`
				configPath := filepath.Join(tempDir, "config.yaml")
				err := os.WriteFile(configPath, []byte(configContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				cfg, err := config.LoadConfigFromPath(configPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.MCPServers).To(HaveLen(1))
				Expect(cfg.MCPServers[0].Name).To(Equal("test-server"))
				Expect(cfg.MCPServers[0].Command).To(Equal("/usr/bin/test"))
				Expect(cfg.MCPServers[0].Args).To(Equal([]string{"--flag"}))
				Expect(cfg.MCPServers[0].Env).To(HaveKeyWithValue("TEST_VAR", "value"))
				Expect(cfg.MCPServers[0].Enabled).To(BeTrue())
			})

			It("defaults Enabled to true when not set", func() {
				configContent := `
mcp_servers:
  - name: test-server
    command: /usr/bin/test
`
				configPath := filepath.Join(tempDir, "config.yaml")
				err := os.WriteFile(configPath, []byte(configContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				cfg, err := config.LoadConfigFromPath(configPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.MCPServers[0].Enabled).To(BeTrue())
			})
		})
	})

	Describe("ValidateMCPServers", func() {
		Context("validation", func() {
			It("rejects server with missing Name", func() {
				servers := []config.MCPServerConfig{
					{
						Command: "/usr/bin/test",
					},
				}

				err := config.ValidateMCPServers(servers)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("missing required field 'name'"))
			})

			It("rejects server with missing Command", func() {
				servers := []config.MCPServerConfig{
					{
						Name: "test-server",
					},
				}

				err := config.ValidateMCPServers(servers)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("missing required field 'command'"))
			})

			It("accepts valid server config", func() {
				servers := []config.MCPServerConfig{
					{
						Name:    "test-server",
						Command: "/usr/bin/test",
					},
				}

				err := config.ValidateMCPServers(servers)

				Expect(err).NotTo(HaveOccurred())
			})

			It("accepts empty server list", func() {
				servers := []config.MCPServerConfig{}

				err := config.ValidateMCPServers(servers)

				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	Describe("AlwaysActiveSkills", func() {
		It("defaults to 6 mandatory skills", func() {
			cfg := config.DefaultConfig()

			Expect(cfg.AlwaysActiveSkills).To(HaveLen(6))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("pre-action"))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("memory-keeper"))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("token-cost-estimation"))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("retrospective"))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("note-taking"))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("knowledge-base"))
		})

		It("loads from YAML config", func() {
			tempDir, err := os.MkdirTemp("", "config-test")
			Expect(err).NotTo(HaveOccurred())
			defer os.RemoveAll(tempDir)

			configContent := `
always_active_skills:
  - custom-skill-1
  - custom-skill-2
`
			configPath := filepath.Join(tempDir, "config.yaml")
			err = os.WriteFile(configPath, []byte(configContent), 0o600)
			Expect(err).NotTo(HaveOccurred())

			cfg, err := config.LoadConfigFromPath(configPath)

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.AlwaysActiveSkills).To(HaveLen(2))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("custom-skill-1"))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("custom-skill-2"))
		})
	})
})
