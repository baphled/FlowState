package cli_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"runtime"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func testdataPath(subdir string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", subdir)
}

func createTestApp(agentsDir, skillsDir string) *app.App {
	tc := app.TestConfig{
		AgentsDir: agentsDir,
		SkillsDir: skillsDir,
	}
	testApp, err := app.NewForTest(tc)
	Expect(err).NotTo(HaveOccurred())
	return testApp
}

func createTestModelApp() *app.App {
	testApp := createTestApp("", "")
	testProvider := &testModelProvider{
		name: "test-provider",
		models: []provider.Model{
			{ID: "test-model", Provider: "test-provider", ContextLength: 4096},
		},
	}
	registry := provider.NewRegistry()
	registry.Register(testProvider)
	testApp.Engine = engine.New(engine.Config{
		ChatProvider: testProvider,
		Registry:     registry,
	})
	testApp.SetProviderRegistry(registry)
	return testApp
}

type testModelProvider struct {
	name   string
	models []provider.Model
}

func (p *testModelProvider) Name() string {
	return p.name
}
func (p *testModelProvider) Models() ([]provider.Model, error) {
	return p.models, nil
}
func (p *testModelProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errors.New("stream not implemented for test provider")
}
func (p *testModelProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (p *testModelProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

var _ = Describe("CLI Commands", func() {
	var (
		out *bytes.Buffer
		cmd = func(testApp *app.App, args ...string) error {
			root := cli.NewRootCmd(testApp)
			root.SetOut(out)
			root.SetErr(out)
			root.SetArgs(args)
			return root.Execute()
		}
	)

	BeforeEach(func() {
		out = new(bytes.Buffer)
	})

	Describe("root --help", func() {
		It("shows usage information", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("FlowState provides an AI assistant TUI"))
			Expect(out.String()).To(ContainSubstring("Available Commands"))
		})
	})

	Describe("root --version", func() {
		It("prints version information", func() {
			testApp := createTestApp("", "")
			root := cli.NewRootCmd(testApp)
			cli.SetVersion(root, "1.0.0", "abc123", "2026-03-18")
			root.SetOut(out)
			root.SetErr(out)
			root.SetArgs([]string{"--version"})
			err := root.Execute()
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("flowstate version"))
		})
	})

	Describe("agent list", func() {
		Context("with sample manifests", func() {
			It("prints agents from the agents directory", func() {
				testApp := createTestApp(testdataPath("agents"), "")
				err := cmd(testApp, "agent", "list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("test-coder"))
				Expect(out.String()).To(ContainSubstring("Test Coder"))
				Expect(out.String()).To(ContainSubstring("standard"))
				Expect(out.String()).To(ContainSubstring("test-researcher"))
			})
		})

		Context("with empty agents directory", func() {
			It("prints no agents found message", func() {
				testApp := createTestApp(testdataPath("empty"), "")
				err := cmd(testApp, "agent", "list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("No agents found"))
			})
		})
	})

	Describe("agent info", func() {
		It("prints JSON details for a named agent", func() {
			testApp := createTestApp(testdataPath("agents"), "")
			err := cmd(testApp, "agent", "info", "test-coder")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring(`"id": "test-coder"`))
			Expect(out.String()).To(ContainSubstring(`"name": "Test Coder"`))
		})

		It("returns error for unknown agent", func() {
			testApp := createTestApp(testdataPath("agents"), "")
			err := cmd(testApp, "agent", "info", "unknown")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(`agent "unknown" not found`))
		})
	})

	Describe("skill list", func() {
		Context("with sample skills", func() {
			It("prints skills from the skills directory", func() {
				testApp := createTestApp("", testdataPath("skills"))
				err := cmd(testApp, "skill", "list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("test-skill"))
				Expect(out.String()).To(ContainSubstring("core"))
				Expect(out.String()).To(ContainSubstring("A test skill for unit testing"))
			})
		})

		Context("with empty skills directory", func() {
			It("prints no skills found message", func() {
				testApp := createTestApp("", testdataPath("empty"))
				err := cmd(testApp, "skill", "list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("No skills found"))
			})
		})
	})

	Describe("discover", func() {
		It("returns suggestions for matching agents", func() {
			testApp := createTestApp(testdataPath("agents"), "")
			err := cmd(testApp, "discover", "write", "code")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("test-coder"))
			Expect(out.String()).To(ContainSubstring("confidence:"))
		})

		It("returns no matching agents message when none match", func() {
			testApp := createTestApp(testdataPath("agents"), "")
			err := cmd(testApp, "discover", "zzzznothing")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("No matching agents found"))
		})
	})

	Describe("session list", func() {
		It("prints placeholder message", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "session", "list")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(Equal("No sessions yet.\n"))
		})

		It("lists sessions with titles and message counts", func() {
			testApp := createTestApp("", "")
			sessDir := filepath.Join(GinkgoT().TempDir(), "sessions")
			sessStore, err := ctxstore.NewFileSessionStore(sessDir)
			Expect(err).NotTo(HaveOccurred())
			testApp.Sessions = sessStore

			store := recall.NewEmptyContextStore("")
			store.Append(provider.Message{
				Role:    "user",
				Content: "Hello session test",
			})
			meta := ctxstore.SessionMetadata{
				AgentID: "test-agent",
				Title:   "Test Session",
			}
			Expect(sessStore.Save("sess-0001", store, meta)).To(Succeed())

			out.Reset()
			err = cmd(testApp, "session", "list")
			Expect(err).NotTo(HaveOccurred())
			output := out.String()
			Expect(output).To(ContainSubstring("sess-000"))
			Expect(output).To(ContainSubstring("Test Session"))
		})

		It("uses truncated ID when session has no title", func() {
			testApp := createTestApp("", "")
			sessDir := filepath.Join(GinkgoT().TempDir(), "sessions")
			sessStore, err := ctxstore.NewFileSessionStore(sessDir)
			Expect(err).NotTo(HaveOccurred())
			testApp.Sessions = sessStore

			store := recall.NewEmptyContextStore("")
			Expect(sessStore.Save("abcdefghijklmnop", store, ctxstore.SessionMetadata{})).To(Succeed())

			out.Reset()
			err = cmd(testApp, "session", "list")
			Expect(err).NotTo(HaveOccurred())
			output := out.String()
			Expect(output).To(ContainSubstring("abcdefgh"))
		})
	})

	Describe("session resume", func() {
		It("returns error when session not found", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "session", "resume", "my-session-123")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(`session "my-session-123" not found`))
		})

		It("returns error when session not found among existing sessions", func() {
			testApp := createTestApp("", "")
			sessDir := filepath.Join(GinkgoT().TempDir(), "sessions")
			sessStore, err := ctxstore.NewFileSessionStore(sessDir)
			Expect(err).NotTo(HaveOccurred())
			testApp.Sessions = sessStore

			store := recall.NewEmptyContextStore("")
			store.Append(provider.Message{Role: "user", Content: "hello"})
			Expect(sessStore.Save("existing-session", store, ctxstore.SessionMetadata{
				AgentID: "test-agent",
				Title:   "Existing",
			})).To(Succeed())

			err = cmd(testApp, "session", "resume", "wrong-id")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(`session "wrong-id" not found`))
		})
	})

	Describe("root (no args)", func() {
		It("prints root stub with config info", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp)
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("root stub"))
			Expect(out.String()).To(ContainSubstring("config="))
		})
	})

	Describe("serve --help", func() {
		It("shows serve usage", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "serve", "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("Start the FlowState HTTP API server"))
			Expect(out.String()).To(ContainSubstring("--port"))
			Expect(out.String()).To(ContainSubstring("--host"))
		})
	})

	Describe("agent --help", func() {
		It("shows agent usage", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "agent", "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("Inspect available agents"))
		})
	})

	Describe("skill --help", func() {
		It("shows skill usage", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "skill", "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("skills available to FlowState"))
		})
	})

	Describe("session --help", func() {
		It("shows session usage", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "session", "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("Inspect saved sessions"))
		})
	})

	Describe("chat", func() {
		Context("without --message flag", func() {
			It("returns error when engine is not configured", func() {
				testApp := createTestApp("", "")
				err := cmd(testApp, "chat")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("engine not configured"))
			})
		})

		Context("with --message flag", func() {
			It("prints the agent and message with response placeholder when engine is nil", func() {
				testApp := createTestApp("", "")
				err := cmd(testApp, "chat", "--message", "Hello world", "--agent", "test-agent")
				Expect(err).To(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("[test-agent] Hello world"))
			})

			It("prints the agent message with default agent", func() {
				testApp := createTestApp("", "")
				err := cmd(testApp, "chat", "--message", "Hello")
				Expect(err).To(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("[executor] Hello"))
			})

			It("streams and returns response when engine is configured", func() {
				testApp := createRunTestApp([]provider.StreamChunk{
					{Content: "Hi there"},
					{Content: "!", Done: true},
				}, nil)
				err := cmd(testApp, "chat", "--message", "Hello", "--agent", "tester")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("[tester] Hello"))
				Expect(out.String()).To(ContainSubstring("Hi there!"))
			})

			It("generates a session ID when none provided", func() {
				testApp := createRunTestApp([]provider.StreamChunk{
					{Content: "ok", Done: true},
				}, nil)
				err := cmd(testApp, "chat", "--message", "test")
				Expect(err).NotTo(HaveOccurred())
			})

			It("uses provided session ID", func() {
				testApp := createRunTestApp([]provider.StreamChunk{
					{Content: "ok", Done: true},
				}, nil)
				err := cmd(testApp, "chat", "--message", "test", "--session", "my-sess")
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns stream error from engine", func() {
				testApp := createRunTestApp([]provider.StreamChunk{
					{Content: "partial"},
					{Error: errors.New("stream broke"), Done: true},
				}, nil)
				err := cmd(testApp, "chat", "--message", "test")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("stream error"))
			})

			It("returns error when provider fails to start stream", func() {
				testApp := createRunTestApp(nil, errors.New("provider down"))
				err := cmd(testApp, "chat", "--message", "test")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("streaming response"))
			})
		})

		Context("with --help flag", func() {
			It("shows chat usage", func() {
				testApp := createTestApp("", "")
				err := cmd(testApp, "chat", "--help")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("Start an interactive chat session"))
				Expect(out.String()).To(ContainSubstring("--message"))
				Expect(out.String()).To(ContainSubstring("--agent"))
				Expect(out.String()).To(ContainSubstring("--session"))
			})
		})
	})

	Describe("discover with no agents", func() {
		It("returns no agents available message", func() {
			testApp := createTestApp(testdataPath("empty"), "")
			err := cmd(testApp, "discover", "anything")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("No agents available"))
		})
	})

	Describe("discover --help", func() {
		It("shows discover usage", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "discover", "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("Suggest an agent"))
		})
	})

	Describe("skill add", func() {
		It("shows help with correct usage", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "skill", "add", "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("Import a skill from a GitHub"))
			Expect(out.String()).To(ContainSubstring("OWNER/REPO"))
		})

		It("returns error when import fails", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "skill", "add", "invalid/nonexistent-repo-xyz")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("importing skill"))
		})

		It("requires exactly one argument", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "skill", "add")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("models", func() {
		Context("with no providers configured", func() {
			It("prints no models available message", func() {
				testApp := createTestApp("", "")
				err := cmd(testApp, "models")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("No models available"))
			})
		})

		Context("with providers configured", func() {
			It("lists models with provider, ID, and context length", func() {
				testApp := createTestModelApp()
				err := cmd(testApp, "models")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("test-provider"))
				Expect(out.String()).To(ContainSubstring("test-model"))
				Expect(out.String()).To(ContainSubstring("4096"))
			})
		})

		Context("with multiple models from a provider", func() {
			It("lists all models", func() {
				testApp := createTestApp("", "")
				testProvider := &testModelProvider{
					name: "multi-provider",
					models: []provider.Model{
						{ID: "model-a", Provider: "multi-provider", ContextLength: 2048},
						{ID: "model-b", Provider: "multi-provider", ContextLength: 8192},
					},
				}
				registry := provider.NewRegistry()
				registry.Register(testProvider)
				testApp.Engine = engine.New(engine.Config{
					ChatProvider: testProvider,
					Registry:     registry,
				})
				testApp.SetProviderRegistry(registry)

				err := cmd(testApp, "models")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("model-a"))
				Expect(out.String()).To(ContainSubstring("model-b"))
				Expect(out.String()).To(ContainSubstring("2048"))
				Expect(out.String()).To(ContainSubstring("8192"))
			})
		})
	})

	Describe("auth anthropic --help", func() {
		It("shows usage for anthropic subcommand", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "auth", "anthropic", "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("Authenticate with Anthropic"))
			Expect(out.String()).To(ContainSubstring("API key"))
		})
	})

	Describe("config --help", func() {
		It("shows configuration management usage", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "config", "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("Configuration management"))
			Expect(out.String()).To(ContainSubstring("harness"))
		})
	})

	Describe("plan --help", func() {
		It("shows plan usage with subcommands", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "plan", "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("Create, list, select, and delete plans"))
			Expect(out.String()).To(ContainSubstring("list"))
			Expect(out.String()).To(ContainSubstring("select"))
			Expect(out.String()).To(ContainSubstring("delete"))
		})
	})

	Describe("root with flag overrides", func() {
		It("applies agents-dir override", func() {
			testApp := createTestApp("", "")
			overrideDir := GinkgoT().TempDir()
			err := cmd(testApp, "--agents-dir", overrideDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring(overrideDir))
		})

		It("applies skills-dir override", func() {
			testApp := createTestApp("", "")
			overrideDir := GinkgoT().TempDir()
			err := cmd(testApp, "--skills-dir", overrideDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring(overrideDir))
		})

		It("applies sessions-dir override", func() {
			testApp := createTestApp("", "")
			overrideDir := GinkgoT().TempDir()
			err := cmd(testApp, "--sessions-dir", overrideDir)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("chat with model flag", func() {
		It("returns error when model is invalid", func() {
			testApp := createRunTestApp([]provider.StreamChunk{
				{Content: "ok", Done: true},
			}, nil)
			err := cmd(testApp, "chat", "--message", "test", "--model", "nonexistent-model")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("setting model"))
		})
	})

	Describe("run with session persistence", func() {
		It("saves session when session store is available", func() {
			testApp := createRunTestApp([]provider.StreamChunk{
				{Content: "saved response", Done: true},
			}, nil)
			sessDir := filepath.Join(GinkgoT().TempDir(), "sessions")
			sessStore, err := ctxstore.NewFileSessionStore(sessDir)
			Expect(err).NotTo(HaveOccurred())
			testApp.Sessions = sessStore

			err = cmd(testApp, "run", "--prompt", "test save", "--session", "save-test")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("saved response"))
		})

		It("loads existing session when session ID is provided", func() {
			testApp := createRunTestApp([]provider.StreamChunk{
				{Content: "resumed", Done: true},
			}, nil)
			sessDir := filepath.Join(GinkgoT().TempDir(), "sessions")
			sessStore, err := ctxstore.NewFileSessionStore(sessDir)
			Expect(err).NotTo(HaveOccurred())
			testApp.Sessions = sessStore

			store := recall.NewEmptyContextStore("")
			store.Append(provider.Message{Role: "user", Content: "old message"})
			Expect(sessStore.Save("resume-test", store, ctxstore.SessionMetadata{
				AgentID: "worker",
			})).To(Succeed())

			err = cmd(testApp, "run", "--prompt", "continue", "--session", "resume-test")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("resumed"))
		})

		It("uses default agent name when agent flag is empty", func() {
			testApp := createRunTestApp([]provider.StreamChunk{
				{Content: "ok", Done: true},
			}, nil)
			err := cmd(testApp, "run", "--prompt", "test", "--agent", "", "--json")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("chat with session persistence", func() {
		It("saves session when session store is available", func() {
			testApp := createRunTestApp([]provider.StreamChunk{
				{Content: "chat saved", Done: true},
			}, nil)
			sessDir := filepath.Join(GinkgoT().TempDir(), "sessions")
			sessStore, err := ctxstore.NewFileSessionStore(sessDir)
			Expect(err).NotTo(HaveOccurred())
			testApp.Sessions = sessStore

			err = cmd(testApp, "chat", "--message", "test save", "--session", "chat-save")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("chat saved"))
		})

		It("loads existing session when session ID is provided", func() {
			testApp := createRunTestApp([]provider.StreamChunk{
				{Content: "chat resumed", Done: true},
			}, nil)
			sessDir := filepath.Join(GinkgoT().TempDir(), "sessions")
			sessStore, err := ctxstore.NewFileSessionStore(sessDir)
			Expect(err).NotTo(HaveOccurred())
			testApp.Sessions = sessStore

			store := recall.NewEmptyContextStore("")
			store.Append(provider.Message{Role: "user", Content: "previous chat"})
			Expect(sessStore.Save("chat-resume", store, ctxstore.SessionMetadata{
				AgentID: "test-agent",
			})).To(Succeed())

			err = cmd(testApp, "chat", "--message", "resume chat", "--session", "chat-resume")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("chat resumed"))
		})

		It("handles session load gracefully for non-existent session", func() {
			testApp := createRunTestApp([]provider.StreamChunk{
				{Content: "fresh chat", Done: true},
			}, nil)
			sessDir := filepath.Join(GinkgoT().TempDir(), "sessions")
			sessStore, err := ctxstore.NewFileSessionStore(sessDir)
			Expect(err).NotTo(HaveOccurred())
			testApp.Sessions = sessStore

			err = cmd(testApp, "chat", "--message", "test", "--session", "nonexistent-session")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("fresh chat"))
		})
	})

	Describe("run with session load for non-existent session", func() {
		It("proceeds without loading when session does not exist", func() {
			testApp := createRunTestApp([]provider.StreamChunk{
				{Content: "fresh run", Done: true},
			}, nil)
			sessDir := filepath.Join(GinkgoT().TempDir(), "sessions")
			sessStore, err := ctxstore.NewFileSessionStore(sessDir)
			Expect(err).NotTo(HaveOccurred())
			testApp.Sessions = sessStore

			err = cmd(testApp, "run", "--prompt", "test", "--session", "nonexistent")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("fresh run"))
		})
	})
})
