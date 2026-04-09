package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/mcp"
	pluginpkg "github.com/baphled/flowstate/internal/plugin"
)

type mockMCPClient struct {
	connectFn        func(ctx context.Context, cfg mcp.ServerConfig) error
	listToolsFn      func(ctx context.Context, serverName string) ([]mcp.ToolInfo, error)
	callToolFn       func(ctx context.Context, serverName, toolName string, args map[string]any) (*mcp.ToolResult, error)
	disconnectFn     func(serverName string) error
	disconnectAllFn  func() error
	connectCalls     []mcp.ServerConfig
	listToolCalls    []string
	disconnectCalled bool
}

func (m *mockMCPClient) Connect(ctx context.Context, cfg mcp.ServerConfig) error {
	m.connectCalls = append(m.connectCalls, cfg)
	if m.connectFn != nil {
		return m.connectFn(ctx, cfg)
	}
	return nil
}

func (m *mockMCPClient) Disconnect(serverName string) error {
	if m.disconnectFn != nil {
		return m.disconnectFn(serverName)
	}
	return nil
}

func (m *mockMCPClient) ListTools(ctx context.Context, serverName string) ([]mcp.ToolInfo, error) {
	m.listToolCalls = append(m.listToolCalls, serverName)
	if m.listToolsFn != nil {
		return m.listToolsFn(ctx, serverName)
	}
	return []mcp.ToolInfo{}, nil
}

func (m *mockMCPClient) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (*mcp.ToolResult, error) {
	if m.callToolFn != nil {
		return m.callToolFn(ctx, serverName, toolName, args)
	}
	return &mcp.ToolResult{}, nil
}

func (m *mockMCPClient) DisconnectAll() error {
	m.disconnectCalled = true
	if m.disconnectAllFn != nil {
		return m.disconnectAllFn()
	}
	return nil
}

