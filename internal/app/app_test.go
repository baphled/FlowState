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

		Describe("Startup orphan-tmp scan (P10 T1)", func() {
			It("removes leftover .events.jsonl.tmp files under the sessions directory", func() {
				sessionsDir := filepath.Join(tempDir, "sessions")
				Expect(os.MkdirAll(sessionsDir, 0o755)).To(Succeed())

				orphan := filepath.Join(sessionsDir, "crashed.events.jsonl.tmp")
				Expect(os.WriteFile(orphan, []byte("half-written"), 0o600)).To(Succeed())

				// Unrelated files in the same directory must remain untouched.
				unrelated := filepath.Join(sessionsDir, "keep.json")
				Expect(os.WriteFile(unrelated, []byte("{}"), 0o600)).To(Succeed())
				keepEvents := filepath.Join(sessionsDir, "keep.events.jsonl")
				Expect(os.WriteFile(keepEvents, []byte("{}\n"), 0o600)).To(Succeed())

				application, err := app.NewForTest(app.TestConfig{DataDir: tempDir})
				Expect(err).NotTo(HaveOccurred())
				Expect(application).NotTo(BeNil())

				Expect(orphan).NotTo(BeAnExistingFile(),
					"app startup must sweep orphan .events.jsonl.tmp files left behind "+
						"by a crashed compaction so they do not accumulate across runs")
				Expect(unrelated).To(BeAnExistingFile(),
					"the scan must only target .events.jsonl.tmp files and leave "+
						"unrelated session data alone")
				Expect(keepEvents).To(BeAnExistingFile(),
					"the scan must not delete live .events.jsonl files; only the "+
						".tmp sibling is orphaned")
			})

			It("tolerates a missing sessions directory", func() {
				// If the app has never run before, the sessions dir does not
				// exist yet when startup begins. The scan must not error —
				// NewFileSessionStore creates the directory, so the scan is
				// effectively a no-op on first boot.
				emptyDir := filepath.Join(tempDir, "fresh-install")
				application, err := app.NewForTest(app.TestConfig{DataDir: emptyDir})
				Expect(err).NotTo(HaveOccurred())
				Expect(application).NotTo(BeNil())
			})
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
				Expect(agents).To(HaveLen(embeddedAgentCount() + 1))
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

			It("surfaces the failure reason for the missing default provider", func() {
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
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir
				cfg.Providers.OpenAI.APIKey = ""
				cfg.Providers.Anthropic.APIKey = ""

				application, err := app.New(cfg)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("getting default provider"))
				Expect(err.Error()).To(ContainSubstring("openai"))
				// The reason for openai's failure should be visible to the user
				// so they can act without grepping the log file.
				Expect(err.Error()).To(Or(
					ContainSubstring("OPENAI_API_KEY"),
					ContainSubstring("api_key"),
					ContainSubstring("no API key"),
				))
				Expect(application).To(BeNil())
			})
		})

		Describe("RegisterProvidersWithFailuresForTest", func() {
			It("returns the failure reason for providers that did not register", func() {
				os.Unsetenv("OPENAI_API_KEY")
				os.Unsetenv("ANTHROPIC_API_KEY")
				DeferCleanup(func() {
					os.Unsetenv("OPENAI_API_KEY")
					os.Unsetenv("ANTHROPIC_API_KEY")
				})

				cfg := config.DefaultConfig()
				cfg.Providers.OpenAI.APIKey = ""
				cfg.Providers.Anthropic.APIKey = ""

				registry, _, failures := app.RegisterProvidersWithFailuresForTest(cfg)

				Expect(registry).NotTo(BeNil())
				Expect(failures).To(HaveKey("openai"))
				Expect(failures["openai"]).To(HaveOccurred())
				Expect(failures["openai"].Error()).NotTo(BeEmpty())
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
			// config.DefaultConfig pins Providers.Default to "anthropic"; the
			// test doubles in this suite do not register anthropic and the
			// sibling specs already switch to openai with a test API key
			// (see "wires plugin config into startup runtime" above).
			// Mirror that pattern so app.New succeeds and the assertion
			// actually reaches the contextAssemblyHooks field under test.
			os.Setenv("OPENAI_API_KEY", "test-key-hook-wiring-custom")
			DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })
			customHook := func(context.Context, *pluginpkg.ContextAssemblyPayload) error { return nil }
			cfg := config.DefaultConfig()
			cfg.Providers.Default = "openai"
			cfg.DataDir = tempDir
			cfg.ContextAssemblyHooks = []pluginpkg.ContextAssemblyHook{customHook}

			application, err := app.New(cfg)

			Expect(err).NotTo(HaveOccurred())
			Expect(application.Engine).NotTo(BeNil())
			hooks := reflect.ValueOf(application.Engine).Elem().FieldByName("contextAssemblyHooks")
			Expect(hooks.IsNil()).To(BeFalse())
			Expect(hooks.Len()).To(Equal(2))
		})

		It("includes only the recall hook when no custom hooks are configured", func() {
			// Same rationale as the preceding spec: the anthropic default
			// cannot resolve under the test doubles; using openai with a
			// throwaway key exercises the identical wiring path on a
			// provider the test fixtures register.
			os.Setenv("OPENAI_API_KEY", "test-key-hook-wiring-default")
			DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })
			cfg := config.DefaultConfig()
			cfg.Providers.Default = "openai"
			cfg.DataDir = tempDir
			cfg.ContextAssemblyHooks = nil

			application, err := app.New(cfg)

			Expect(err).NotTo(HaveOccurred())
			Expect(application.Engine).NotTo(BeNil())
			hooks := reflect.ValueOf(application.Engine).Elem().FieldByName("contextAssemblyHooks")
			Expect(hooks.IsNil()).To(BeFalse())
			Expect(hooks.Len()).To(Equal(1))
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

	Describe("OpenCode credential migration", func() {
		var tempDir string
		var originalHome string

		BeforeEach(func() {
			var err error
			tempDir, err = os.MkdirTemp("", "opencode-migration-test-*")
			Expect(err).NotTo(HaveOccurred())

			originalHome = os.Getenv("HOME")
			os.Setenv("HOME", tempDir)
		})

		AfterEach(func() {
			os.Setenv("HOME", originalHome)
			os.RemoveAll(tempDir)
		})

		Context("when OpenCode has Anthropic credentials but config and env do not", func() {
			It("does not register Anthropic from OpenCode auth.json", func() {
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
				_, err := registry.Get("anthropic")
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when OpenCode has GitHub Copilot credentials but config and env do not", func() {
			It("does not register GitHub Copilot from OpenCode auth.json", func() {
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
				_, err := registry.Get("github-copilot")
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when config supplies an Anthropic key alongside an OpenCode auth.json", func() {
			It("registers the provider from config and ignores OpenCode auth.json", func() {
				os.Unsetenv("ANTHROPIC_API_KEY")
				DeferCleanup(func() { os.Unsetenv("ANTHROPIC_API_KEY") })

				opencodePath := filepath.Join(tempDir, ".local", "share", "opencode")
				Expect(os.MkdirAll(opencodePath, 0o755)).To(Succeed())
				authPath := filepath.Join(opencodePath, "auth.json")
				jsonContent := `{
  "anthropic": {
    "type": "oauth",
    "access": "sk-ant-oat01-should-be-ignored"
  }
}`
				Expect(os.WriteFile(authPath, []byte(jsonContent), 0o600)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Anthropic.APIKey = "config-anthropic-key"

				registry, _ := app.RegisterProvidersForTest(cfg)

				Expect(registry).NotTo(BeNil())
				provider, err := registry.Get("anthropic")
				Expect(err).NotTo(HaveOccurred())
				Expect(provider).NotTo(BeNil())
			})
		})
	})
})

// Plan-location wiring integration: the production callers in app.go,
// internal/cli/plan.go, and the plan_list/plan_read/plan_write tools all
// resolve their target directory through cfg.ResolvedPlanLocation(). This
// spec exercises the App-level entrypoint with a `.flowstate/` marker in
// place and asserts the resolved path lands under the project marker —
// proving the resolver is actually consulted on the wired path. The
// resolver tiers themselves are covered exhaustively in
// internal/config/config_test.go.
var _ = Describe("Plan location wiring", func() {
	var (
		origWD  string
		tempDir string
	)

	BeforeEach(func() {
		var err error
		origWD, err = os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		tempDir, err = os.MkdirTemp("", "app-plan-location")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.Chdir(origWD)).To(Succeed())
		os.RemoveAll(tempDir)
	})

	It("resolves the App's plan location through cfg.ResolvedPlanLocation when a project marker exists", func() {
		markerDir := filepath.Join(tempDir, ".flowstate")
		Expect(os.MkdirAll(markerDir, 0o755)).To(Succeed())
		Expect(os.Chdir(tempDir)).To(Succeed())

		application, err := app.NewForTest(app.TestConfig{
			DataDir: filepath.Join(tempDir, "datadir"),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(application).NotTo(BeNil())

		// The wired callers in app.go (planStore setup) and the CLI
		// subcommands all build their plan path via this helper. Asserting
		// it returns the project-marker path here proves they will land
		// there rather than under DataDir.
		resolved := application.Config.ResolvedPlanLocation()
		Expect(resolved).To(HaveSuffix(filepath.Join(".flowstate", "plans")))
		Expect(resolved).NotTo(ContainSubstring("datadir"))
	})

	It("honours an explicit PlanLocation override on the App's config", func() {
		application, err := app.NewForTest(app.TestConfig{
			DataDir: filepath.Join(tempDir, "datadir"),
		})
		Expect(err).NotTo(HaveOccurred())
		application.Config.PlanLocation = "/custom/plans"

		Expect(application.Config.ResolvedPlanLocation()).To(Equal("/custom/plans"))
	})
})

// Auto-materialise on app startup is the seam that lets fresh users
// drive the FlowState memory MCP without first running `flowstate
// memory-tools install`. Without this, every fresh-machine bootstrap
// has to follow a documented two-step ritual; the auto-materialise
// makes the binary self-bootstrapping. Tests target the small helper
// `MaterialiseMemoryToolsOnStartup` so the wiring is verifiable in
// isolation from the full `app.New` pipeline (which needs a provider,
// agents dir, etc.).
var _ = Describe("MaterialiseMemoryToolsOnStartup", func() {
	var destDir string

	BeforeEach(func() {
		destDir = filepath.Join(GinkgoT().TempDir(), "memory-tools")
	})

	Context("when the destination directory is missing", func() {
		It("creates the directory and writes the embedded payload", func() {
			report := app.MaterialiseMemoryToolsOnStartup(destDir)

			Expect(destDir).To(BeADirectory())
			Expect(filepath.Join(destDir, "mcp-mem0-server")).To(BeAnExistingFile())
			Expect(filepath.Join(destDir, "mcp-mem0-server.js")).To(BeAnExistingFile())
			Expect(report).NotTo(BeEmpty(),
				"first-run materialise must report what it created so the caller can log it")

			var sawCreated bool
			for _, entry := range report {
				if entry.Status == app.MemoryToolStatusCreated {
					sawCreated = true
				}
			}
			Expect(sawCreated).To(BeTrue(),
				"first-run materialise must classify at least one file as Created so the wire-in can log only when bytes were actually written")
		})

		It("writes the wrapper with the executable bit set so PATH/discovery can invoke it", func() {
			_ = app.MaterialiseMemoryToolsOnStartup(destDir)

			info, err := os.Stat(filepath.Join(destDir, "mcp-mem0-server"))
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()&0o111).NotTo(BeZero(),
				"wrapper must be executable so the auto-discovered command is invokable")
		})
	})

	Context("when the embedded payload is already materialised byte-for-byte", func() {
		It("classifies every file as Unchanged on the second call", func() {
			first := app.MaterialiseMemoryToolsOnStartup(destDir)
			Expect(first).NotTo(BeEmpty())

			second := app.MaterialiseMemoryToolsOnStartup(destDir)

			Expect(second).To(HaveLen(len(first)))
			for _, entry := range second {
				Expect(entry.Status).To(Equal(app.MemoryToolStatusUnchanged),
					"steady-state runs must not rewrite or relog the materialised payload")
			}
		})
	})

	Context("when the destination directory cannot be created", func() {
		It("returns nil without panicking so app startup can proceed", func() {
			// A path under /dev/null cannot be a directory; MkdirAll
			// surfaces ENOTDIR. The helper must swallow this so a
			// permissions or disk-full failure on the install dir does
			// not abort `flowstate run`.
			report := app.MaterialiseMemoryToolsOnStartup("/dev/null/cannot-create-here")

			Expect(report).To(BeNil(),
				"materialise failures must not abort app startup; the user should still get a working FlowState")
		})
	})
})
