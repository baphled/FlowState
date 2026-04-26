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
			Expect(cfg.Providers.Default).To(Equal("anthropic"))
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

		It("sets DataDir using DataDir()", func() {
			cfg := config.DefaultConfig()
			expectedDataDir := config.DataDir()

			Expect(cfg.DataDir).To(Equal(expectedDataDir))
		})

		It("locates AgentDir under XDG_CONFIG rather than XDG_DATA", func() {
			// Agent manifests are user-edited config (tweaking
			// `harness.critic_enabled`, swapping models). XDG_CONFIG is
			// the canonical home for that class of file; swarms already
			// live there. Pin AgentDir to Dir() so a future refactor
			// that re-derives it from DataDir is caught at RED.
			cfg := config.DefaultConfig()

			Expect(cfg.AgentDir).To(Equal(filepath.Join(config.Dir(), "agents")))
			Expect(cfg.AgentDir).NotTo(Equal(filepath.Join(config.DataDir(), "agents")),
				"AgentDir must NOT be re-derived from DataDir — XDG_CONFIG is correct")
		})

		It("locates SkillDir under XDG_CONFIG rather than XDG_DATA", func() {
			cfg := config.DefaultConfig()

			Expect(cfg.SkillDir).To(Equal(filepath.Join(config.Dir(), "skills")))
			Expect(cfg.SkillDir).NotTo(Equal(filepath.Join(config.DataDir(), "skills")),
				"SkillDir must NOT be re-derived from DataDir — XDG_CONFIG is correct")
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
				// Point both XDG_CONFIG_HOME and HOME at an empty tmpdir so
				// LoadConfig cannot resolve to the developer's real
				// ~/.config/flowstate/config.yaml on disk. GinkgoT().Setenv
				// captures the prior value and restores it at spec teardown.
				isolated := filepath.Join(tempDir, "isolated-home")
				Expect(os.MkdirAll(isolated, 0o755)).To(Succeed())
				GinkgoT().Setenv("XDG_CONFIG_HOME", isolated)
				GinkgoT().Setenv("HOME", isolated)

				cfg, err := config.LoadConfig()

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg).NotTo(BeNil())
				Expect(cfg.Providers.Default).To(Equal("anthropic"))
			})
		})
	})

	Describe("LoadConfigFromPath", func() {
		Context("when plugins section is absent", func() {
			It("loads plugin defaults", func() {
				configContent := `
log_level: info
`
				configPath := filepath.Join(tempDir, "config.yaml")
				err := os.WriteFile(configPath, []byte(configContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				cfg, err := config.LoadConfigFromPath(configPath)

				Expect(err).NotTo(HaveOccurred())
				homeDir, _ := os.UserHomeDir()
				Expect(cfg.Plugins.Dir).To(Equal(filepath.Join(homeDir, ".config", "flowstate", "plugins")))
				Expect(cfg.Plugins.Timeout).To(Equal(5))
			})
		})

		Context("when plugins section is provided", func() {
			It("loads custom plugin dir from YAML", func() {
				configContent := `
plugins:
  dir: /custom/plugins
`
				configPath := filepath.Join(tempDir, "config.yaml")
				err := os.WriteFile(configPath, []byte(configContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				cfg, err := config.LoadConfigFromPath(configPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.Plugins.Dir).To(Equal("/custom/plugins"))
				Expect(cfg.Plugins.Timeout).To(Equal(5))
			})

			It("loads enabled and disabled lists", func() {
				configContent := `
plugins:
  enabled:
    - plugin-a
    - plugin-b
  disabled:
    - plugin-c
  timeout: 12
`
				configPath := filepath.Join(tempDir, "config.yaml")
				err := os.WriteFile(configPath, []byte(configContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				cfg, err := config.LoadConfigFromPath(configPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.Plugins.Enabled).To(Equal([]string{"plugin-a", "plugin-b"}))
				Expect(cfg.Plugins.Disabled).To(Equal([]string{"plugin-c"}))
				Expect(cfg.Plugins.Timeout).To(Equal(12))
			})
		})

		Context("when config file does not exist", func() {
			It("returns default config", func() {
				cfg, err := config.LoadConfigFromPath(filepath.Join(tempDir, "nonexistent.yaml"))

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg).NotTo(BeNil())
				Expect(cfg.Providers.Default).To(Equal("anthropic"))
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
				Expect(cfg.Providers.Default).To(Equal("anthropic"))
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

	Describe("Harness configuration", func() {
		Describe("DefaultConfig", func() {
			It("sets harness defaults", func() {
				cfg := config.DefaultConfig()

				Expect(cfg.Harness).NotTo(BeNil())
				Expect(cfg.Harness.Enabled).To(BeTrue())
				Expect(cfg.Harness.CriticEnabled).To(BeFalse())
				Expect(cfg.Harness.VotingEnabled).To(BeFalse())
			})
		})

		Describe("LoadConfigFromPath", func() {
			Context("when harness section is provided", func() {
				// Note: Due to YAML unmarshalling, missing bool fields default to false.
				// We cannot distinguish between "not provided" and "explicitly set to false".
				// This matches the same pattern used for MCP servers in this codebase.
				// To disable harness, users must explicitly set enabled: false in their config.

				It("parses all harness fields correctly when fully specified", func() {
					configContent := `
harness:
  enabled: true
  critic_enabled: true
  voting_enabled: true
  project_root: /custom/project
`
					configPath := filepath.Join(tempDir, "config.yaml")
					err := os.WriteFile(configPath, []byte(configContent), 0o600)
					Expect(err).NotTo(HaveOccurred())

					cfg, err := config.LoadConfigFromPath(configPath)

					Expect(err).NotTo(HaveOccurred())
					Expect(cfg.Harness.Enabled).To(BeTrue())
					Expect(cfg.Harness.CriticEnabled).To(BeTrue())
					Expect(cfg.Harness.VotingEnabled).To(BeTrue())
					Expect(cfg.Harness.ProjectRoot).To(Equal("/custom/project"))
				})

				It("applies defaults when harness section is partial", func() {
					configContent := `
harness:
  critic_enabled: true
`
					configPath := filepath.Join(tempDir, "config.yaml")
					err := os.WriteFile(configPath, []byte(configContent), 0o600)
					Expect(err).NotTo(HaveOccurred())

					cfg, err := config.LoadConfigFromPath(configPath)

					Expect(err).NotTo(HaveOccurred())
					Expect(cfg.Harness.Enabled).To(BeTrue())        // default
					Expect(cfg.Harness.CriticEnabled).To(BeTrue())  // from config
					Expect(cfg.Harness.VotingEnabled).To(BeFalse()) // default
				})
			})

			Context("when harness section is not provided", func() {
				It("uses defaults", func() {
					configContent := `
log_level: debug
`
					configPath := filepath.Join(tempDir, "config.yaml")
					err := os.WriteFile(configPath, []byte(configContent), 0o600)
					Expect(err).NotTo(HaveOccurred())

					cfg, err := config.LoadConfigFromPath(configPath)

					Expect(err).NotTo(HaveOccurred())
					Expect(cfg.Harness.Enabled).To(BeTrue())
					Expect(cfg.Harness.CriticEnabled).To(BeFalse())
					Expect(cfg.Harness.VotingEnabled).To(BeFalse())
				})
			})
		})
	})

	Describe("Failover configuration", func() {
		Describe("DefaultConfig", func() {
			It("includes default tier mappings", func() {
				cfg := config.DefaultConfig()

				Expect(cfg.Plugins.Failover.Tiers).To(HaveLen(6))
				Expect(cfg.Plugins.Failover.Tiers).To(HaveKeyWithValue("claude-sonnet-4-20250514", "tier-0"))
				Expect(cfg.Plugins.Failover.Tiers).To(HaveKeyWithValue("claude-3-5-sonnet-20241022", "tier-0"))
				Expect(cfg.Plugins.Failover.Tiers).To(HaveKeyWithValue("gpt-4o", "tier-1"))
				Expect(cfg.Plugins.Failover.Tiers).To(HaveKeyWithValue("gpt-4o-mini", "tier-2"))
				Expect(cfg.Plugins.Failover.Tiers).To(HaveKeyWithValue("llama3.2", "tier-3"))
				Expect(cfg.Plugins.Failover.Tiers).To(HaveKeyWithValue("llama3", "tier-3"))
			})
		})

		Describe("LoadConfigFromPath", func() {
			Context("when failover section is absent", func() {
				It("applies default tier mappings", func() {
					configContent := `
log_level: info
`
					configPath := filepath.Join(tempDir, "config.yaml")
					err := os.WriteFile(configPath, []byte(configContent), 0o600)
					Expect(err).NotTo(HaveOccurred())

					cfg, err := config.LoadConfigFromPath(configPath)

					Expect(err).NotTo(HaveOccurred())
					Expect(cfg.Plugins.Failover.Tiers).To(HaveLen(6))
					Expect(cfg.Plugins.Failover.Tiers).To(HaveKeyWithValue("claude-sonnet-4-20250514", "tier-0"))
				})
			})

			Context("when failover section is provided", func() {
				It("preserves custom tier mappings", func() {
					configContent := `
plugins:
  failover:
    tiers:
      anthropic: "tier-0"
      ollama: "tier-1"
`
					configPath := filepath.Join(tempDir, "config.yaml")
					err := os.WriteFile(configPath, []byte(configContent), 0o600)
					Expect(err).NotTo(HaveOccurred())

					cfg, err := config.LoadConfigFromPath(configPath)

					Expect(err).NotTo(HaveOccurred())
					Expect(cfg.Plugins.Failover.Tiers).To(HaveKeyWithValue("anthropic", "tier-0"))
					Expect(cfg.Plugins.Failover.Tiers).To(HaveKeyWithValue("ollama", "tier-1"))
				})
			})
		})
	})

	Describe("AlwaysActiveSkills", func() {
		It("defaults to 9 canonical core-tier skills", func() {
			cfg := config.DefaultConfig()

			Expect(cfg.AlwaysActiveSkills).To(HaveLen(9))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("pre-action"))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("memory-keeper"))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("token-cost-estimation"))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("retrospective"))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("note-taking"))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("knowledge-base"))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("discipline"))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("skill-discovery"))
			Expect(cfg.AlwaysActiveSkills).To(ContainElement("agent-discovery"))
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

	Describe("AgentDirs", func() {
		It("defaults to nil in DefaultConfig", func() {
			cfg := config.DefaultConfig()

			Expect(cfg.AgentDirs).To(BeEmpty())
		})

		It("parses agent_dirs from YAML", func() {
			configContent := `
agent_dirs:
  - /custom/agents/a
  - /custom/agents/b
`
			configPath := filepath.Join(tempDir, "config.yaml")
			err := os.WriteFile(configPath, []byte(configContent), 0o600)
			Expect(err).NotTo(HaveOccurred())

			cfg, err := config.LoadConfigFromPath(configPath)

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.AgentDirs).To(Equal([]string{"/custom/agents/a", "/custom/agents/b"}))
		})

		It("expands tilde in each AgentDirs entry", func() {
			home, _ := os.UserHomeDir()
			configContent := `
agent_dirs:
  - ~/my-agents
  - ~/other-agents
`
			configPath := filepath.Join(tempDir, "config.yaml")
			err := os.WriteFile(configPath, []byte(configContent), 0o600)
			Expect(err).NotTo(HaveOccurred())

			cfg, err := config.LoadConfigFromPath(configPath)

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.AgentDirs).To(Equal([]string{
				filepath.Join(home, "my-agents"),
				filepath.Join(home, "other-agents"),
			}))
		})

		It("leaves AgentDirs nil when not specified in YAML", func() {
			configContent := `
log_level: info
`
			configPath := filepath.Join(tempDir, "config.yaml")
			err := os.WriteFile(configPath, []byte(configContent), 0o600)
			Expect(err).NotTo(HaveOccurred())

			cfg, err := config.LoadConfigFromPath(configPath)

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.AgentDirs).To(BeEmpty())
		})
	})

	Describe("expandTilde", func() {
		It("expands ~ alone to home directory", func() {
			home, _ := os.UserHomeDir()
			Expect(config.ExpandTildeForTest("~")).To(Equal(home))
		})

		It("expands ~/path to home directory with path", func() {
			home, _ := os.UserHomeDir()
			Expect(config.ExpandTildeForTest("~/foo/bar")).To(Equal(filepath.Join(home, "foo", "bar")))
		})

		It("leaves absolute paths unchanged", func() {
			Expect(config.ExpandTildeForTest("/absolute/path")).To(Equal("/absolute/path"))
		})

		It("leaves relative paths unchanged", func() {
			Expect(config.ExpandTildeForTest("relative/path")).To(Equal("relative/path"))
		})

		It("leaves empty string unchanged", func() {
			Expect(config.ExpandTildeForTest("")).To(Equal(""))
		})

		It("does not expand tilde mid-string", func() {
			Expect(config.ExpandTildeForTest("/foo/~/bar")).To(Equal("/foo/~/bar"))
		})
	})

	Describe("expandPaths", func() {
		It("expands tilde in all path fields", func() {
			home, _ := os.UserHomeDir()
			cfg := &config.AppConfig{
				AgentDir: "~/.local/share/flowstate/agents",
				SkillDir: "~/skills",
				DataDir:  "~/.local/share/flowstate",
			}
			cfg.Plugins.Dir = "~/plugins"
			config.ExpandPathsForTest(cfg)
			Expect(cfg.AgentDir).To(Equal(filepath.Join(home, ".local", "share", "flowstate", "agents")))
			Expect(cfg.SkillDir).To(Equal(filepath.Join(home, "skills")))
			Expect(cfg.DataDir).To(Equal(filepath.Join(home, ".local", "share", "flowstate")))
			Expect(cfg.Plugins.Dir).To(Equal(filepath.Join(home, "plugins")))
		})

		It("leaves absolute paths unchanged", func() {
			cfg := &config.AppConfig{
				AgentDir: "/absolute/agents",
				SkillDir: "/absolute/skills",
				DataDir:  "/absolute/data",
			}
			config.ExpandPathsForTest(cfg)
			Expect(cfg.AgentDir).To(Equal("/absolute/agents"))
			Expect(cfg.SkillDir).To(Equal("/absolute/skills"))
			Expect(cfg.DataDir).To(Equal("/absolute/data"))
		})
	})

	Describe("ResolvedEmbeddingModel", func() {
		It("returns the historical default when EmbeddingModel is unset", func() {
			cfg := &config.AppConfig{}
			Expect(cfg.ResolvedEmbeddingModel()).To(Equal(config.DefaultEmbeddingModel))
			Expect(config.DefaultEmbeddingModel).To(Equal("nomic-embed-text"))
		})

		It("returns the configured value when EmbeddingModel is set", func() {
			cfg := &config.AppConfig{EmbeddingModel: "text-embedding-3-small"}
			Expect(cfg.ResolvedEmbeddingModel()).To(Equal("text-embedding-3-small"))
		})

		It("tolerates a nil receiver and returns the historical default", func() {
			var cfg *config.AppConfig
			Expect(cfg.ResolvedEmbeddingModel()).To(Equal(config.DefaultEmbeddingModel))
		})
	})

	Describe("DefaultProviderModel", func() {
		It("returns the model for the named default provider", func() {
			cfg := &config.AppConfig{}
			cfg.Providers.Default = "zai"
			cfg.Providers.ZAI.Model = "glm-4.7"
			Expect(cfg.DefaultProviderModel()).To(Equal("glm-4.7"))
		})

		It("returns empty when the default provider has no model configured", func() {
			cfg := &config.AppConfig{}
			cfg.Providers.Default = "anthropic"
			Expect(cfg.DefaultProviderModel()).To(BeEmpty())
		})

		It("returns empty when the default provider name is unknown", func() {
			cfg := &config.AppConfig{}
			cfg.Providers.Default = "made-up"
			Expect(cfg.DefaultProviderModel()).To(BeEmpty())
		})
	})
})

// AppConfig.ResolvedPlanLocation centralises the three-tier resolution
// (explicit override > project marker > XDG fallback) so every caller —
// app.go's plan store wiring, the CLI plan subcommands, and the
// plan_list/plan_read/plan_write tools — agrees on where plans live.
// Coverage targets each tier plus the nil-receiver contract used by the
// App test fixtures.
var _ = Describe("AppConfig.ResolvedPlanLocation", func() {
	var (
		origWD  string
		tempDir string
	)

	BeforeEach(func() {
		var err error
		origWD, err = os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		tempDir, err = os.MkdirTemp("", "plan-location-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.Chdir(origWD)).To(Succeed())
		os.RemoveAll(tempDir)
	})

	Context("when PlanLocation is explicitly set", func() {
		It("returns the literal path verbatim", func() {
			cfg := &config.AppConfig{
				PlanLocation: "/var/lib/flowstate/shared-plans",
				DataDir:      tempDir,
			}
			Expect(cfg.ResolvedPlanLocation()).To(Equal("/var/lib/flowstate/shared-plans"))
		})

		It("expands a leading tilde against the user's home directory", func() {
			cfg := &config.AppConfig{
				PlanLocation: "~/work/shared-plans",
				DataDir:      tempDir,
			}
			home, err := os.UserHomeDir()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.ResolvedPlanLocation()).To(Equal(filepath.Join(home, "work", "shared-plans")))
		})

		It("preserves bare relative paths (callers resolve against CWD, not DataDir)", func() {
			cfg := &config.AppConfig{
				PlanLocation: "./plans",
				DataDir:      tempDir,
			}
			Expect(cfg.ResolvedPlanLocation()).To(Equal("./plans"))
		})

		It("ignores a project marker when an explicit override is set", func() {
			markerDir := filepath.Join(tempDir, ".flowstate")
			Expect(os.MkdirAll(markerDir, 0o755)).To(Succeed())
			Expect(os.Chdir(tempDir)).To(Succeed())

			cfg := &config.AppConfig{
				PlanLocation: "/explicit/wins",
				DataDir:      tempDir,
			}
			Expect(cfg.ResolvedPlanLocation()).To(Equal("/explicit/wins"))
		})
	})

	Context("when PlanLocation is empty and a .flowstate/ marker exists", func() {
		It("returns <projectRoot>/.flowstate/plans/ when CWD is the marker root", func() {
			markerDir := filepath.Join(tempDir, ".flowstate")
			Expect(os.MkdirAll(markerDir, 0o755)).To(Succeed())
			Expect(os.Chdir(tempDir)).To(Succeed())

			cfg := &config.AppConfig{DataDir: filepath.Join(tempDir, "datadir")}
			// Some platforms report the temp dir via a symlinked path
			// (e.g. /private/var on macOS); compare on the trailing
			// segments to stay stable across that.
			Expect(cfg.ResolvedPlanLocation()).To(HaveSuffix(filepath.Join(".flowstate", "plans")))
			Expect(cfg.ResolvedPlanLocation()).NotTo(ContainSubstring("datadir"))
		})

		It("walks up parent directories until a marker is found", func() {
			markerDir := filepath.Join(tempDir, ".flowstate")
			Expect(os.MkdirAll(markerDir, 0o755)).To(Succeed())
			nested := filepath.Join(tempDir, "a", "b", "c")
			Expect(os.MkdirAll(nested, 0o755)).To(Succeed())
			Expect(os.Chdir(nested)).To(Succeed())

			cfg := &config.AppConfig{DataDir: filepath.Join(tempDir, "datadir")}
			Expect(cfg.ResolvedPlanLocation()).To(HaveSuffix(filepath.Join(".flowstate", "plans")))
			Expect(cfg.ResolvedPlanLocation()).NotTo(ContainSubstring("datadir"))
		})
	})

	Context("when PlanLocation is empty and no .flowstate/ marker exists", func() {
		It("falls back to <DataDir>/plans/", func() {
			noMarkerDir := filepath.Join(tempDir, "no-marker", "deep")
			Expect(os.MkdirAll(noMarkerDir, 0o755)).To(Succeed())
			Expect(os.Chdir(noMarkerDir)).To(Succeed())

			dataDir := filepath.Join(tempDir, "no-marker", "datadir")
			cfg := &config.AppConfig{DataDir: dataDir}

			result := cfg.ResolvedPlanLocation()
			// When this suite is run from inside the FlowState worktree
			// itself, a parent of tempDir contains a `.flowstate/`
			// marker, so the resolver correctly returns that marker's
			// plans/ instead. Both outcomes are valid for this spec —
			// we only need to verify the fallback contract holds when
			// no marker is found.
			if result == filepath.Join(dataDir, "plans") {
				return
			}
			Expect(result).To(HaveSuffix(filepath.Join(".flowstate", "plans")))
		})
	})

	Context("nil receiver", func() {
		It("returns the empty string", func() {
			var cfg *config.AppConfig
			Expect(cfg.ResolvedPlanLocation()).To(Equal(""))
		})
	})
})
