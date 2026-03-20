package cli_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"runtime"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
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
	})

	Describe("session resume", func() {
		It("returns error when session not found", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "session", "resume", "my-session-123")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(`session "my-session-123" not found`))
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
				Expect(out.String()).To(ContainSubstring("[default] Hello"))
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

	Describe("skill add", func() {
		It("shows help with correct usage", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "skill", "add", "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("Import a skill from a GitHub"))
			Expect(out.String()).To(ContainSubstring("OWNER/REPO"))
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
	})
})