var _ = Describe("App", func() {
	var tempDir string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "app-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("NewForTest", func() {
		It("creates app with minimal configuration", func() {
			tc := app.TestConfig{
				DataDir: tempDir,
			}

			application, err := app.NewForTest(tc)

			Expect(err).NotTo(HaveOccurred())
			Expect(application).NotTo(BeNil())
			Expect(application.Config).NotTo(BeNil())
			Expect(application.Registry).NotTo(BeNil())
			Expect(application.Sessions).NotTo(BeNil())
			Expect(application.Discovery).NotTo(BeNil())
		})

		It("uses temp directory when DataDir is empty", func() {
			tc := app.TestConfig{}

			application, err := app.NewForTest(tc)

			Expect(err).NotTo(HaveOccurred())
			Expect(application.Config.DataDir).To(Equal(os.TempDir()))
		})

		It("creates sessions directory under DataDir", func() {
			tc := app.TestConfig{
				DataDir: tempDir,
			}

			application, err := app.NewForTest(tc)

			Expect(err).NotTo(HaveOccurred())
			expectedSessionsDir := filepath.Join(tempDir, "sessions")
			Expect(application.SessionsDir()).To(Equal(expectedSessionsDir))
		})

		Context("with agents directory", func() {
			It("discovers agents from directory", func() {
				agentsDir := filepath.Join(tempDir, "agents")
				err := os.MkdirAll(agentsDir, 0o755)
				Expect(err).NotTo(HaveOccurred())

				agentContent := `{"id": "test-agent", "name": "Test Agent"}`
				err = os.WriteFile(filepath.Join(agentsDir, "test.json"), []byte(agentContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				tc := app.TestConfig{
					DataDir:   tempDir,
					AgentsDir: agentsDir,
				}

				application, err := app.NewForTest(tc)

				Expect(err).NotTo(HaveOccurred())
				agents := application.Registry.List()
				Expect(agents).To(HaveLen(1))
				Expect(agents[0].ID).To(Equal("test-agent"))
			})

			It("returns error for invalid agents directory", func() {
				tc := app.TestConfig{
					DataDir:   tempDir,
					AgentsDir: "/nonexistent/agents",
				}

				application, err := app.NewForTest(tc)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("discovering agents"))
				Expect(application).To(BeNil())
			})
		})

		Context("with skills directory", func() {
			It("loads skills from directory", func() {
				skillsDir := filepath.Join(tempDir, "skills")
				err := os.MkdirAll(skillsDir, 0o755)
				Expect(err).NotTo(HaveOccurred())

				skillDir := filepath.Join(skillsDir, "test-skill")
				err = os.MkdirAll(skillDir, 0o755)
				Expect(err).NotTo(HaveOccurred())

				skillContent := `# Skill: test-skill
Description: A test skill
When to use: Testing purposes
`
				err = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				tc := app.TestConfig{
					DataDir:   tempDir,
					SkillsDir: skillsDir,
				}

				application, err := app.NewForTest(tc)

				Expect(err).NotTo(HaveOccurred())
				Expect(application.Skills).NotTo(BeEmpty())
			})
		})

		It("sets Engine to nil for test instances", func() {
			tc := app.TestConfig{
				DataDir: tempDir,
			}

			application, err := app.NewForTest(tc)

			Expect(err).NotTo(HaveOccurred())
			Expect(application.Engine).To(BeNil())
		})

		It("sets API to nil for test instances", func() {
			tc := app.TestConfig{
				DataDir: tempDir,
			}

			application, err := app.NewForTest(tc)

			Expect(err).NotTo(HaveOccurred())
			Expect(application.API).To(BeNil())
		})
	})

	Describe("Config provider keys", func() {
		Context("when OPENAI_API_KEY env var is empty", func() {
			It("uses config file API key for OpenAI", func() {
				os.Unsetenv("OPENAI_API_KEY")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				tc := app.TestConfig{
					DataDir: tempDir,
				}
				application, err := app.NewForTest(tc)
				Expect(err).NotTo(HaveOccurred())

				application.Config.Providers.OpenAI.APIKey = "config-openai-key"
				registry, _ := app.RegisterProvidersForTest(application.Config)
				Expect(registry).NotTo(BeNil())
			})
		})

		Context("when ANTHROPIC_API_KEY env var is empty", func() {
			It("uses config file API key for Anthropic", func() {
				os.Unsetenv("ANTHROPIC_API_KEY")
				DeferCleanup(func() { os.Unsetenv("ANTHROPIC_API_KEY") })

				tc := app.TestConfig{
					DataDir: tempDir,
				}
				application, err := app.NewForTest(tc)
				Expect(err).NotTo(HaveOccurred())

				application.Config.Providers.Anthropic.APIKey = "config-anthropic-key"
				registry, _ := app.RegisterProvidersForTest(application.Config)
				Expect(registry).NotTo(BeNil())
			})
		})

		Context("when env vars take precedence", func() {
			It("uses OPENAI_API_KEY over config file", func() {
				os.Setenv("OPENAI_API_KEY", "env-openai-key")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				tc := app.TestConfig{
					DataDir: tempDir,
				}
				application, err := app.NewForTest(tc)
				Expect(err).NotTo(HaveOccurred())

				application.Config.Providers.OpenAI.APIKey = "config-openai-key"
				registry, _ := app.RegisterProvidersForTest(application.Config)
				Expect(registry).NotTo(BeNil())
			})
		})
	})

	Describe("Helper methods", func() {
		var application *app.App

		BeforeEach(func() {
			agentsDir := filepath.Join(tempDir, "agents")
			skillsDir := filepath.Join(tempDir, "skills")
			err := os.MkdirAll(agentsDir, 0o755)
			Expect(err).NotTo(HaveOccurred())
			err = os.MkdirAll(skillsDir, 0o755)
			Expect(err).NotTo(HaveOccurred())

			tc := app.TestConfig{
				DataDir:   tempDir,
				AgentsDir: agentsDir,
				SkillsDir: skillsDir,
			}
			application, err = app.NewForTest(tc)
			Expect(err).NotTo(HaveOccurred())
		})

		Describe("AgentsDir", func() {
			It("returns configured agents directory", func() {
				expectedDir := filepath.Join(tempDir, "agents")

				Expect(application.AgentsDir()).To(Equal(expectedDir))
			})
		})

		Describe("SkillsDir", func() {
			It("returns configured skills directory", func() {
				expectedDir := filepath.Join(tempDir, "skills")

				Expect(application.SkillsDir()).To(Equal(expectedDir))
			})
		})

		Describe("SessionsDir", func() {
			It("returns sessions directory under data dir", func() {
				expectedDir := filepath.Join(tempDir, "sessions")

				Expect(application.SessionsDir()).To(Equal(expectedDir))
			})
		})

		Describe("ConfigPath", func() {
			It("returns config path using ConfigDir()", func() {
				expectedPath := filepath.Join(config.Dir(), "config.yaml")

				Expect(application.ConfigPath()).To(Equal(expectedPath))
			})

			Context("when XDG_CONFIG_HOME is set", func() {
				It("returns config path in XDG_CONFIG_HOME", func() {
					xdgPath := filepath.Join(tempDir, "xdg-config")
					os.Setenv("XDG_CONFIG_HOME", xdgPath)
					DeferCleanup(func() { os.Unsetenv("XDG_CONFIG_HOME") })

					expectedPath := filepath.Join(xdgPath, "flowstate", "config.yaml")

					Expect(application.ConfigPath()).To(Equal(expectedPath))
				})
			})
		})
	})

	Describe("MCP wiring", func() {
		var client *mockMCPClient

		BeforeEach(func() {
			client = &mockMCPClient{}
		})

		Context("with enabled MCP servers in config", func() {
			It("registers MCP proxy tools in the tool slice", func() {
				client.listToolsFn = func(_ context.Context, _ string) ([]mcp.ToolInfo, error) {
					return []mcp.ToolInfo{
						{Name: "echo", Description: "Echoes input"},
						{Name: "fetch", Description: "Fetches URL"},
					}, nil
				}

				servers := []config.MCPServerConfig{
					{Name: "test-server", Command: "test-cmd", Enabled: true},
				}

				tools, results, _ := app.ConnectMCPServers(context.Background(), client, servers)

				Expect(tools).To(HaveLen(2))
				Expect(tools[0].Name()).To(Equal("echo"))
				Expect(tools[1].Name()).To(Equal("fetch"))
				Expect(client.connectCalls).To(HaveLen(1))
				Expect(client.connectCalls[0].Name).To(Equal("test-server"))
				Expect(results).To(HaveLen(1))
				Expect(results[0].Name).To(Equal("test-server"))
				Expect(results[0].Success).To(BeTrue())
				Expect(results[0].Error).To(BeEmpty())
				Expect(results[0].ToolCount).To(Equal(2))
			})
		})

		Context("with disabled MCP servers", func() {
			It("skips disabled servers", func() {
				servers := []config.MCPServerConfig{
					{Name: "disabled-server", Command: "test-cmd", Enabled: false},
				}

				tools, results, _ := app.ConnectMCPServers(context.Background(), client, servers)

				Expect(tools).To(BeEmpty())
				Expect(client.connectCalls).To(BeEmpty())
				Expect(results).To(BeEmpty())
			})
		})

		Context("when connection fails", func() {
			It("logs warning and continues without crashing", func() {
				client.connectFn = func(_ context.Context, cfg mcp.ServerConfig) error {
					if cfg.Name == "bad-server" {
						return errors.New("connection refused")
					}
					return nil
				}
				client.listToolsFn = func(_ context.Context, _ string) ([]mcp.ToolInfo, error) {
					return []mcp.ToolInfo{
						{Name: "good-tool", Description: "Works"},
					}, nil
				}

				servers := []config.MCPServerConfig{
					{Name: "bad-server", Command: "bad-cmd", Enabled: true},
					{Name: "good-server", Command: "good-cmd", Enabled: true},
				}

				tools, results, _ := app.ConnectMCPServers(context.Background(), client, servers)

				Expect(tools).To(HaveLen(1))
				Expect(tools[0].Name()).To(Equal("good-tool"))

				Expect(results).To(ContainElement(MatchFields(IgnoreExtras, Fields{
					"Name":    Equal("bad-server"),
					"Success": BeFalse(),
					"Error":   ContainSubstring("connection refused"),
				})))
				Expect(results).To(ContainElement(MatchFields(IgnoreExtras, Fields{
					"Name":      Equal("good-server"),
					"Success":   BeTrue(),
					"Error":     BeEmpty(),
					"ToolCount": Equal(1),
				})))
			})
		})

		Context("when ListTools fails", func() {
			It("logs warning and continues without crashing", func() {
				client.listToolsFn = func(_ context.Context, serverName string) ([]mcp.ToolInfo, error) {
					if serverName == "broken-server" {
						return nil, errors.New("list tools failed")
					}
					return []mcp.ToolInfo{
						{Name: "working-tool", Description: "Works"},
					}, nil
				}

				servers := []config.MCPServerConfig{
					{Name: "broken-server", Command: "cmd1", Enabled: true},
					{Name: "ok-server", Command: "cmd2", Enabled: true},
				}

				tools, results, _ := app.ConnectMCPServers(context.Background(), client, servers)

				Expect(tools).To(HaveLen(1))
				Expect(tools[0].Name()).To(Equal("working-tool"))
				Expect(results).To(HaveLen(2))
				// broken-server should be failure
				Expect(results[0].Name).To(Equal("broken-server"))
				Expect(results[0].Success).To(BeFalse())
				Expect(results[0].Error).To(ContainSubstring("list tools failed"))
				// ok-server should be success
				Expect(results[1].Name).To(Equal("ok-server"))
				Expect(results[1].Success).To(BeTrue())
				Expect(results[1].Error).To(BeEmpty())
				Expect(results[1].ToolCount).To(Equal(1))
			})
		})
	})

	Describe("DisconnectAll", func() {
		It("delegates to the MCP client", func() {
			client := &mockMCPClient{}

			application, err := app.NewForTest(app.TestConfig{
				DataDir:   tempDir,
				MCPClient: client,
			})
			Expect(err).NotTo(HaveOccurred())

			err = application.DisconnectAll()

			Expect(err).NotTo(HaveOccurred())
			Expect(client.disconnectCalled).To(BeTrue())
		})

		It("returns nil when no MCP client is set", func() {
			application, err := app.NewForTest(app.TestConfig{
				DataDir: tempDir,
			})
			Expect(err).NotTo(HaveOccurred())

			err = application.DisconnectAll()

			Expect(err).NotTo(HaveOccurred())
		})

		It("propagates error from MCP client", func() {
			client := &mockMCPClient{
				disconnectAllFn: func() error {
					return errors.New("disconnect failed")
				},
			}

			application, err := app.NewForTest(app.TestConfig{
				DataDir:   tempDir,
				MCPClient: client,
			})
			Expect(err).NotTo(HaveOccurred())

			err = application.DisconnectAll()

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("disconnect failed"))
		})
	})

	Describe("New", func() {
		Context("with valid configuration", func() {
			It("creates a fully wired app", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-for-coverage")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application).NotTo(BeNil())
				Expect(application.Engine).NotTo(BeNil())
				Expect(application.Config).To(Equal(cfg))
				Expect(application.Registry).NotTo(BeNil())
				Expect(application.Sessions).NotTo(BeNil())
				Expect(application.Discovery).NotTo(BeNil())
				Expect(application.API).NotTo(BeNil())
			})

			It("creates app with MCP servers configured", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-for-mcp")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir
				cfg.MCPServers = []config.MCPServerConfig{
					{Name: "disabled-server", Command: "cmd", Enabled: false},
				}

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application).NotTo(BeNil())
			})

			It("creates app with agents discovered from directory", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-agents")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				agentJSON := `{"id": "test-agent", "name": "Test Agent"}`
				Expect(os.WriteFile(
					filepath.Join(agentsDir, "test.json"),
					[]byte(agentJSON), 0o600,
				)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir
				cfg.DefaultAgent = "test-agent"

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application).NotTo(BeNil())
				agents := application.Registry.List()
				Expect(agents).To(HaveLen(8))
			})
		})

		Context("when default agent has can_delegate true", func() {
			It("includes delegate tool in engine", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-delegate")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				delegatingAgent := `{
					"id": "delegator",
					"name": "Delegating Agent",
					"delegation": {
						"can_delegate": true,
						"delegation_table": {
							"testing": "qa-agent"
						}
					}
				}`
				Expect(os.WriteFile(
					filepath.Join(agentsDir, "delegator.json"),
					[]byte(delegatingAgent), 0o600,
				)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir
				cfg.DefaultAgent = "delegator"

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application.Engine).NotTo(BeNil())
				Expect(application.Engine.HasTool("delegate")).To(BeTrue())
			})
		})

		Context("when default agent has can_delegate false", func() {
			It("does not include delegate tool in engine", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-no-delegate")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				nonDelegatingAgent := `{
					"id": "worker",
					"name": "Worker Agent",
					"delegation": {
						"can_delegate": false
					}
				}`
				Expect(os.WriteFile(
					filepath.Join(agentsDir, "worker.json"),
					[]byte(nonDelegatingAgent), 0o600,
				)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir
				cfg.DefaultAgent = "worker"

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application.Engine).NotTo(BeNil())
				Expect(application.Engine.HasTool("delegate")).To(BeFalse())
			})
		})

		Context("when default provider is not registered", func() {
			It("returns an error", func() {
				os.Unsetenv("OPENAI_API_KEY")
				os.Unsetenv("ANTHROPIC_API_KEY")
				DeferCleanup(func() {
					os.Unsetenv("OPENAI_API_KEY")
					os.Unsetenv("ANTHROPIC_API_KEY")
				})

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "nonexistent"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir
				cfg.Providers.OpenAI.APIKey = ""
				cfg.Providers.Anthropic.APIKey = ""

				application, err := app.New(cfg)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("getting default provider"))
				Expect(application).To(BeNil())
			})
		})

		Context("with plugin configuration", func() {
			It("wires plugin config into startup runtime", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-plugin-config")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir
				cfg.Plugins = config.PluginsConfig{
					Dir:     "/custom/plugins",
					Timeout: 9,
					Enabled: []string{"alpha"},
				}

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application).NotTo(BeNil())
				pluginCfg := application.PluginConfigForTest()
				Expect(pluginCfg.Dir).To(Equal("/custom/plugins"))
				Expect(pluginCfg.Timeout).To(Equal(9))
				Expect(pluginCfg.Enabled).To(Equal([]string{"alpha"}))
			})
		})
	})

	Describe("Context assembly hook wiring", func() {
		It("passes configured custom hooks into the engine", func() {
			customHook := func(context.Context, *pluginpkg.ContextAssemblyPayload) error { return nil }
			cfg := config.DefaultConfig()
			cfg.DataDir = tempDir
			cfg.ContextAssemblyHooks = []pluginpkg.ContextAssemblyHook{customHook}

			application, err := app.New(cfg)

			Expect(err).NotTo(HaveOccurred())
			Expect(application.Engine).NotTo(BeNil())
			hooks := reflect.ValueOf(application.Engine).Elem().FieldByName("contextAssemblyHooks")
			Expect(hooks.IsNil()).To(BeFalse())
			Expect(hooks.Len()).To(Equal(1))
		})

		It("leaves hooks nil when none are configured", func() {
			cfg := config.DefaultConfig()
			cfg.DataDir = tempDir
			cfg.ContextAssemblyHooks = nil

			application, err := app.New(cfg)

			Expect(err).NotTo(HaveOccurred())
			Expect(application.Engine).NotTo(BeNil())
			hooks := reflect.ValueOf(application.Engine).Elem().FieldByName("contextAssemblyHooks")
			Expect(hooks.IsNil()).To(BeTrue())
		})
	})

	Describe("Plugin startup wiring", func() {
		Context("when app starts with valid configuration", func() {
			It("creates event logger in plugin runtime", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-plugin-logger")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application).NotTo(BeNil())
				Expect(application.HasEventLogger()).To(BeTrue())
			})

			It("creates failover hook in plugin runtime", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-plugin-failover")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application.HasFailoverHook()).To(BeTrue())
			})

			It("creates dispatcher in plugin runtime", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-plugin-dispatcher")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application.HasDispatcher()).To(BeTrue())
			})

			It("includes failover hook in the hook chain", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-hook-chain")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir

				chainWithFailover := app.BuildHookChainForTestWithFailover(nil, func() agent.Manifest {
					return agent.Manifest{ID: "test"}
				})
				chainWithout := app.BuildHookChainForTest(nil, func() agent.Manifest {
					return agent.Manifest{ID: "test"}
				})

				Expect(chainWithFailover.Len()).To(Equal(chainWithout.Len() + 1))
			})

			It("event logger closes without error on shutdown", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-plugin-close")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application.ClosePlugins()).To(Succeed())
			})
		})
	})

	Describe("External plugin lifecycle", func() {
		Context("when app starts with a plugin directory", func() {
			It("discovers and starts external plugins at startup", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-external-discover")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				pluginsDir := filepath.Join(tempDir, "plugins")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(pluginsDir, 0o755)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir
				cfg.Plugins.Dir = pluginsDir

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application).NotTo(BeNil())
				Expect(application.ExternalPluginsStarted()).To(BeTrue())
			})
		})

		Context("when plugin directory does not exist", func() {
			It("continues startup without aborting", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-external-nodir")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir
				cfg.Plugins.Dir = "/nonexistent/plugins/dir"

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application).NotTo(BeNil())
				Expect(application.ExternalPluginsStarted()).To(BeTrue())
			})
		})

		Context("when external plugin fails to start", func() {
			It("does not abort FlowState", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-external-fail")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				pluginsDir := filepath.Join(tempDir, "plugins")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(pluginsDir, 0o755)).To(Succeed())

				badPluginDir := filepath.Join(pluginsDir, "bad-plugin")
				Expect(os.MkdirAll(badPluginDir, 0o755)).To(Succeed())
				manifestJSON := `{"name":"bad-plugin","version":"1.0.0","command":"/nonexistent/binary","hooks":["event"]}`
				Expect(os.WriteFile(
					filepath.Join(badPluginDir, "manifest.json"),
					[]byte(manifestJSON), 0o600,
				)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir
				cfg.Plugins.Dir = pluginsDir

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application).NotTo(BeNil())
				Expect(application.ExternalPluginsStarted()).To(BeTrue())
			})
		})

		Context("when app shuts down", func() {
			It("stops external plugin lifecycle on shutdown", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-external-stop")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				pluginsDir := filepath.Join(tempDir, "plugins")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(pluginsDir, 0o755)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir
				cfg.Plugins.Dir = pluginsDir

				application, err := app.New(cfg)
				Expect(err).NotTo(HaveOccurred())

				Expect(application.ClosePlugins()).To(Succeed())
			})
		})
	})

	Describe("RegisterProvidersForTest", func() {
		Context("when no API keys are provided anywhere", func() {
			It("returns a registry without OpenAI or Anthropic", func() {
				os.Unsetenv("OPENAI_API_KEY")
				os.Unsetenv("ANTHROPIC_API_KEY")
				DeferCleanup(func() {
					os.Unsetenv("OPENAI_API_KEY")
					os.Unsetenv("ANTHROPIC_API_KEY")
				})

				cfg := config.DefaultConfig()
				cfg.Providers.OpenAI.APIKey = ""
				cfg.Providers.Anthropic.APIKey = ""

				registry, _ := app.RegisterProvidersForTest(cfg)

				Expect(registry).NotTo(BeNil())
			})
		})

		Context("when Anthropic env var takes precedence", func() {
			It("uses ANTHROPIC_API_KEY over config file", func() {
				os.Setenv("ANTHROPIC_API_KEY", "env-anthropic-key")
				DeferCleanup(func() { os.Unsetenv("ANTHROPIC_API_KEY") })

				cfg := config.DefaultConfig()
				cfg.Providers.Anthropic.APIKey = "config-anthropic-key"

				registry, _ := app.RegisterProvidersForTest(cfg)

				Expect(registry).NotTo(BeNil())
			})
		})

		Context("when config provides Anthropic key only", func() {
			It("registers Anthropic from config", func() {
				os.Unsetenv("ANTHROPIC_API_KEY")
				DeferCleanup(func() { os.Unsetenv("ANTHROPIC_API_KEY") })

				cfg := config.DefaultConfig()
				cfg.Providers.Anthropic.APIKey = "config-only-key"

				registry, _ := app.RegisterProvidersForTest(cfg)

				Expect(registry).NotTo(BeNil())
			})
		})
	})

	Describe("OpenCode credential integration", func() {
		var tempDir string
		var originalHome string

		BeforeEach(func() {
			var err error
			tempDir, err = os.MkdirTemp("", "opencode-integration-test-*")
			Expect(err).NotTo(HaveOccurred())

			originalHome = os.Getenv("HOME")
			os.Setenv("HOME", tempDir)
		})

		AfterEach(func() {
			os.Setenv("HOME", originalHome)
			os.RemoveAll(tempDir)
		})

		Context("when OpenCode has Anthropic credentials but config and env do not", func() {
			It("registers Anthropic provider using OpenCode credentials", func() {
				os.Unsetenv("ANTHROPIC_API_KEY")
				DeferCleanup(func() { os.Unsetenv("ANTHROPIC_API_KEY") })

				opencodePath := filepath.Join(tempDir, ".local", "share", "opencode")
				Expect(os.MkdirAll(opencodePath, 0o755)).To(Succeed())
				authPath := filepath.Join(opencodePath, "auth.json")
				jsonContent := `{
  "anthropic": {
    "type": "oauth",
    "access": "sk-ant-oat01-test-token"
  }
}`
				Expect(os.WriteFile(authPath, []byte(jsonContent), 0o600)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Anthropic.APIKey = ""

				registry, _ := app.RegisterProvidersForTest(cfg)

				Expect(registry).NotTo(BeNil())
				provider, err := registry.Get("anthropic")
				Expect(err).NotTo(HaveOccurred())
				Expect(provider).NotTo(BeNil())
			})
		})

		Context("when OpenCode has GitHub Copilot credentials but config and env do not", func() {
			It("registers GitHub Copilot provider using OpenCode credentials", func() {
				os.Unsetenv("GITHUB_TOKEN")
				DeferCleanup(func() { os.Unsetenv("GITHUB_TOKEN") })

				opencodePath := filepath.Join(tempDir, ".local", "share", "opencode")
				Expect(os.MkdirAll(opencodePath, 0o755)).To(Succeed())
				authPath := filepath.Join(opencodePath, "auth.json")
				jsonContent := `{
  "github-copilot": {
    "type": "oauth",
    "access": "gho_test_access_token",
    "refresh": "gho_test_refresh_token",
    "expires": 0
  }
}`
				Expect(os.WriteFile(authPath, []byte(jsonContent), 0o600)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.GitHub.APIKey = ""

				registry, _ := app.RegisterProvidersForTest(cfg)

				Expect(registry).NotTo(BeNil())
				provider, err := registry.Get("github-copilot")
				Expect(err).NotTo(HaveOccurred())
				Expect(provider).NotTo(BeNil())
			})
		})

		Context("when OpenCode has both Anthropic and GitHub Copilot credentials", func() {
			It("registers both providers using OpenCode credentials", func() {
				os.Unsetenv("ANTHROPIC_API_KEY")
				os.Unsetenv("GITHUB_TOKEN")
				DeferCleanup(func() {
					os.Unsetenv("ANTHROPIC_API_KEY")
					os.Unsetenv("GITHUB_TOKEN")
				})

				opencodePath := filepath.Join(tempDir, ".local", "share", "opencode")
				Expect(os.MkdirAll(opencodePath, 0o755)).To(Succeed())
				authPath := filepath.Join(opencodePath, "auth.json")
				jsonContent := `{
  "anthropic": {
    "type": "oauth",
    "access": "sk-ant-oat01-test-token"
  },
  "github-copilot": {
    "type": "oauth",
    "access": "gho_test_access_token",
    "refresh": "gho_test_refresh_token",
    "expires": 0
  }
}`
				Expect(os.WriteFile(authPath, []byte(jsonContent), 0o600)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Anthropic.APIKey = ""
				cfg.Providers.GitHub.APIKey = ""

				registry, _ := app.RegisterProvidersForTest(cfg)

				Expect(registry).NotTo(BeNil())

				anthropicProvider, err := registry.Get("anthropic")
				Expect(err).NotTo(HaveOccurred())
				Expect(anthropicProvider).NotTo(BeNil())

				githubProvider, err := registry.Get("github-copilot")
				Expect(err).NotTo(HaveOccurred())
				Expect(githubProvider).NotTo(BeNil())
			})
		})
	})
})
