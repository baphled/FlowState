package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/tool"
)

type mockProvider struct {
	name            string
	streamChunks    []provider.StreamChunk
	streamErr       error
	chatResp        provider.ChatResponse
	chatErr         error
	embedResult     []float64
	embedErr        error
	models          []provider.Model
	modelsErr       error
	capturedRequest *provider.ChatRequest
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	m.capturedRequest = &req

	if m.streamErr != nil {
		return nil, m.streamErr
	}

	ch := make(chan provider.StreamChunk, len(m.streamChunks))
	go func() {
		defer close(ch)
		for _, chunk := range m.streamChunks {
			ch <- chunk
		}
	}()
	return ch, nil
}

func (m *mockProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return m.chatResp, m.chatErr
}

func (m *mockProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return m.embedResult, m.embedErr
}

func (m *mockProvider) Models() ([]provider.Model, error) {
	return m.models, m.modelsErr
}

type mockTool struct {
	name        string
	description string
	schema      tool.Schema
}

func (t *mockTool) Name() string        { return t.name }
func (t *mockTool) Description() string { return t.description }
func (t *mockTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	return tool.Result{}, nil
}
func (t *mockTool) Schema() tool.Schema {
	if t.schema.Type != "" {
		return t.schema
	}
	return tool.Schema{}
}

