package engine_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("Engine caching", func() {
	var (
		chatProvider *mockProvider
		manifest     agent.Manifest
	)

	BeforeEach(func() {
		chatProvider = &mockProvider{
			name: "test-chat-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "Hello", Done: true},
			},
		}

		manifest = agent.Manifest{
			ID:   "test-agent",
			Name: "Test Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a helpful assistant.",
			},
		}
	})

	Describe("BuildSystemPrompt caching", func() {
		It("returns the same result on consecutive calls without changes", func() {
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
			})

			first := eng.BuildSystemPrompt()
			second := eng.BuildSystemPrompt()

			Expect(first).To(Equal(second))
		})

		It("invalidates cache when SetManifest is called", func() {
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
			})

			first := eng.BuildSystemPrompt()
			Expect(first).To(ContainSubstring("You are a helpful assistant."))

			eng.SetManifest(agent.Manifest{
				ID:   "updated-agent",
				Name: "Updated Agent",
				Instructions: agent.Instructions{
					SystemPrompt: "You are a specialised agent.",
				},
			})

			second := eng.BuildSystemPrompt()
			Expect(second).To(ContainSubstring("You are a specialised agent."))
			Expect(second).NotTo(ContainSubstring("You are a helpful assistant."))
		})

		It("invalidates cache when SetAgentOverrides is called", func() {
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
			})

			first := eng.BuildSystemPrompt()
			Expect(first).NotTo(ContainSubstring("extra override"))

			eng.SetAgentOverrides(map[string]string{
				"test-agent": "extra override",
			})

			second := eng.BuildSystemPrompt()
			Expect(second).To(ContainSubstring("extra override"))
		})
	})

	Describe("buildToolSchemas caching", func() {
		It("returns consistent tool schemas on consecutive Stream calls", func() {
			toolWithSchema := &mockTool{
				name:        "search",
				description: "Search for information",
				schema: tool.Schema{
					Type: "object",
					Properties: map[string]tool.Property{
						"query": {Type: "string", Description: "Search query"},
					},
					Required: []string{"query"},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Tools:        []tool.Tool{toolWithSchema},
			})

			eng.BuildSystemPrompt()

			Expect(eng.HasTool("search")).To(BeTrue())
		})

		It("includes newly added tools after AddTool is called", func() {
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Tools:        []tool.Tool{&mockTool{name: "bash", description: "Run bash"}},
			})

			eng.AddTool(&mockTool{
				name:        "read",
				description: "Read files",
				schema: tool.Schema{
					Type: "object",
					Properties: map[string]tool.Property{
						"path": {Type: "string", Description: "File path"},
					},
					Required: []string{"path"},
				},
			})

			Expect(eng.HasTool("read")).To(BeTrue())
			Expect(eng.HasTool("bash")).To(BeTrue())
		})
	})

	Describe("agent file caching", func() {
		It("loads agent files once and caches the result", func() {
			tempDir, err := os.MkdirTemp("", "cache-agents-test-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(tempDir) })

			agentsContent := "# Cached Instructions\n\nFollow these cached rules."
			Expect(os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(agentsContent), 0o600)).To(Succeed())

			loader := agent.NewAgentsFileLoader(tempDir, "")

			eng := engine.New(engine.Config{
				ChatProvider:     chatProvider,
				Manifest:         manifest,
				AgentsFileLoader: loader,
			})

			first := eng.BuildSystemPrompt()
			Expect(first).To(ContainSubstring("Follow these cached rules."))

			second := eng.BuildSystemPrompt()
			Expect(second).To(Equal(first))
		})

		It("includes cached agent files in system prompt after SetManifest", func() {
			tempDir, err := os.MkdirTemp("", "cache-agents-setmanifest-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(tempDir) })

			agentsContent := "# Project Instructions\n\nAlways test first."
			Expect(os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(agentsContent), 0o600)).To(Succeed())

			loader := agent.NewAgentsFileLoader(tempDir, "")

			eng := engine.New(engine.Config{
				ChatProvider:     chatProvider,
				Manifest:         manifest,
				AgentsFileLoader: loader,
			})

			eng.BuildSystemPrompt()

			eng.SetManifest(agent.Manifest{
				ID:   "new-agent",
				Name: "New Agent",
				Instructions: agent.Instructions{
					SystemPrompt: "You are a new agent.",
				},
			})

			prompt := eng.BuildSystemPrompt()
			Expect(prompt).To(ContainSubstring("You are a new agent."))
			Expect(prompt).To(ContainSubstring("Always test first."))
		})
	})

	Describe("SetSkipAgentFiles", func() {
		It("excludes agent files from system prompt when enabled", func() {
			tempDir, err := os.MkdirTemp("", "skip-agents-test-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(tempDir) })

			agentsContent := "# Project Instructions\n\nFollow these project rules."
			Expect(os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(agentsContent), 0o600)).To(Succeed())

			loader := agent.NewAgentsFileLoader(tempDir, "")

			eng := engine.New(engine.Config{
				ChatProvider:     chatProvider,
				Manifest:         manifest,
				AgentsFileLoader: loader,
			})

			eng.SetSkipAgentFiles(true)

			prompt := eng.BuildSystemPrompt()
			Expect(prompt).To(ContainSubstring("You are a helpful assistant."))
			Expect(prompt).NotTo(ContainSubstring("Follow these project rules."))
		})

		It("includes agent files when skip is not set", func() {
			tempDir, err := os.MkdirTemp("", "no-skip-agents-test-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(tempDir) })

			agentsContent := "# Project Instructions\n\nInclude these rules."
			Expect(os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(agentsContent), 0o600)).To(Succeed())

			loader := agent.NewAgentsFileLoader(tempDir, "")

			eng := engine.New(engine.Config{
				ChatProvider:     chatProvider,
				Manifest:         manifest,
				AgentsFileLoader: loader,
			})

			prompt := eng.BuildSystemPrompt()
			Expect(prompt).To(ContainSubstring("Include these rules."))
		})

		It("invalidates cached system prompt when toggled", func() {
			tempDir, err := os.MkdirTemp("", "skip-toggle-test-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(tempDir) })

			agentsContent := "# Toggle Instructions\n\nToggleable content."
			Expect(os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(agentsContent), 0o600)).To(Succeed())

			loader := agent.NewAgentsFileLoader(tempDir, "")

			eng := engine.New(engine.Config{
				ChatProvider:     chatProvider,
				Manifest:         manifest,
				AgentsFileLoader: loader,
			})

			withFiles := eng.BuildSystemPrompt()
			Expect(withFiles).To(ContainSubstring("Toggleable content."))

			eng.SetSkipAgentFiles(true)
			withoutFiles := eng.BuildSystemPrompt()
			Expect(withoutFiles).NotTo(ContainSubstring("Toggleable content."))
			Expect(withoutFiles).NotTo(Equal(withFiles))
		})

		It("re-enables agent files when toggled back to false", func() {
			tempDir, err := os.MkdirTemp("", "skip-reenable-test-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(tempDir) })

			agentsContent := "# Re-enable Instructions\n\nRe-enabled content."
			Expect(os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(agentsContent), 0o600)).To(Succeed())

			loader := agent.NewAgentsFileLoader(tempDir, "")

			eng := engine.New(engine.Config{
				ChatProvider:     chatProvider,
				Manifest:         manifest,
				AgentsFileLoader: loader,
			})

			eng.SetSkipAgentFiles(true)
			skipped := eng.BuildSystemPrompt()
			Expect(skipped).NotTo(ContainSubstring("Re-enabled content."))

			eng.SetSkipAgentFiles(false)
			restored := eng.BuildSystemPrompt()
			Expect(restored).To(ContainSubstring("Re-enabled content."))
		})
	})
})
