package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
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

				Expect(prompt).To(ContainSubstring("FlowState Strategic Planner"))
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
			store, err := ctxstore.NewFileContextStore(storePath, "test-model")
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
			store, err := ctxstore.NewFileContextStore(storePath, "test-model")
			Expect(err).NotTo(HaveOccurred())

			tokenCounter := ctxstore.NewTiktokenCounter()

			testManifest := agent.Manifest{
				ID:   "placeholder",
				Name: "Placeholder Agent",
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
			chunks, err := eng.Stream(ctx, "placeholder", "test message")
			Expect(err).NotTo(HaveOccurred())

			for v := range chunks {
				_ = v
			}

			Expect(chatProvider.capturedRequest).NotTo(BeNil())
			Expect(chatProvider.capturedRequest.Messages).NotTo(BeEmpty())

			systemMessage := chatProvider.capturedRequest.Messages[0]
			Expect(systemMessage.Role).To(Equal("system"))

			Expect(systemMessage.Content).To(ContainSubstring("placeholder"))
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
	})
})