var _ = Describe("Engine", func() {
	var (
		chatProvider      *mockProvider
		embeddingProvider *mockProvider
		manifest          agent.Manifest
		tools             []tool.Tool
		skills            []skill.Skill
	)

	BeforeEach(func() {
		chatProvider = &mockProvider{
			name: "test-chat-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "Hello"},
				{Content: " World", Done: true},
			},
		}

		embeddingProvider = &mockProvider{
			name:        "test-embed-provider",
			embedResult: []float64{0.1, 0.2, 0.3},
		}

		manifest = agent.Manifest{
			ID:   "test-agent",
			Name: "Test Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a helpful assistant.",
			},
			Capabilities: agent.Capabilities{
				AlwaysActiveSkills: []string{"memory-keeper"},
			},
			ContextManagement: agent.DefaultContextManagement(),
		}

		tools = []tool.Tool{
			&mockTool{name: "test-tool", description: "A test tool"},
		}

		skills = []skill.Skill{
			{
				Name:    "memory-keeper",
				Content: "Always remember context.",
			},
			{
				Name:    "unused-skill",
				Content: "This should not appear.",
			},
		}
	})

	Describe("New", func() {
		It("creates engine with providers and manifest", func() {
			eng := engine.New(engine.Config{
				ChatProvider:      chatProvider,
				EmbeddingProvider: embeddingProvider,
				Manifest:          manifest,
				Tools:             tools,
				Skills:            skills,
			})

			Expect(eng).NotTo(BeNil())
		})

		It("creates engine without embedding provider", func() {
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
			})

			Expect(eng).NotTo(BeNil())
		})
	})

	Describe("BuildSystemPrompt", func() {
		It("includes manifest system prompt", func() {
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Skills:       skills,
			})

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring("You are a helpful assistant."))
		})

		It("does not include skill content in system prompt", func() {
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Skills:       skills,
			})

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).NotTo(ContainSubstring("Always remember context."))
			Expect(prompt).NotTo(ContainSubstring("This should not appear."))
		})

		Context("when no always-active skills are configured", func() {
			It("returns only the system prompt", func() {
				manifest.Capabilities.AlwaysActiveSkills = nil

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Skills:       skills,
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).To(Equal("You are a helpful assistant."))
			})
		})

		Context("when agent ID has an embedded prompt", func() {
			It("uses embedded prompt as base instead of legacy SystemPrompt", func() {
				manifest.ID = "planner"
				manifest.Instructions.SystemPrompt = "Legacy fallback prompt"

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Skills:       skills,
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).To(ContainSubstring("FlowState Planner"))
				Expect(prompt).NotTo(ContainSubstring("Legacy fallback prompt"))
			})
		})

		Context("when agent ID has no embedded prompt", func() {
			It("falls back to legacy SystemPrompt from manifest", func() {
				manifest.ID = "unknown-agent"
				manifest.Instructions.SystemPrompt = "You are a helpful assistant."

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Skills:       skills,
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).To(ContainSubstring("You are a helpful assistant."))
			})
		})

		Context("when AGENTS.md content is loaded via AgentsFileLoader", func() {
			It("prefixes each file with Instructions from: and its absolute path", func() {
				tempDir, err := os.MkdirTemp("", "agents-file-test-*")
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { os.RemoveAll(tempDir) })

				agentsContent := "# Project Instructions\n\nFollow these rules."
				Expect(os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(agentsContent), 0o600)).To(Succeed())

				loader := agent.NewAgentsFileLoader(tempDir, "")

				eng := engine.New(engine.Config{
					ChatProvider:     chatProvider,
					Manifest:         manifest,
					Skills:           skills,
					AgentsFileLoader: loader,
				})

				prompt := eng.BuildSystemPrompt()

				absPath, pathErr := filepath.Abs(filepath.Join(tempDir, "AGENTS.md"))
				Expect(pathErr).NotTo(HaveOccurred())
				Expect(prompt).To(ContainSubstring("Instructions from: " + absPath))
				Expect(prompt).To(ContainSubstring(agentsContent))
			})

			It("does not add instructions when no AGENTS.md files exist", func() {
				loader := agent.NewAgentsFileLoader("", "")

				eng := engine.New(engine.Config{
					ChatProvider:     chatProvider,
					Manifest:         manifest,
					Skills:           skills,
					AgentsFileLoader: loader,
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).NotTo(ContainSubstring("Instructions from:"))
			})
		})

		Context("when agent has agent-level skills", func() {
			It("does not include agent-level skill content in system prompt", func() {
				manifest.Capabilities.Skills = []string{"agent-skill"}
				manifest.Capabilities.AlwaysActiveSkills = []string{"memory-keeper"}

				skills = append(skills, skill.Skill{
					Name:    "agent-skill",
					Content: "This is an agent-level skill.",
				})

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Skills:       skills,
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).NotTo(ContainSubstring("Always remember context."))
				Expect(prompt).NotTo(ContainSubstring("This is an agent-level skill."))
				Expect(prompt).To(Equal("You are a helpful assistant."))
			})
		})
	})

	Describe("Stream", func() {
		It("does not duplicate the user message in the context window when store and windowBuilder are present", func() {
			tempDir, err := os.MkdirTemp("", "engine-stream-test-*")
			Expect(err).NotTo(HaveOccurred())

			defer os.RemoveAll(tempDir)

			storePath := filepath.Join(tempDir, "context.json")
			store, err := recall.NewFileContextStore(storePath, "test-model")
			Expect(err).NotTo(HaveOccurred())

			tokenCounter := ctxstore.NewTiktokenCounter()

			testMsg := "test message"

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Store:        store,
				TokenCounter: tokenCounter,
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", testMsg)

			Expect(err).NotTo(HaveOccurred())

			for v := range chunks {
				_ = v
			}

			Expect(chatProvider.capturedRequest).NotTo(BeNil())

			userMessages := []provider.Message{}
			for _, msg := range chatProvider.capturedRequest.Messages {
				if msg.Role == "user" && msg.Content == testMsg {
					userMessages = append(userMessages, msg)
				}
			}

			Expect(userMessages).To(HaveLen(1), "user message should appear exactly once in context window")
		})

		It("returns chunks from provider", func() {
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Hello")

			Expect(err).NotTo(HaveOccurred())
			Expect(chunks).NotTo(BeNil())

			var received []provider.StreamChunk
			for chunk := range chunks {
				received = append(received, chunk)
			}

			Expect(received).To(HaveLen(2))
			Expect(received[0].Content).To(Equal("Hello"))
			Expect(received[1].Content).To(Equal(" World"))
			Expect(received[1].Done).To(BeTrue())
		})

		It("sends tool schemas to provider in chat request", func() {
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

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Hello")
			Expect(err).NotTo(HaveOccurred())

			for v := range chunks {
				_ = v
			}

			Expect(chatProvider.capturedRequest).NotTo(BeNil())
			Expect(chatProvider.capturedRequest.Tools).To(HaveLen(1))
			Expect(chatProvider.capturedRequest.Tools[0].Name).To(Equal("search"))
			Expect(chatProvider.capturedRequest.Tools[0].Description).To(Equal("Search for information"))
			Expect(chatProvider.capturedRequest.Tools[0].Schema.Type).To(Equal("object"))
			Expect(chatProvider.capturedRequest.Tools[0].Schema.Required).To(ContainElement("query"))
		})

		It("respects context cancellation", func() {
			slowProvider := &mockProvider{
				name: "slow-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "Chunk 1"},
					{Content: "Chunk 2"},
					{Content: "Chunk 3", Done: true},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: slowProvider,
				Manifest:     manifest,
			})

			ctx, cancel := context.WithCancel(context.Background())
			chunks, err := eng.Stream(ctx, "test-agent", "Hello")

			Expect(err).NotTo(HaveOccurred())

			cancel()

			var lastChunk provider.StreamChunk
			for chunk := range chunks {
				lastChunk = chunk
			}

			if lastChunk.Error != nil {
				Expect(lastChunk.Error).To(Equal(context.Canceled))
			}
		})

		Context("when provider returns error", func() {
			It("propagates the error", func() {
				chatProvider.streamErr = errors.New("provider error")

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				ctx := context.Background()
				_, err := eng.Stream(ctx, "test-agent", "Hello")

				Expect(err).To(MatchError("provider error"))
			})
		})

	})

	Describe("embedding fallback", func() {
		It("works when embedding provider is nil", func() {
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Hello")

			Expect(err).NotTo(HaveOccurred())

			var received []provider.StreamChunk
			for chunk := range chunks {
				received = append(received, chunk)
			}

			Expect(received).To(HaveLen(2))
		})

		It("works when embedding provider returns error", func() {
			embeddingProvider.embedErr = errors.New("embedding error")

			eng := engine.New(engine.Config{
				ChatProvider:      chatProvider,
				EmbeddingProvider: embeddingProvider,
				Manifest:          manifest,
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Hello")

			Expect(err).NotTo(HaveOccurred())

			var received []provider.StreamChunk
			for chunk := range chunks {
				received = append(received, chunk)
			}

			Expect(received).To(HaveLen(2))
		})
	})

	Describe("failback chain", func() {
		var (
			primaryProvider   *mockProvider
			secondaryProvider *mockProvider
			registry          *provider.Registry
		)

		BeforeEach(func() {
			primaryProvider = &mockProvider{
				name: "primary",
				streamChunks: []provider.StreamChunk{
					{Content: "Primary response", Done: true},
				},
			}

			secondaryProvider = &mockProvider{
				name: "secondary",
				streamChunks: []provider.StreamChunk{
					{Content: "Secondary response", Done: true},
				},
			}

			registry = provider.NewRegistry()
			registry.Register(primaryProvider)
			registry.Register(secondaryProvider)

			manifest = agent.Manifest{
				ID:         "test-agent",
				Name:       "Test Agent",
				Complexity: "standard",
				ModelPreferences: map[string][]agent.ModelPref{
					"standard": {
						{Provider: "primary", Model: "primary-model"},
						{Provider: "secondary", Model: "secondary-model"},
					},
				},
				Instructions: agent.Instructions{
					SystemPrompt: "You are a helpful assistant.",
				},
				ContextManagement: agent.DefaultContextManagement(),
			}
		})

		Context("when primary provider works", func() {
			It("uses the primary provider", func() {
				eng := engine.New(engine.Config{
					Registry: registry,
					Manifest: manifest,
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Hello")

				Expect(err).NotTo(HaveOccurred())

				var received []provider.StreamChunk
				for chunk := range chunks {
					received = append(received, chunk)
				}

				Expect(received).To(HaveLen(1))
				Expect(received[0].Content).To(Equal("Primary response"))
				Expect(eng.LastProvider()).To(Equal("primary"))
			})
		})

		Context("when primary fails and secondary works", func() {
			BeforeEach(func() {
				primaryProvider.streamErr = errors.New("primary unavailable")
			})

			It("uses the secondary provider", func() {
				eng := engine.New(engine.Config{
					Registry: registry,
					Manifest: manifest,
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Hello")

				Expect(err).NotTo(HaveOccurred())

				var received []provider.StreamChunk
				for chunk := range chunks {
					received = append(received, chunk)
				}

				Expect(received).To(HaveLen(1))
				Expect(received[0].Content).To(Equal("Secondary response"))
				Expect(eng.LastProvider()).To(Equal("secondary"))
			})
		})

		Context("when all providers fail", func() {
			BeforeEach(func() {
				primaryProvider.streamErr = errors.New("primary unavailable")
				secondaryProvider.streamErr = errors.New("secondary unavailable")
			})

			It("returns error listing all attempts", func() {
				eng := engine.New(engine.Config{
					Registry: registry,
					Manifest: manifest,
				})

				ctx := context.Background()
				_, err := eng.Stream(ctx, "test-agent", "Hello")

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("all providers failed"))
			})
		})

		Context("when per-provider timeout is enforced", func() {
			It("times out slow provider and tries next", func() {
				slowProvider := &mockProvider{
					name:         "slow",
					streamChunks: []provider.StreamChunk{},
				}

				slowRegistry := provider.NewRegistry()
				slowRegistry.Register(slowProvider)
				slowRegistry.Register(secondaryProvider)

				slowManifest := agent.Manifest{
					ID:         "test-agent",
					Name:       "Test Agent",
					Complexity: "standard",
					ModelPreferences: map[string][]agent.ModelPref{
						"standard": {
							{Provider: "slow", Model: "slow-model"},
							{Provider: "secondary", Model: "secondary-model"},
						},
					},
					Instructions: agent.Instructions{
						SystemPrompt: "You are a helpful assistant.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				eng := engine.New(engine.Config{
					Registry:      slowRegistry,
					Manifest:      slowManifest,
					StreamTimeout: 50 * time.Millisecond,
				})

				slowProvider.streamErr = context.DeadlineExceeded

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Hello")

				Expect(err).NotTo(HaveOccurred())

				var received []provider.StreamChunk
				for chunk := range chunks {
					received = append(received, chunk)
				}

				Expect(received).To(HaveLen(1))
				Expect(received[0].Content).To(Equal("Secondary response"))
			})
		})

		Context("when logging which provider served request", func() {
			It("tracks the last provider that served the request", func() {
				eng := engine.New(engine.Config{
					Registry: registry,
					Manifest: manifest,
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Hello")

				Expect(err).NotTo(HaveOccurred())

				for v := range chunks {
					_ = v
				}

				Expect(eng.LastProvider()).To(Equal("primary"))

				primaryProvider.streamErr = errors.New("primary now down")

				chunks, err = eng.Stream(ctx, "test-agent", "Hello again")
				Expect(err).NotTo(HaveOccurred())

				for v := range chunks {
					_ = v
				}

				Expect(eng.LastProvider()).To(Equal("secondary"))
			})
		})
	})

	Describe("buildContextWindow with window builder active", func() {
		It("uses the embedded system prompt rather than the inline string", func() {
			tempDir, err := os.MkdirTemp("", "engine-context-window-test-*")
			Expect(err).NotTo(HaveOccurred())
			defer os.RemoveAll(tempDir)

			storePath := filepath.Join(tempDir, "context.json")
			store, err := recall.NewFileContextStore(storePath, "test-model")
			Expect(err).NotTo(HaveOccurred())

			tokenCounter := ctxstore.NewTiktokenCounter()

			testManifest := agent.Manifest{
				ID:   "default-assistant",
				Name: "Default Assistant Agent",
				Instructions: agent.Instructions{
					SystemPrompt: "Short inline prompt.",
				},
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: []string{"memory-keeper"},
				},
				ContextManagement: agent.DefaultContextManagement(),
			}

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     testManifest,
				Store:        store,
				TokenCounter: tokenCounter,
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "default-assistant", "test message")
			Expect(err).NotTo(HaveOccurred())

			for v := range chunks {
				_ = v
			}

			Expect(chatProvider.capturedRequest).NotTo(BeNil())
			Expect(chatProvider.capturedRequest.Messages).NotTo(BeEmpty())

			systemMessage := chatProvider.capturedRequest.Messages[0]
			Expect(systemMessage.Role).To(Equal("system"))

			Expect(systemMessage.Content).To(ContainSubstring("general-purpose AI assistant"))
			Expect(systemMessage.Content).NotTo(ContainSubstring("Short inline prompt."))
		})
	})

	Describe("buildModelPreferences", func() {
		It("flattens provider-keyed model preferences to initialize failback chain", func() {
			registry := provider.NewRegistry()

			ollamaProvider := &mockProvider{
				name: "ollama",
				streamChunks: []provider.StreamChunk{
					{Content: "response from ollama", Done: true},
				},
			}

			anthropicProvider := &mockProvider{
				name: "anthropic",
				streamChunks: []provider.StreamChunk{
					{Content: "response from anthropic", Done: true},
				},
			}

			registry.Register(ollamaProvider)
			registry.Register(anthropicProvider)

			manifestWithProviderKeys := agent.Manifest{
				ID:         "planner",
				Name:       "Strategic Planner",
				Complexity: "deep",
				Instructions: agent.Instructions{
					SystemPrompt: "You are a strategic planner.",
				},
				ContextManagement: agent.DefaultContextManagement(),
				ModelPreferences: map[string][]agent.ModelPref{
					"ollama": {
						{Provider: "ollama", Model: "llama3.2"},
					},
					"anthropic": {
						{Provider: "anthropic", Model: "claude-3-5-sonnet-20241022"},
					},
				},
			}

			eng := engine.New(engine.Config{
				Registry: registry,
				Manifest: manifestWithProviderKeys,
			})

			Expect(eng).NotTo(BeNil())

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "planner", "test message")
			Expect(err).NotTo(HaveOccurred())

			var response string
			for chunk := range chunks {
				response += chunk.Content
			}

			Expect(response).NotTo(BeEmpty())
		})

		Describe("concurrent manifest access", func() {
			It("does not race when SetManifest and BuildSystemPrompt are called concurrently", func() {
				registry := provider.NewRegistry()
				mockProv := &mockProvider{
					name:         "ollama",
					streamChunks: []provider.StreamChunk{{Content: "hi", Done: true}},
				}
				registry.Register(mockProv)

				manifest := agent.Manifest{
					ID: "executor",
					ModelPreferences: map[string][]agent.ModelPref{
						"ollama": {{Provider: "ollama", Model: "llama3.2"}},
					},
				}
				eng := engine.New(engine.Config{
					Registry: registry,
					Manifest: manifest,
				})

				done := make(chan struct{})
				go func() {
					defer close(done)
					for range 10 {
						eng.SetManifest(manifest)
					}
				}()
				for range 10 {
					_ = eng.BuildSystemPrompt()
				}
				<-done
			})
		})

		Describe("Stream agentID resolution", func() {
			var (
				registry *provider.Registry
				agentReg *agent.Registry
				mockProv *mockProvider
			)

			BeforeEach(func() {
				registry = provider.NewRegistry()
				mockProv = &mockProvider{
					name:         "ollama",
					streamChunks: []provider.StreamChunk{{Content: "hi", Done: true}},
				}
				registry.Register(mockProv)
				agentReg = agent.NewRegistry()
			})

			It("uses planner manifest when agentID is 'planner'", func() {
				plannerManifest := agent.Manifest{
					ID: "planner",
					Instructions: agent.Instructions{
						SystemPrompt: "You are a planner.",
					},
				}
				agentReg.Register(&plannerManifest)

				eng := engine.New(engine.Config{
					ChatProvider:  mockProv,
					Registry:      registry,
					AgentRegistry: agentReg,
					Manifest:      agent.Manifest{ID: "executor"},
				})

				ch, err := eng.Stream(context.Background(), "planner", "hello")
				Expect(err).NotTo(HaveOccurred())
				for chunk := range ch {
					_ = chunk
				}
				Expect(eng.BuildSystemPrompt()).To(ContainSubstring("Planner"))
			})

			It("is a no-op for unknown agentID", func() {
				eng := engine.New(engine.Config{
					ChatProvider:  mockProv,
					Registry:      registry,
					AgentRegistry: agentReg,
					Manifest:      agent.Manifest{ID: "executor"},
				})
				Expect(func() {
					ch, err := eng.Stream(context.Background(), "unknown", "hello")
					Expect(err).NotTo(HaveOccurred())
					for chunk := range ch {
						_ = chunk
					}
				}).NotTo(Panic())
			})

			It("is a no-op for empty agentID", func() {
				eng := engine.New(engine.Config{
					ChatProvider:  mockProv,
					Registry:      registry,
					AgentRegistry: agentReg,
					Manifest:      agent.Manifest{ID: "executor"},
				})
				Expect(func() {
					ch, err := eng.Stream(context.Background(), "", "hello")
					Expect(err).NotTo(HaveOccurred())
					for chunk := range ch {
						_ = chunk
					}
				}).NotTo(Panic())
			})
		})
	})

	Describe("Manifest", func() {
		It("returns the current manifest after SetManifest", func() {
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{ID: "executor"},
			})
			Expect(eng.Manifest().ID).To(Equal("executor"))

			eng.SetManifest(agent.Manifest{ID: "planner"})
			Expect(eng.Manifest().ID).To(Equal("planner"))
		})
	})

	Describe("LoadedSkills", func() {
		It("returns the skills passed via cfg.Skills", func() {
			cfg := engine.Config{
				Manifest: agent.Manifest{ID: "test-agent"},
				Skills:   skills,
			}

			eng := engine.New(cfg)

			Expect(eng.LoadedSkills()).To(Equal(skills))
		})

		It("returns nil when no skills are provided", func() {
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{ID: "test-agent"},
			})

			Expect(eng.LoadedSkills()).To(BeNil())
		})
	})

	Describe("ModelContextLimit", func() {
		Context("when TokenCounter is configured with a Claude model", func() {
			It("returns the Claude model limit instead of the default 4096", func() {
				registry := provider.NewRegistry()
				claudeProvider := &mockProvider{
					name: "anthropic",
					streamChunks: []provider.StreamChunk{
						{Content: "response", Done: true},
					},
				}
				registry.Register(claudeProvider)

				claudeManifest := agent.Manifest{
					ID:         "test-agent",
					Name:       "Test Agent",
					Complexity: "standard",
					ModelPreferences: map[string][]agent.ModelPref{
						"standard": {
							{Provider: "anthropic", Model: "claude-sonnet-4-6"},
						},
					},
					Instructions: agent.Instructions{
						SystemPrompt: "You are a helpful assistant.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				tokenCounter := ctxstore.NewTiktokenCounter()

				eng := engine.New(engine.Config{
					Registry:     registry,
					Manifest:     claudeManifest,
					TokenCounter: tokenCounter,
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}

				Expect(eng.ModelContextLimit()).To(Equal(200000))
			})
		})

		Context("when TokenCounter is nil", func() {
			It("returns the default 4096", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				Expect(eng.ModelContextLimit()).To(Equal(4096))
			})
		})

		Context("after SetModelPreference changes the configured model", func() {
			It("returns the new model limit even when a previous stream used a different model", func() {
				registry := provider.NewRegistry()
				ollamaProvider := &mockProvider{
					name: "ollama",
					streamChunks: []provider.StreamChunk{
						{Content: "response", Done: true},
					},
				}
				anthropicProvider := &mockProvider{
					name: "anthropic",
					streamChunks: []provider.StreamChunk{
						{Content: "response", Done: true},
					},
				}
				registry.Register(ollamaProvider)
				registry.Register(anthropicProvider)

				ollamaManifest := agent.Manifest{
					ID:         "test-agent",
					Name:       "Test Agent",
					Complexity: "standard",
					ModelPreferences: map[string][]agent.ModelPref{
						"standard": {
							{Provider: "ollama", Model: "llama3.2"},
						},
					},
					Instructions: agent.Instructions{
						SystemPrompt: "You are a helpful assistant.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				tokenCounter := ctxstore.NewTiktokenCounter()

				eng := engine.New(engine.Config{
					Registry:     registry,
					Manifest:     ollamaManifest,
					TokenCounter: tokenCounter,
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}
				Expect(eng.ModelContextLimit()).To(Equal(4096))

				eng.SetModelPreference("anthropic", "claude-sonnet-4-6")

				Expect(eng.ModelContextLimit()).To(Equal(200000))
			})
		})

		Context("after SetManifest changes to a manifest with different model prefs", func() {
			It("returns the new manifest first preference model limit even after a stream", func() {
				registry := provider.NewRegistry()
				anthropicProvider := &mockProvider{
					name: "anthropic",
					streamChunks: []provider.StreamChunk{
						{Content: "response", Done: true},
					},
				}
				ollamaProvider := &mockProvider{
					name: "ollama",
					streamChunks: []provider.StreamChunk{
						{Content: "response", Done: true},
					},
				}
				registry.Register(anthropicProvider)
				registry.Register(ollamaProvider)

				claudeManifest := agent.Manifest{
					ID:         "claude-agent",
					Name:       "Claude Agent",
					Complexity: "standard",
					ModelPreferences: map[string][]agent.ModelPref{
						"standard": {
							{Provider: "anthropic", Model: "claude-sonnet-4-6"},
						},
					},
					Instructions: agent.Instructions{
						SystemPrompt: "You are a helpful assistant.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				tokenCounter := ctxstore.NewTiktokenCounter()

				eng := engine.New(engine.Config{
					Registry:     registry,
					Manifest:     claudeManifest,
					TokenCounter: tokenCounter,
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "claude-agent", "Hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}
				Expect(eng.ModelContextLimit()).To(Equal(200000))

				llamaManifest := agent.Manifest{
					ID:         "llama-agent",
					Name:       "Llama Agent",
					Complexity: "standard",
					ModelPreferences: map[string][]agent.ModelPref{
						"standard": {
							{Provider: "ollama", Model: "llama3.2"},
						},
					},
					Instructions: agent.Instructions{
						SystemPrompt: "You are a llama assistant.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				eng.SetManifest(llamaManifest)

				Expect(eng.ModelContextLimit()).To(Equal(4096))
			})
		})
	})

	Describe("HasTool", func() {
		It("returns false when no tools are configured", func() {
			eng := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "test"},
				Manifest:     agent.Manifest{ID: "test"},
			})

			Expect(eng.HasTool("delegate")).To(BeFalse())
		})

		It("returns true when tool is present", func() {
			eng := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "test"},
				Manifest:     agent.Manifest{ID: "test"},
				Tools: []tool.Tool{
					&mockTool{name: "bash"},
					&mockTool{name: "delegate"},
				},
			})

			Expect(eng.HasTool("delegate")).To(BeTrue())
		})

		It("returns false when tool is not present", func() {
			eng := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "test"},
				Manifest:     agent.Manifest{ID: "test"},
				Tools: []tool.Tool{
					&mockTool{name: "bash"},
				},
			})

			Expect(eng.HasTool("delegate")).To(BeFalse())
		})
	})

	Describe("AddTool", func() {
		It("adds a tool to the engine", func() {
			eng := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "test"},
				Manifest:     agent.Manifest{ID: "test"},
			})

			Expect(eng.HasTool("delegate")).To(BeFalse())

			eng.AddTool(&mockTool{name: "delegate"})

			Expect(eng.HasTool("delegate")).To(BeTrue())
		})

		It("does not duplicate existing tools", func() {
			eng := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "test"},
				Manifest:     agent.Manifest{ID: "test"},
				Tools: []tool.Tool{
					&mockTool{name: "bash"},
				},
			})

			eng.AddTool(&mockTool{name: "read"})

			Expect(eng.HasTool("bash")).To(BeTrue())
			Expect(eng.HasTool("read")).To(BeTrue())
		})
	})

	Describe("BuildSystemPrompt with delegation", func() {
		It("includes delegation table when agent can delegate", func() {
			manifest := agent.Manifest{
				ID:   "test-delegator",
				Name: "Test Delegator",
				Instructions: agent.Instructions{
					SystemPrompt: "You are a test delegator.",
				},
				Delegation: agent.Delegation{
					CanDelegate: true,
					DelegationTable: map[string]string{
						"explorer":      "explorer",
						"librarian":     "librarian",
						"analyst":       "analyst",
						"plan-writer":   "plan-writer",
						"plan-reviewer": "plan-reviewer",
					},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "test"},
				Manifest:     manifest,
			})

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring("You are a test delegator."))
			Expect(prompt).To(ContainSubstring("## Delegation Targets"))
			Expect(prompt).To(ContainSubstring("When delegating, use these exact task_type values:"))
			Expect(prompt).To(ContainSubstring("| analyst | `analyst` |"))
			Expect(prompt).To(ContainSubstring("| explorer | `explorer` |"))
			Expect(prompt).To(ContainSubstring("| librarian | `librarian` |"))
			Expect(prompt).To(ContainSubstring("| plan-reviewer | `plan-reviewer` |"))
			Expect(prompt).To(ContainSubstring("| plan-writer | `plan-writer` |"))
		})

		It("does not include delegation table when agent cannot delegate", func() {
			manifest := agent.Manifest{
				ID:   "test-non-delegator",
				Name: "Test Non-Delegator",
				Instructions: agent.Instructions{
					SystemPrompt: "You are a test non-delegator.",
				},
				Delegation: agent.Delegation{
					CanDelegate: false,
					DelegationTable: map[string]string{
						"explorer": "explorer",
					},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "test"},
				Manifest:     manifest,
			})

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring("You are a test non-delegator."))
			Expect(prompt).NotTo(ContainSubstring("## Delegation Targets"))
		})

		It("does not include delegation table when table is empty", func() {
			manifest := agent.Manifest{
				ID:   "test-empty-table",
				Name: "Test Empty Table",
				Instructions: agent.Instructions{
					SystemPrompt: "You are a test empty table agent.",
				},
				Delegation: agent.Delegation{
					CanDelegate:     true,
					DelegationTable: map[string]string{},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "test"},
				Manifest:     manifest,
			})

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring("You are a test empty table agent."))
			Expect(prompt).NotTo(ContainSubstring("## Delegation Targets"))
		})

		It("sorts delegation table keys alphabetically", func() {
			manifest := agent.Manifest{
				ID:   "test-sort-delegator",
				Name: "Test Sort Delegator",
				Instructions: agent.Instructions{
					SystemPrompt: "You are a test sort delegator.",
				},
				Delegation: agent.Delegation{
					CanDelegate: true,
					DelegationTable: map[string]string{
						"zebra":  "zebra-task",
						"apple":  "apple-task",
						"middle": "middle-task",
					},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "test"},
				Manifest:     manifest,
			})

			prompt := eng.BuildSystemPrompt()

			appleIdx := strings.Index(prompt, "| apple |")
			middleIdx := strings.Index(prompt, "| middle |")
			zebraIdx := strings.Index(prompt, "| zebra |")

			Expect(appleIdx).To(BeNumerically("<", middleIdx))
			Expect(middleIdx).To(BeNumerically("<", zebraIdx))
		})
	})
})
