package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/swarm"
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
		for i := range m.streamChunks {
			ch <- m.streamChunks[i]
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

// mockResolver implements context.ModelResolver for tests.
type mockResolver struct {
	limits map[string]int
}

func (m *mockResolver) ResolveContextLength(providerName, model string) int {
	key := providerName + "/" + model
	if limit, ok := m.limits[key]; ok {
		return limit
	}
	return 0
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

		Context("when both HookChain and FailoverManager are provided", func() {
			var (
				registry *provider.Registry
				manager  *failover.Manager
				hookRan  bool
			)

			BeforeEach(func() {
				hookRan = false

				primaryProvider := &mockProvider{
					name: "primary",
					streamChunks: []provider.StreamChunk{
						{Content: "response", Done: true},
					},
				}

				registry = provider.NewRegistry()
				registry.Register(primaryProvider)

				health := failover.NewHealthManager()
				manager = failover.NewManager(registry, health, 5*time.Minute)
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "primary", Model: "primary-model"},
				})
			})

			It("preserves the full hook chain instead of replacing it", func() {
				markerHook := func(next hook.HandlerFunc) hook.HandlerFunc {
					return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
						hookRan = true
						return next(ctx, req)
					}
				}

				chain := hook.NewChain(markerHook, hook.LoggingHook())

				eng := engine.New(engine.Config{
					Registry:        registry,
					FailoverManager: manager,
					HookChain:       chain,
					Manifest:        manifest,
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Hello")
				Expect(err).NotTo(HaveOccurred())

				var received []provider.StreamChunk
				for chunk := range chunks {
					received = append(received, chunk)
				}

				Expect(received).NotTo(BeEmpty())
				Expect(hookRan).To(BeTrue())
			})
		})

		Context("when only FailoverManager is provided without HookChain", func() {
			It("creates a minimal failover stream hook chain", func() {
				primaryProvider := &mockProvider{
					name: "primary",
					streamChunks: []provider.StreamChunk{
						{Content: "response", Done: true},
					},
				}

				registry := provider.NewRegistry()
				registry.Register(primaryProvider)

				health := failover.NewHealthManager()
				manager := failover.NewManager(registry, health, 5*time.Minute)
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "primary", Model: "primary-model"},
				})

				eng := engine.New(engine.Config{
					Registry:        registry,
					FailoverManager: manager,
					Manifest:        manifest,
				})

				Expect(eng).NotTo(BeNil())

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Hello")
				Expect(err).NotTo(HaveOccurred())

				var received []provider.StreamChunk
				for chunk := range chunks {
					received = append(received, chunk)
				}

				Expect(received).NotTo(BeEmpty())
			})
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

		It("includes skill content in system prompt", func() {
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Skills:       skills,
			})

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring("Always remember context."))
			Expect(prompt).To(ContainSubstring("This should not appear."))
		})

		Context("with skill injection", func() {
			It("includes headings and content when two skills are configured", func() {
				twoSkills := []skill.Skill{
					{Name: "pre-action", Content: "PREFLIGHT content"},
					{Name: "memory-keeper", Content: "MEMORY content"},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Skills:       twoSkills,
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).To(ContainSubstring("# Skill: pre-action"))
				Expect(prompt).To(ContainSubstring("PREFLIGHT content"))
				Expect(prompt).To(ContainSubstring("# Skill: memory-keeper"))
				Expect(prompt).To(ContainSubstring("MEMORY content"))
			})

			It("includes heading and content when one skill is configured", func() {
				oneSkill := []skill.Skill{
					{Name: "scope-management", Content: "Manage scope effectively."},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Skills:       oneSkill,
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).To(ContainSubstring("# Skill: scope-management"))
				Expect(prompt).To(ContainSubstring("Manage scope effectively."))
			})

			It("does not include skill markers when Skills is nil", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Skills:       nil,
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).NotTo(ContainSubstring("# Skill:"))
			})
		})

		Context("when no always-active skills are configured", func() {
			It("returns only the system prompt", func() {
				manifest.Capabilities.AlwaysActiveSkills = nil

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).To(HavePrefix("You are a helpful assistant."))
			})
		})

		Context("when agent manifest has SystemPrompt populated from markdown", func() {
			It("uses manifest SystemPrompt as the base prompt", func() {
				manifest.ID = "planner"
				manifest.Instructions.SystemPrompt = "You are the FlowState Planner with comprehensive instructions."

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Skills:       skills,
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).To(ContainSubstring("You are the FlowState Planner with comprehensive instructions."))
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
			It("includes agent-level skill content in system prompt", func() {
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

				Expect(prompt).To(ContainSubstring("Always remember context."))
				Expect(prompt).To(ContainSubstring("This is an agent-level skill."))
				Expect(prompt).To(ContainSubstring("# Skill: agent-skill"))
			})
		})

		Context("temporal context", func() {
			It("includes the current date in the system prompt", func() {
				fixed := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Skills:       skills,
					NowFunc:      func() time.Time { return fixed },
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).To(ContainSubstring("## Temporal Context"))
				Expect(prompt).To(ContainSubstring("Today is 2026-05-02 (Saturday, UTC)"))
			})

			It("uses real time when NowFunc is not set", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Skills:       skills,
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).To(ContainSubstring("## Temporal Context"))
				Expect(prompt).To(ContainSubstring("Today is"))
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

			// Tool gating is fail-closed when capabilities.tools is empty,
			// so the manifest must explicitly allow "search" for the
			// schema to be forwarded to the provider.
			searchManifest := manifest
			searchManifest.Capabilities.Tools = []string{"search"}

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     searchManifest,
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

			It("publishes structured provider error data when available", func() {
				structuredErr := &provider.Error{
					HTTPStatus:  429,
					ErrorCode:   "1113",
					ErrorType:   provider.ErrorTypeBilling,
					Provider:    "test-chat-provider",
					Message:     "insufficient balance",
					IsRetriable: false,
				}
				chatProvider.streamErr = fmt.Errorf("wrapped provider error: %w", structuredErr)

				bus := eventbus.NewEventBus()
				captured := make(chan *events.ProviderErrorEvent, 1)
				bus.Subscribe(events.EventProviderError, func(event any) {
					providerEvent, ok := event.(*events.ProviderErrorEvent)
					if !ok {
						return
					}
					captured <- providerEvent
				})

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					EventBus:     bus,
					Manifest:     manifest,
				})

				ctx := context.Background()
				_, err := eng.Stream(ctx, "test-agent", "Hello")

				Expect(err).To(MatchError("wrapped provider error: provider test-chat-provider error [billing/1113 HTTP 429]: insufficient balance"))

				var event *events.ProviderErrorEvent
				Eventually(captured).Should(Receive(&event))
				Expect(event.Data.ErrorType).To(Equal(string(provider.ErrorTypeBilling)))
				Expect(event.Data.ErrorCode).To(Equal("1113"))
				Expect(event.Data.HTTPStatus).To(Equal(429))
				Expect(event.Data.IsRetriable).To(BeFalse())
			})
		})

		Context("session model/provider overrides via context", func() {
			It("uses context model and provider overrides when both are set", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				ctx := context.WithValue(context.Background(), session.ProviderOverrideKey{}, "anthropic")
				ctx = context.WithValue(ctx, session.ModelOverrideKey{}, "claude-opus-4.7")

				_, err := eng.Stream(ctx, "test-agent", "Hello")
				Expect(err).NotTo(HaveOccurred())

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				Expect(chatProvider.capturedRequest.Provider).To(Equal("anthropic"))
				Expect(chatProvider.capturedRequest.Model).To(Equal("claude-opus-4.7"))
			})

			It("uses only the provider override when model override is empty", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				ctx := context.WithValue(context.Background(), session.ProviderOverrideKey{}, "openai")

				_, err := eng.Stream(ctx, "test-agent", "Hello")
				Expect(err).NotTo(HaveOccurred())

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				Expect(chatProvider.capturedRequest.Provider).To(Equal("openai"))
				Expect(chatProvider.capturedRequest.Model).To(BeEmpty())
			})

			It("falls back to engine defaults when no context overrides are set", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				_, err := eng.Stream(context.Background(), "test-agent", "Hello")
				Expect(err).NotTo(HaveOccurred())

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				Expect(chatProvider.capturedRequest.Provider).To(Equal("test-chat-provider"))
				Expect(chatProvider.capturedRequest.Model).To(BeEmpty())
			})

			It("uses only the model override when provider override is empty", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				ctx := context.WithValue(context.Background(), session.ModelOverrideKey{}, "gpt-4o")

				_, err := eng.Stream(ctx, "test-agent", "Hello")
				Expect(err).NotTo(HaveOccurred())

				Expect(chatProvider.capturedRequest).NotTo(BeNil())
				Expect(chatProvider.capturedRequest.Model).To(Equal("gpt-4o"))
				Expect(chatProvider.capturedRequest.Provider).To(Equal("test-chat-provider"))
			})
		})

		// Pin the proactive context-window overflow check (matches OpenCode's
		// isOverflow gate, see compaction.ts:30-89). The engine must compare
		// estimated input tokens against the configured per-model limit BEFORE
		// flushing to the upstream provider, refuse the request when the input
		// exceeds the limit, and surface a structured critical error chunk that
		// tells the user how to recover. This closes the glm-4.6 saturation
		// failure mode where a 700KB tool-result wave + accumulated context
		// drove the model into reasoning-only "thought into the void" turns.
		Context("when input context exceeds the model limit", func() {
			It("under-budget passes through and calls the upstream provider", func() {
				// Regression guard: a normal-sized message must continue to
				// reach the provider. Token-budget gating must not over-fire.
				registry := provider.NewRegistry()
				registry.Register(chatProvider)
				health := failover.NewHealthManager()
				manager := failover.NewManager(registry, health, 5*time.Minute)
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "test-chat-provider", Model: "test-model"},
				})
				manager.SetContextFallback(100_000) // generous limit

				eng := engine.New(engine.Config{
					Registry:        registry,
					FailoverManager: manager,
					Manifest:        manifest,
					TokenCounter:    ctxstore.NewApproximateCounter(),
				})

				chunks, err := eng.Stream(context.Background(), "test-agent", "Hello")
				Expect(err).NotTo(HaveOccurred())
				for range chunks {
				}

				Expect(chatProvider.capturedRequest).NotTo(BeNil(),
					"under-budget request must reach the provider")
			})

			It("over-budget refuses to send and emits a critical context-window error chunk", func() {
				// The provider must NOT be called. The stream channel must
				// emit a single error chunk, classified as SeverityCritical
				// and carrying provider.ErrorTypeContextWindowExceeded so
				// the SSE consumer routes it to the persistent banner path.
				registry := provider.NewRegistry()
				registry.Register(chatProvider)
				health := failover.NewHealthManager()
				manager := failover.NewManager(registry, health, 5*time.Minute)
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "test-chat-provider", Model: "test-model"},
				})
				manager.SetContextFallback(10) // tiny limit — anything realistic overflows

				eng := engine.New(engine.Config{
					Registry:        registry,
					FailoverManager: manager,
					Manifest:        manifest,
					TokenCounter:    ctxstore.NewApproximateCounter(),
				})

				// Build a message large enough that even at 1 token ≈ 4 chars
				// the heuristic count exceeds the 10-token limit by an order
				// of magnitude.
				bigMsg := strings.Repeat("A bunch of words to push us over budget. ", 200)

				chunks, err := eng.Stream(context.Background(), "test-agent", bigMsg)
				Expect(err).NotTo(HaveOccurred(),
					"refusal is delivered as an error chunk, not a synchronous error")

				var received []provider.StreamChunk
				for chunk := range chunks {
					received = append(received, chunk)
				}

				Expect(chatProvider.capturedRequest).To(BeNil(),
					"upstream provider must not be called when over budget")

				Expect(received).NotTo(BeEmpty())
				var errChunk *provider.StreamChunk
				for i := range received {
					if received[i].Error != nil {
						errChunk = &received[i]
						break
					}
				}
				Expect(errChunk).NotTo(BeNil(), "expected an error chunk on the channel")
				Expect(provider.IsCriticalStreamError(errChunk.Error)).To(BeTrue(),
					"context-window overflow must classify as SeverityCritical")

				var pErr *provider.Error
				Expect(errors.As(errChunk.Error, &pErr)).To(BeTrue(),
					"error must wrap a *provider.Error so the SSE seam can branch on ErrorType")
				Expect(pErr.ErrorType).To(Equal(provider.ErrorTypeContextWindowExceeded))
			})

			It("error chunk carries user-actionable recovery copy", func() {
				// The user-visible message must hint at the recoverable
				// action (trim recent tool results, start a fresh session).
				// This is what the Vue CriticalErrorBanner renders verbatim.
				registry := provider.NewRegistry()
				registry.Register(chatProvider)
				health := failover.NewHealthManager()
				manager := failover.NewManager(registry, health, 5*time.Minute)
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "test-chat-provider", Model: "test-model"},
				})
				manager.SetContextFallback(10)

				eng := engine.New(engine.Config{
					Registry:        registry,
					FailoverManager: manager,
					Manifest:        manifest,
					TokenCounter:    ctxstore.NewApproximateCounter(),
				})

				bigMsg := strings.Repeat("Tool output line. ", 500)
				chunks, err := eng.Stream(context.Background(), "test-agent", bigMsg)
				Expect(err).NotTo(HaveOccurred())

				var errChunk *provider.StreamChunk
				for chunk := range chunks {
					if chunk.Error != nil {
						c := chunk
						errChunk = &c
						break
					}
				}
				Expect(errChunk).NotTo(BeNil())
				msg := errChunk.Error.Error()
				Expect(msg).To(MatchRegexp(`(?i)context.*(window|limit)`),
					"message must name the failure mode")
				Expect(msg).To(MatchRegexp(`(?i)(trim|fresh|start a new|recent tool)`),
					"message must hint at a recoverable user action")
			})

			DescribeTable("applies the per-model context limit",
				func(limit int, expectOverflow bool) {
					// Two distinct models with different limits drive the
					// same input through the gate. The same payload must
					// pass for the high-limit case and refuse for the
					// low-limit case — pinning that the limit is read
					// per-(provider, model), not hardcoded.
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})
					manager.SetContextFallback(limit)

					eng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest:        manifest,
						TokenCounter:    ctxstore.NewApproximateCounter(),
					})

					// 4_000-char message → heuristic ~1_000 tokens. Sits
					// above the small-model limit (200) and below the
					// large-model limit (200_000).
					msg := strings.Repeat("x", 4_000)
					chunks, _ := eng.Stream(context.Background(), "test-agent", msg)
					for range chunks {
					}

					if expectOverflow {
						Expect(chatProvider.capturedRequest).To(BeNil(),
							"limit %d should refuse a ~1k-token message", limit)
					} else {
						Expect(chatProvider.capturedRequest).NotTo(BeNil(),
							"limit %d should accept a ~1k-token message", limit)
					}
				},
				Entry("small-model limit (200) refuses", 200, true),
				Entry("large-model limit (200_000) passes", 200_000, false),
			)

			// Phase 2 — output reserve. Pre-this-fix the gate compared
			// estimated input tokens against the raw context limit, leaving
			// zero budget for the response when the input filled 100% of
			// the window. The model would either truncate immediately or
			// hang in reasoning-only "thought into the void" turns. Mirrors
			// OpenCode's `usable = input_limit - output_reserve` reference
			// at compaction.ts:30-39. Reserve formula:
			//   reserve = max(req.MaxTokens or 4096, 1024)
			//   usable  = max(1, limit - reserve)
			//   refuse if estimated > usable
			Context("output-reserve guard (Phase 2)", func() {
				It("refuses an input that fits the raw limit but exceeds limit-reserve", func() {
					// limit=5_000, no MaxTokens → reserve=4_096 →
					// usable=904. A ~1_500-token input was passing under
					// the old check (≤ limit) and now must refuse.
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})
					manager.SetContextFallback(5_000)

					eng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest:        manifest,
						TokenCounter:    ctxstore.NewApproximateCounter(),
					})

					// 6_000 chars → heuristic ~1_500 tokens. Sits above
					// usable (904) and below the raw limit (5_000).
					msg := strings.Repeat("x", 6_000)
					chunks, err := eng.Stream(context.Background(), "test-agent", msg)
					Expect(err).NotTo(HaveOccurred())
					for range chunks {
					}

					Expect(chatProvider.capturedRequest).To(BeNil(),
						"input fits raw limit (5_000) but exceeds limit-reserve (904) — must refuse")
				})

				It("preserves backward-compat under the new boundary (limit-reserve)", func() {
					// Regression guard: a small input well under
					// limit-reserve must continue to pass.
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})
					// limit=200_000, reserve=4_096 → usable=195_904. A
					// ~1k-token message still passes.
					manager.SetContextFallback(200_000)

					eng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest:        manifest,
						TokenCounter:    ctxstore.NewApproximateCounter(),
					})

					msg := strings.Repeat("x", 4_000)
					chunks, err := eng.Stream(context.Background(), "test-agent", msg)
					Expect(err).NotTo(HaveOccurred())
					for range chunks {
					}

					Expect(chatProvider.capturedRequest).NotTo(BeNil(),
						"under-limit-reserve input must pass through")
				})

				It("applies the reserve to the systemPromptBudget fallback when no failover manager is wired", func() {
					// Resolver fallback path: no FailoverManager → engine
					// uses systemPromptBudget. With cfg.SystemPromptBudget
					// =5_000 and reserve=4_096 the usable is 904, so a
					// ~1_500-token input refuses even via the fallback
					// path. Pre-this-fix the gate compared against the
					// raw fallback (5_000) and let it through.
					eng := engine.New(engine.Config{
						ChatProvider:       chatProvider,
						Manifest:           manifest,
						TokenCounter:       ctxstore.NewApproximateCounter(),
						SystemPromptBudget: 5_000,
					})

					msg := strings.Repeat("x", 6_000)
					chunks, err := eng.Stream(context.Background(), "test-agent", msg)
					Expect(err).NotTo(HaveOccurred())
					for range chunks {
					}

					Expect(chatProvider.capturedRequest).To(BeNil(),
						"fallback path must apply the same reserve — input fits raw 5_000 but exceeds usable 904")
				})

				It("clamps a small caller-supplied MaxTokens up to the 1024 floor", func() {
					// limit=5_000, MaxTokens=100 → reserve=max(100, 1024)
					// =1024 → usable=3_976. A ~5_000-token input must
					// still refuse — small MaxTokens cannot sneak through
					// by shrinking the reserve below the floor. The
					// MaxTokens=100 stamp arrives via applyCategoryParams
					// from a CategoryConfig with MaxTokens=100.
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})
					manager.SetContextFallback(5_000)

					mfst := manifest
					mfst.OrchestratorMeta = agent.OrchestratorMetadata{Category: "tiny"}
					resolver := engine.NewCategoryResolver(map[string]engine.CategoryConfig{
						"tiny": {MaxTokens: 100},
					})

					eng := engine.New(engine.Config{
						Registry:         registry,
						FailoverManager:  manager,
						Manifest:         mfst,
						TokenCounter:     ctxstore.NewApproximateCounter(),
						CategoryResolver: resolver,
					})

					// ~5_000 tokens, well above usable=3_976.
					msg := strings.Repeat("x", 20_000)
					chunks, err := eng.Stream(context.Background(), "test-agent", msg)
					Expect(err).NotTo(HaveOccurred())
					for range chunks {
					}

					Expect(chatProvider.capturedRequest).To(BeNil(),
						"small MaxTokens must clamp to the 1024 floor — large input must still refuse")
				})

				It("uses the 4096 default reserve when MaxTokens is zero", func() {
					// limit=5_000, MaxTokens=0 → reserve=4_096 → usable=904.
					// A ~1_500-token input refuses under the default
					// reserve. Sibling of case 1 — pins that default
					// reserve picks 4_096 not the 1024 floor.
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})
					manager.SetContextFallback(5_000)

					eng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest:        manifest,
						TokenCounter:    ctxstore.NewApproximateCounter(),
					})

					// ~250-token message — under usable(904), passes.
					small := strings.Repeat("x", 1_000)
					chunks, err := eng.Stream(context.Background(), "test-agent", small)
					Expect(err).NotTo(HaveOccurred())
					for range chunks {
					}
					Expect(chatProvider.capturedRequest).NotTo(BeNil(),
						"~250-token input must pass with default reserve")
				})
			})

			// Slice 1 (Phase-4 follow-ups) — per-model OutputLimit in the
			// provider registry tightens the Phase-2 reserve formula from
			//   reserve = max(req.MaxTokens or 4096, 1024)
			// to
			//   reserve = max(req.MaxTokens or model.OutputLimit, 1024)
			// so each (provider, model) declares its own reasonable output
			// budget rather than sharing a single hardcoded 4096 default.
			// Mirrors OpenCode's compaction.ts:30-39 which reads
			// `model.limit.output`. The resolver mirrors
			// Engine.ResolveContextLength: route through the failover
			// manager when wired, fall back to defaultModelOutputLimit
			// (=defaultOutputReserve=4096) otherwise so a missing-registry-
			// value yields no behaviour change vs the Phase-2 default.
			Context("output reserve sourced from per-model registry (Slice 1)", func() {
				It("uses model.OutputLimit when MaxTokens is unset", func() {
					// limit=10_000, OutputLimit=8192 → usable = 1_808.
					// Old default reserve=4096 → old usable = 5_904.
					// A ~3_000-token input PASSED under the old check
					// (3_000 ≤ 5_904) and now must REFUSE
					// (3_000 > 1_808). Pins the new formula precisely
					// rather than just "tighter than before".
					chatProvider.models = []provider.Model{
						{ID: "test-model", Provider: "test-chat-provider", ContextLength: 10_000, OutputLimit: 8192},
					}
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})

					eng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest:        manifest,
						TokenCounter:    ctxstore.NewApproximateCounter(),
					})

					// 12_000 chars → heuristic ~3_000 tokens. Sits above
					// new usable (1_808) and below old usable (5_904).
					msg := strings.Repeat("x", 12_000)
					chunks, err := eng.Stream(context.Background(), "test-agent", msg)
					Expect(err).NotTo(HaveOccurred())
					for range chunks {
					}

					Expect(chatProvider.capturedRequest).To(BeNil(),
						"OutputLimit=8192 → usable=1_808; ~3_000-token input must refuse "+
							"(would have passed under the old hardcoded 4096 default)")
				})

				It("falls back to defaultOutputReserve when the registry returns zero", func() {
					// Regression guard: a Models() entry that omits
					// OutputLimit (zero value) must yield the existing
					// 4096 fallback. Same scenario as the Phase-2 base
					// case: limit=5_000, reserve=4_096, usable=904; a
					// ~1_500-token input refuses under the default.
					chatProvider.models = []provider.Model{
						{ID: "test-model", Provider: "test-chat-provider", ContextLength: 5_000},
					}
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})

					eng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest:        manifest,
						TokenCounter:    ctxstore.NewApproximateCounter(),
					})

					msg := strings.Repeat("x", 6_000)
					chunks, err := eng.Stream(context.Background(), "test-agent", msg)
					Expect(err).NotTo(HaveOccurred())
					for range chunks {
					}

					Expect(chatProvider.capturedRequest).To(BeNil(),
						"OutputLimit=0 → fall back to default 4096 reserve; "+
							"~1_500-token input must still refuse against usable=904")
				})

				It("honours MaxTokens when stamped, ignoring registry OutputLimit", func() {
					// limit=10_000, OutputLimit=8192, MaxTokens=2048 →
					// reserve=max(2048, 1024)=2048 → usable=7_952. A
					// ~3_000-token input that REFUSED under the
					// OutputLimit-only path now PASSES because the
					// caller's MaxTokens stamp takes precedence over
					// the registry default. Pins MaxTokens-source
					// precedence: caller > registry > engine default.
					chatProvider.models = []provider.Model{
						{ID: "test-model", Provider: "test-chat-provider", ContextLength: 10_000, OutputLimit: 8192},
					}
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})

					mfst := manifest
					mfst.OrchestratorMeta = agent.OrchestratorMetadata{Category: "explicit-max"}
					resolver := engine.NewCategoryResolver(map[string]engine.CategoryConfig{
						"explicit-max": {MaxTokens: 2048},
					})

					eng := engine.New(engine.Config{
						Registry:         registry,
						FailoverManager:  manager,
						Manifest:         mfst,
						TokenCounter:     ctxstore.NewApproximateCounter(),
						CategoryResolver: resolver,
					})

					msg := strings.Repeat("x", 12_000)
					chunks, err := eng.Stream(context.Background(), "test-agent", msg)
					Expect(err).NotTo(HaveOccurred())
					for range chunks {
					}

					Expect(chatProvider.capturedRequest).NotTo(BeNil(),
						"MaxTokens=2048 must take precedence over registry OutputLimit=8192; "+
							"usable=7_952 → ~3_000-token input passes")
				})

				It("emits the registry-sourced output_reserve in the context_usage chunk", func() {
					// The chip must reflect the same reserve the gate
					// uses — drift between emitter and gate was the
					// failure mode the Phase-3 buildContextUsagePayload
					// extraction prevents. Slice 1 extends the contract
					// to the new resolver: when MaxTokens is unset and
					// the registry carries OutputLimit=8192, the chunk's
					// `output_reserve` field must be 8192, not 4096.
					chatProvider.models = []provider.Model{
						{ID: "test-model", Provider: "test-chat-provider", ContextLength: 100_000, OutputLimit: 8192},
					}
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})

					eng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest:        manifest,
						TokenCounter:    ctxstore.NewApproximateCounter(),
					})

					chunks, err := eng.Stream(context.Background(), "test-agent", "Hello")
					Expect(err).NotTo(HaveOccurred())

					var usage *provider.StreamChunk
					for chunk := range chunks {
						if chunk.EventType == "context_usage" && usage == nil {
							c := chunk
							usage = &c
						}
					}
					Expect(usage).NotTo(BeNil(),
						"a context_usage chunk must be emitted")

					var payload map[string]interface{}
					Expect(json.Unmarshal([]byte(usage.Content), &payload)).To(Succeed())

					Expect(payload["output_reserve"]).To(BeNumerically("==", 8_192),
						"context_usage payload must surface the registry-sourced reserve, not the 4096 default")
					Expect(payload["limit"]).To(BeNumerically("==", 100_000))
				})
			})

			// Slice 2 (Phase-4 follow-ups) — mid-stream tool-result-wave
			// saturation gate audit + regression spec. Both seams that
			// open a provider stream (`Stream` for the initial turn and
			// `retryStreamForToolResult` for the post-tool-result
			// continuation) flow through `Engine.streamFromProvider`,
			// which calls `checkContextWindowOverflow(req)` on the
			// post-tool-result message slice (engine.go:2593,3231). A
			// tool result large enough to push the assembled second
			// request over `limit-reserve` MUST refuse without invoking
			// the upstream provider a second time. This spec pins that
			// claim against future refactors that might split
			// streamFromProvider into per-seam paths and forget to copy
			// the gate.
			//
			// Audit verdict (recorded in
			// /home/baphled/vaults/baphled/1. Projects/FlowState/Bug Fixes/
			// Mid-Stream Tool-Result Overflow Gate Audit (May 2026).md):
			// gate-already-covers — single function, single gate, both
			// paths.
			Context("mid-stream tool-result wave (Slice 2)", func() {
				It("refuses an over-budget retry-with-tool-result before invoking the provider a second time", func() {
					// limit=5_000, no MaxTokens, no model OutputLimit →
					// reserve=4_096 → usable=904. First request ("Hello"
					// + tool schema) easily fits. The second request
					// adds a ~5_000-char tool-result Content (~1_250
					// tokens by the ApproximateCounter heuristic) on
					// top of the original user message and the
					// assistant tool_call envelope, which pushes the
					// estimate above usable=904. The gate must refuse
					// before streamSequenceProvider's second sequence
					// is consumed.
					seqProv := &streamSequenceProvider{
						name: "test-chat-provider",
						sequences: [][]provider.StreamChunk{
							{
								// Turn 1: assistant emits a tool call.
								{
									EventType: "tool_call",
									ToolCall: &provider.ToolCall{
										ID:        "call_slice2_overflow",
										Name:      "bulky_tool",
										Arguments: map[string]any{"q": "go"},
									},
								},
							},
							{
								// Turn 2: should never be consumed —
								// the gate must refuse before this
								// sequence is dispatched. If consumed,
								// the test detects it via
								// seqProv.callIndex on assertion.
								{Content: "should-not-arrive", Done: true},
							},
						},
					}

					// Tool result is large enough to push the second
					// request over usable=904. 5_000 chars → ~1_250
					// tokens via ApproximateCounter (1 token ≈ 4 chars).
					bulkyToolOutput := strings.Repeat("y", 5_000)
					bulkyTool := &executableMockTool{
						name:        "bulky_tool",
						description: "tool that returns a context-bloating payload",
						execResult:  tool.Result{Output: bulkyToolOutput},
					}

					registry := provider.NewRegistry()
					registry.Register(seqProv)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})
					// Tight limit so the tool-result wave reliably
					// pushes the assembled request over usable.
					manager.SetContextFallback(5_000)

					mfst := manifest
					mfst.Capabilities.Tools = []string{"bulky_tool"}

					eng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest:        mfst,
						Tools:           []tool.Tool{bulkyTool},
						TokenCounter:    ctxstore.NewApproximateCounter(),
					})

					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()

					chunks, err := eng.Stream(ctx, "test-agent", "Hello")
					Expect(err).NotTo(HaveOccurred(),
						"refusal is delivered as an error chunk, not a synchronous error")

					var received []provider.StreamChunk
					for chunk := range chunks {
						received = append(received, chunk)
					}

					// Provider must have been invoked exactly ONCE —
					// the first turn ran but the retry was refused by
					// the gate before reaching streamSequenceProvider's
					// second sequence.
					Expect(seqProv.callIndex).To(Equal(1),
						"provider.Stream must be invoked exactly once; "+
							"a callIndex of 2 means the gate let the over-budget retry through")

					// The tool was actually executed — the spec is
					// pinning the post-tool-result gate, not a
					// pre-tool-call one.
					Expect(bulkyTool.execCalled).To(BeTrue(),
						"the bulky tool must have been executed so the retry path was actually entered")

					// A critical context-window error chunk must land
					// on the channel, classified the same way as the
					// initial-turn refusal.
					var errChunk *provider.StreamChunk
					for i := range received {
						if received[i].Error != nil {
							c := received[i]
							errChunk = &c
							break
						}
					}
					Expect(errChunk).NotTo(BeNil(),
						"expected a context-window error chunk on the retry-refusal path")
					Expect(provider.IsCriticalStreamError(errChunk.Error)).To(BeTrue(),
						"mid-stream context-window overflow must classify as SeverityCritical")

					var pErr *provider.Error
					Expect(errors.As(errChunk.Error, &pErr)).To(BeTrue(),
						"error must wrap a *provider.Error so the SSE seam can branch on ErrorType")
					Expect(pErr.ErrorType).To(Equal(provider.ErrorTypeContextWindowExceeded))
					Expect(pErr.Message).To(MatchRegexp(`(?i)context.*(window|limit)`),
						"the canonical user-facing message must surface on the retry seam too")
				})
			})

			// Phase 2 — context_usage SSE event. The engine emits a
			// single context_usage chunk at the start of every Stream so
			// the chat UI can render a live usage chip. Shape:
			//   {type: "context_usage", input_tokens, output_reserve,
			//    limit, percentage, model, provider}
			// Forwarded ahead of any provider chunks (and ahead of the
			// gate refusal chunk on overflow) so the chip updates even
			// when the gate refuses.
			Context("context_usage event (Phase 2)", func() {
				It("emits a context_usage chunk with the computed shape on a passing request", func() {
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})
					manager.SetContextFallback(100_000)

					eng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest:        manifest,
						TokenCounter:    ctxstore.NewApproximateCounter(),
					})

					chunks, err := eng.Stream(context.Background(), "test-agent", "Hello")
					Expect(err).NotTo(HaveOccurred())

					var received []provider.StreamChunk
					for chunk := range chunks {
						received = append(received, chunk)
					}

					var usage *provider.StreamChunk
					for i := range received {
						if received[i].EventType == "context_usage" {
							c := received[i]
							usage = &c
							break
						}
					}
					Expect(usage).NotTo(BeNil(),
						"a context_usage chunk must be emitted on every Stream")

					var payload map[string]interface{}
					Expect(json.Unmarshal([]byte(usage.Content), &payload)).To(Succeed(),
						"context_usage chunk Content must be JSON")

					Expect(payload).To(HaveKey("input_tokens"))
					Expect(payload).To(HaveKey("output_reserve"))
					Expect(payload).To(HaveKey("limit"))
					Expect(payload).To(HaveKey("percentage"))
					Expect(payload).To(HaveKey("model"))
					Expect(payload).To(HaveKey("provider"))

					Expect(payload["limit"]).To(BeNumerically("==", 100_000))
					Expect(payload["output_reserve"]).To(BeNumerically("==", 4_096))
					Expect(payload["input_tokens"]).To(BeNumerically(">", 0))
					Expect(payload["percentage"]).To(BeNumerically(">=", 0))
					Expect(payload["provider"]).To(Equal("test-chat-provider"))
					Expect(payload["model"]).To(Equal("test-model"))
				})

				It("emits the context_usage chunk before the gate refusal error chunk on overflow", func() {
					// Wire ordering pin: when the gate refuses, the chip
					// still needs to update (so the user sees they hit
					// 100%+). The usage chunk MUST arrive before the
					// refusal error chunk on outChan.
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})
					manager.SetContextFallback(10)

					eng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest:        manifest,
						TokenCounter:    ctxstore.NewApproximateCounter(),
					})

					bigMsg := strings.Repeat("words ", 500)
					chunks, err := eng.Stream(context.Background(), "test-agent", bigMsg)
					Expect(err).NotTo(HaveOccurred())

					var usageIdx, errIdx = -1, -1
					i := 0
					for chunk := range chunks {
						if chunk.EventType == "context_usage" && usageIdx == -1 {
							usageIdx = i
						}
						if chunk.Error != nil && errIdx == -1 {
							errIdx = i
						}
						i++
					}
					Expect(usageIdx).NotTo(Equal(-1), "context_usage chunk must be emitted on overflow path")
					Expect(errIdx).NotTo(Equal(-1), "refusal error chunk must be emitted")
					Expect(usageIdx).To(BeNumerically("<", errIdx),
						"context_usage must arrive before the refusal chunk")
				})

				It("does not emit a context_usage chunk when no limit is known (no counter)", func() {
					// Sibling guard: when tokenCounter is unwired the
					// gate is a no-op AND no usage chunk fires (we have
					// no input_tokens to report).
					eng := engine.New(engine.Config{
						ChatProvider: chatProvider,
						Manifest:     manifest,
						// No TokenCounter wired
					})

					chunks, err := eng.Stream(context.Background(), "test-agent", "Hello")
					Expect(err).NotTo(HaveOccurred())

					for chunk := range chunks {
						Expect(chunk.EventType).NotTo(Equal("context_usage"),
							"no usage event when token counter is unwired")
					}
				})
			})

			// Phase 3 — TUI-cadence parity. The TUI's StatusBar reads
			// LastContextResult().TokensUsed on every redraw so the chip
			// reflects the *current* state at all times. The web chip
			// previously hid until the first pre-send event landed; the
			// engine now emits a fresh context_usage chunk after each
			// completed turn so the chip ticks up to reflect the
			// just-extended message history (no waiting for the next
			// pre-send to see the cost of the last reply).
			Context("post-turn context_usage emission (Phase 3)", func() {
				It("emits a fresh context_usage chunk before the terminal Done chunk", func() {
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})
					manager.SetContextFallback(100_000)

					eng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest:        manifest,
						TokenCounter:    ctxstore.NewApproximateCounter(),
					})

					chunks, err := eng.Stream(context.Background(), "test-agent", "Hello world")
					Expect(err).NotTo(HaveOccurred())

					var received []provider.StreamChunk
					for chunk := range chunks {
						received = append(received, chunk)
					}

					// At minimum we expect TWO context_usage chunks:
					// one pre-send (Phase 2) and one post-turn (Phase 3).
					var usageIdxs []int
					var doneIdx = -1
					for i := range received {
						if received[i].EventType == "context_usage" {
							usageIdxs = append(usageIdxs, i)
						}
						if received[i].Done && doneIdx == -1 {
							doneIdx = i
						}
					}
					Expect(len(usageIdxs)).To(BeNumerically(">=", 2),
						"expected at least two context_usage chunks (pre-send + post-turn); got %d", len(usageIdxs))
					Expect(doneIdx).NotTo(Equal(-1), "Done chunk must be emitted")

					// The post-turn usage chunk MUST land before Done so
					// SSE consumers (which return on Done) actually see
					// it.
					lastUsageIdx := usageIdxs[len(usageIdxs)-1]
					Expect(lastUsageIdx).To(BeNumerically("<", doneIdx),
						"post-turn context_usage must precede Done so the chip updates before stream-close")
				})

				It("preserves the pre-send context_usage emission as the first artefact", func() {
					// Regression guard: Phase 2's wire-ordering pin
					// (pre-send usage BEFORE provider chunks) must
					// survive Phase 3's additional emission.
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})
					manager.SetContextFallback(100_000)

					eng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest:        manifest,
						TokenCounter:    ctxstore.NewApproximateCounter(),
					})

					chunks, err := eng.Stream(context.Background(), "test-agent", "Hello")
					Expect(err).NotTo(HaveOccurred())

					var received []provider.StreamChunk
					for chunk := range chunks {
						received = append(received, chunk)
					}
					Expect(received).NotTo(BeEmpty())
					Expect(received[0].EventType).To(Equal("context_usage"),
						"pre-send context_usage must remain the first artefact")
				})
			})

			// Phase 3 — session-load + agent/model-switch parity. The
			// engine exposes a public helper that the api server uses
			// to compute the current usage shape on demand (session
			// SSE-connect, agent PATCH, model PATCH). Same payload
			// shape as the streamed chunk so the frontend dispatches
			// once.
			Context("ContextUsageJSONForSession (Phase 3 helper)", func() {
				It("returns the JSON payload and hasUsage=true when the engine can compute the figure", func() {
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})
					manager.SetContextFallback(100_000)

					eng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest:        manifest,
						TokenCounter:    ctxstore.NewApproximateCounter(),
					})

					payload, ok := eng.ContextUsageJSONForSession(
						"test-chat-provider", "test-model",
						[]provider.Message{{Role: "user", Content: "hello"}},
					)
					Expect(ok).To(BeTrue(), "engine has a counter and a known limit; helper must compute usage")
					Expect(payload).NotTo(BeEmpty())

					var parsed map[string]interface{}
					Expect(json.Unmarshal([]byte(payload), &parsed)).To(Succeed())
					Expect(parsed).To(HaveKey("input_tokens"))
					Expect(parsed).To(HaveKey("output_reserve"))
					Expect(parsed).To(HaveKey("limit"))
					Expect(parsed).To(HaveKey("percentage"))
					Expect(parsed).To(HaveKey("provider"))
					Expect(parsed).To(HaveKey("model"))
					Expect(parsed["limit"]).To(BeNumerically("==", 100_000))
					Expect(parsed["provider"]).To(Equal("test-chat-provider"))
					Expect(parsed["model"]).To(Equal("test-model"))
				})

				It("returns hasUsage=false when no token counter is wired", func() {
					eng := engine.New(engine.Config{
						ChatProvider: chatProvider,
						Manifest:     manifest,
						// No TokenCounter wired
					})

					_, ok := eng.ContextUsageJSONForSession("test-chat-provider", "test-model",
						[]provider.Message{{Role: "user", Content: "hi"}})
					Expect(ok).To(BeFalse(), "no counter, no usage payload")
				})

				It("includes per-tool overhead in the input estimate by reading the engine's current schema set", func() {
					// Same input messages but two engines — one with no
					// tools, one with a tool that adds a name +
					// description + per-tool fixed overhead. The wired-
					// engine's input_tokens MUST exceed the bare engine's
					// figure so the chip's "what does the next send cost"
					// reflects the active tool slate, matching the pre-
					// send chunk's behaviour.
					registry := provider.NewRegistry()
					registry.Register(chatProvider)
					health := failover.NewHealthManager()
					manager := failover.NewManager(registry, health, 5*time.Minute)
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "test-chat-provider", Model: "test-model"},
					})
					manager.SetContextFallback(100_000)

					bareEng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest:        manifest,
						TokenCounter:    ctxstore.NewApproximateCounter(),
					})

					toolyEng := engine.New(engine.Config{
						Registry:        registry,
						FailoverManager: manager,
						Manifest: agent.Manifest{
							ID:   "test-agent",
							Name: "test",
							Capabilities: agent.Capabilities{
								Tools: []string{"a-noisy-tool"},
							},
						},
						Tools: []tool.Tool{&mockTool{
							name:        "a-noisy-tool",
							description: "A description that consumes some tokens",
						}},
						TokenCounter: ctxstore.NewApproximateCounter(),
					})

					payloadBare, okBare := bareEng.ContextUsageJSONForSession(
						"test-chat-provider", "test-model",
						[]provider.Message{{Role: "user", Content: "hi"}},
					)
					payloadTooly, okTooly := toolyEng.ContextUsageJSONForSession(
						"test-chat-provider", "test-model",
						[]provider.Message{{Role: "user", Content: "hi"}},
					)
					Expect(okBare).To(BeTrue())
					Expect(okTooly).To(BeTrue())

					var bare, tooly map[string]interface{}
					Expect(json.Unmarshal([]byte(payloadBare), &bare)).To(Succeed())
					Expect(json.Unmarshal([]byte(payloadTooly), &tooly)).To(Succeed())

					Expect(tooly["input_tokens"]).To(BeNumerically(">", bare["input_tokens"]),
						"the helper must include the engine's tool-schema overhead so the figure matches what the next send would actually cost")
				})
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
			manager           *failover.Manager
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

			health := failover.NewHealthManager()
			manager = failover.NewManager(registry, health, 5*time.Minute)
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "primary", Model: "primary-model"},
				{Provider: "secondary", Model: "secondary-model"},
			})

			manifest = agent.Manifest{
				ID:         "test-agent",
				Name:       "Test Agent",
				Complexity: "standard",
				Instructions: agent.Instructions{
					SystemPrompt: "You are a helpful assistant.",
				},
				ContextManagement: agent.DefaultContextManagement(),
			}
		})

		Context("when primary provider works", func() {
			It("uses the primary provider", func() {
				eng := engine.New(engine.Config{
					Registry:        registry,
					FailoverManager: manager,
					Manifest:        manifest,
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Hello")

				Expect(err).NotTo(HaveOccurred())

				var received []provider.StreamChunk
				for chunk := range chunks {
					// model_active is prepended on EVERY successful stream so
					// the chat UI's chip can pivot from selection to actual
					// the moment streaming starts. It is observability
					// metadata for the chip — not user-visible content — so
					// drop it before asserting on the assistant response
					// stream.
					if chunk.EventType == "model_active" {
						continue
					}
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
					Registry:        registry,
					FailoverManager: manager,
					Manifest:        manifest,
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Hello")

				Expect(err).NotTo(HaveOccurred())

				var received []provider.StreamChunk
				for chunk := range chunks {
					// The Track B failover affordance prepends a synthetic
					// chunk{EventType:"provider_changed"} when fallback
					// kicks in, and the May 2026 chip-fix prepends
					// model_active on EVERY successful stream. Both are
					// observability metadata for the chat UI — not
					// user-visible content — so drop them before asserting
					// on the assistant response stream.
					if chunk.EventType == "provider_changed" || chunk.EventType == "model_active" {
						continue
					}
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
					Registry:        registry,
					FailoverManager: manager,
					Manifest:        manifest,
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

				slowHealth := failover.NewHealthManager()
				slowManager := failover.NewManager(slowRegistry, slowHealth, 50*time.Millisecond)
				slowManager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "slow", Model: "slow-model"},
					{Provider: "secondary", Model: "secondary-model"},
				})

				slowManifest := agent.Manifest{
					ID:         "test-agent",
					Name:       "Test Agent",
					Complexity: "standard",
					Instructions: agent.Instructions{
						SystemPrompt: "You are a helpful assistant.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				eng := engine.New(engine.Config{
					Registry:        slowRegistry,
					FailoverManager: slowManager,
					Manifest:        slowManifest,
					StreamTimeout:   50 * time.Millisecond,
				})

				slowProvider.streamErr = context.DeadlineExceeded

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Hello")

				Expect(err).NotTo(HaveOccurred())

				var received []provider.StreamChunk
				for chunk := range chunks {
					// Skip the Track B failover transition affordance and
					// the May 2026 model_active chip-fix prepend — see
					// comment above on the prior fallback test for the
					// rationale.
					if chunk.EventType == "provider_changed" || chunk.EventType == "model_active" {
						continue
					}
					received = append(received, chunk)
				}

				Expect(received).To(HaveLen(1))
				Expect(received[0].Content).To(Equal("Secondary response"))
			})
		})

		Context("when logging which provider served request", func() {
			It("tracks the last provider that served the request", func() {
				eng := engine.New(engine.Config{
					Registry:        registry,
					FailoverManager: manager,
					Manifest:        manifest,
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
		It("uses the system prompt from manifest instructions", func() {
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
					SystemPrompt: "You are a helpful assistant.",
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

			Expect(systemMessage.Content).To(ContainSubstring("You are a helpful assistant."))
		})
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
					SystemPrompt: "You are a planner orchestrating complex tasks.",
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
			Expect(eng.BuildSystemPrompt()).To(ContainSubstring("planner orchestrating"))
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

	Describe("Manifest", func() {
		It("returns the current manifest after SetManifest", func() {
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{ID: "executor"},
			})
			Expect(eng.Manifest().ID).To(Equal("executor"))

			eng.SetManifest(agent.Manifest{ID: "planner"})
			Expect(eng.Manifest().ID).To(Equal("planner"))
		})

		It("updates the delegate tool delegation config when agent switches", func() {
			executorManifest := agent.Manifest{
				ID: "executor",
				Delegation: agent.Delegation{
					CanDelegate: false,
				},
			}

			eng := engine.New(engine.Config{
				Manifest: executorManifest,
			})

			delegateTool := engine.NewDelegateTool(nil, executorManifest.Delegation, "executor")
			eng.AddTool(delegateTool)

			Expect(delegateTool.Delegation().CanDelegate).To(BeFalse())

			plannerManifest := agent.Manifest{
				ID: "planner",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			eng.SetManifest(plannerManifest)

			dt, ok := eng.GetDelegateTool()
			Expect(ok).To(BeTrue())
			Expect(dt.Delegation().CanDelegate).To(BeTrue())
		})

		Context("when the new manifest declares always_active_skills", func() {
			// Pins the root-engine manifest-swap path. The CLI's
			// `flowstate run --agent <id>` flow reuses a single root
			// engine across agent invocations and swaps manifests
			// in-place via Engine.Stream → SetManifest
			// (engine.go:1438). Without a SkillsResolver plumbed
			// through SetManifest the engine keeps the skills
			// resolved at construction time; skills declared in the
			// swapped-in manifest's always_active_skills never
			// reach LoadedSkills and the session sidecar reports
			// the regression (validate-harness.sh emits
			// "WARNING: manifest always_active_skills not loaded
			// in session: …").
			//
			// Mirrors the predecessor fix for the delegate-engine
			// path (commit edfad25, Obsidian note "Delegate Engine
			// Skills Silent Drop (April 2026)"). The engine-side
			// equivalent is a construction-time callback so the
			// engine can re-resolve its skills on every manifest
			// swap without reaching back into App state.
			//
			It("re-resolves LoadedSkills against the new manifest", func() {
				plannerSkill := skill.Skill{
					Name:    "planner-only",
					Content: "plan first",
				}
				executorSkill := skill.Skill{
					Name:    "executor-only",
					Content: "do it",
				}
				resolver := func(m agent.Manifest) []skill.Skill {
					switch m.ID {
					case "planner":
						return []skill.Skill{plannerSkill}
					case "executor":
						return []skill.Skill{executorSkill}
					default:
						return nil
					}
				}

				plannerManifest := agent.Manifest{
					ID: "planner",
					Capabilities: agent.Capabilities{
						AlwaysActiveSkills: []string{"planner-only"},
					},
				}
				executorManifest := agent.Manifest{
					ID: "executor",
					Capabilities: agent.Capabilities{
						AlwaysActiveSkills: []string{"executor-only"},
					},
				}

				eng := engine.New(engine.Config{
					Manifest:       plannerManifest,
					Skills:         []skill.Skill{plannerSkill},
					SkillsResolver: resolver,
				})

				loadedBefore := eng.LoadedSkills()
				namesBefore := make([]string, 0, len(loadedBefore))
				for i := range loadedBefore {
					namesBefore = append(namesBefore, loadedBefore[i].Name)
				}
				Expect(namesBefore).To(ConsistOf("planner-only"))

				eng.SetManifest(executorManifest)

				loadedAfter := eng.LoadedSkills()
				namesAfter := make([]string, 0, len(loadedAfter))
				for i := range loadedAfter {
					namesAfter = append(namesAfter, loadedAfter[i].Name)
				}
				Expect(namesAfter).To(ConsistOf("executor-only"))
			})
		})
	})

	// T-swarm-2 (spec §2): the lead engine receives the SwarmContext
	// so the delegate tool's allowlist shadow, gate dispatch, and
	// chain-prefix namespacing can read it from a single source of
	// truth. Pinned here as a unit on the engine because §5 calls
	// out the lead-engine wiring as the T-swarm-2 deliverable.
	Describe("SwarmContext (T-swarm-2)", func() {
		It("returns nil when no swarm context is configured", func() {
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{ID: "test-agent"},
			})

			Expect(eng.SwarmContext()).To(BeNil())
		})

		It("surfaces the context provided via Config.SwarmContext at construction time", func() {
			swarmCtx := &swarm.Context{
				SwarmID:     "tech-team",
				LeadAgent:   "tech-lead",
				Members:     []string{"explorer", "analyst"},
				ChainPrefix: "tech",
			}

			eng := engine.New(engine.Config{
				Manifest:     agent.Manifest{ID: "tech-lead"},
				SwarmContext: swarmCtx,
			})

			got := eng.SwarmContext()
			Expect(got).NotTo(BeNil(),
				"the lead engine must surface the SwarmContext supplied via Config")
			Expect(got.SwarmID).To(Equal("tech-team"))
			Expect(got.LeadAgent).To(Equal("tech-lead"))
			Expect(got.Members).To(Equal([]string{"explorer", "analyst"}))
		})

		It("updates the carried context on SetSwarmContext", func() {
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{ID: "tech-lead"},
			})

			swarmCtx := &swarm.Context{SwarmID: "tech-team", LeadAgent: "tech-lead"}
			eng.SetSwarmContext(swarmCtx)
			Expect(eng.SwarmContext().SwarmID).To(Equal("tech-team"))

			// Clearing reverts to single-agent shape — the runner
			// invokes this when the swarm run completes.
			eng.SetSwarmContext(nil)
			Expect(eng.SwarmContext()).To(BeNil())
		})
	})

	Describe("FlushSwarmLifecycle (T-swarm-3)", func() {
		It("returns nil when no delegate tool is wired", func() {
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{ID: "test-agent"},
			})

			Expect(eng.FlushSwarmLifecycle(context.Background())).To(Succeed())
		})

		It("proxies to DelegateTool.FlushSwarmLifecycle when wired", func() {
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{ID: "lead"},
			})
			eng.SetSwarmContext(&swarm.Context{
				SwarmID:     "test-swarm",
				LeadAgent:   "lead",
				ChainPrefix: "test",
				Gates: []swarm.GateSpec{{
					Name: "post-aggregate",
					Kind: "builtin:result-schema",
					When: swarm.LifecyclePostSwarm,
				}},
			})
			runner := &recordingRunner{}
			dt := engine.NewDelegateToolWithBackground(
				map[string]*engine.Engine{"lead": eng},
				agent.Delegation{CanDelegate: true},
				"lead",
				nil,
				nil,
			).WithGateRunner(runner)
			eng.AddTool(dt)

			Expect(eng.FlushSwarmLifecycle(context.Background())).To(Succeed())
			Expect(runner.calls).To(HaveLen(1))
			Expect(runner.calls[0].When).To(Equal(swarm.LifecyclePostSwarm))
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

	Describe("Recall tool registration", func() {
		It("registers recall tools during engine construction when the dependencies are available", func() {
			manifest := agent.Manifest{
				ID:                "test-agent",
				ContextManagement: agent.DefaultContextManagement(),
			}
			manifest.ContextManagement.EmbeddingModel = "test-model"

			eng := engine.New(engine.Config{
				ChatProvider:      chatProvider,
				EmbeddingProvider: chatProvider,
				Store:             recall.NewEmptyContextStore("test-model"),
				TokenCounter:      ctxstore.NewApproximateCounter(),
				Manifest:          manifest,
			})

			Expect(eng.HasTool("search_context")).To(BeTrue())
			Expect(eng.HasTool("get_messages")).To(BeTrue())
			Expect(eng.HasTool("summarize_context")).To(BeTrue())
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
					models: []provider.Model{
						{ID: "claude-sonnet-4-6", Provider: "anthropic", ContextLength: 200000},
					},
				}
				registry.Register(claudeProvider)

				claudeManifest := agent.Manifest{
					ID:         "test-agent",
					Name:       "Test Agent",
					Complexity: "standard",
					Instructions: agent.Instructions{
						SystemPrompt: "You are a helpful assistant.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				resolver := &mockResolver{limits: map[string]int{
					"anthropic/claude-sonnet-4-6": 200000,
				}}
				tokenCounter := ctxstore.NewTiktokenCounterWithResolver(resolver, "anthropic")

				health := failover.NewHealthManager()
				manager := failover.NewManager(registry, health, 5*time.Minute)
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-sonnet-4-6"},
				})

				eng := engine.New(engine.Config{
					Registry:        registry,
					FailoverManager: manager,
					Manifest:        claudeManifest,
					TokenCounter:    tokenCounter,
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
			It("returns the 16K default model-context fallback instead of the legacy 4096", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				Expect(eng.ModelContextLimit()).To(Equal(ctxstore.DefaultModelContextFallback))
			})

			It("honours cfg.SystemPromptBudget as the fallback override", func() {
				eng := engine.New(engine.Config{
					ChatProvider:       chatProvider,
					Manifest:           manifest,
					SystemPromptBudget: 32768,
				})

				Expect(eng.ModelContextLimit()).To(Equal(32768))
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
					models: []provider.Model{
						{ID: "llama3.2", Provider: "ollama", ContextLength: 4096},
					},
				}
				anthropicProvider := &mockProvider{
					name: "anthropic",
					streamChunks: []provider.StreamChunk{
						{Content: "response", Done: true},
					},
					models: []provider.Model{
						{ID: "claude-sonnet-4-6", Provider: "anthropic", ContextLength: 200000},
					},
				}
				registry.Register(ollamaProvider)
				registry.Register(anthropicProvider)

				ollamaManifest := agent.Manifest{
					ID:         "test-agent",
					Name:       "Test Agent",
					Complexity: "standard",
					Instructions: agent.Instructions{
						SystemPrompt: "You are a helpful assistant.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				resolver := &mockResolver{limits: map[string]int{
					"ollama/llama3.2":             4096,
					"anthropic/claude-sonnet-4-6": 200000,
				}}
				tokenCounter := ctxstore.NewTiktokenCounterWithResolver(resolver, "ollama")

				health := failover.NewHealthManager()
				manager := failover.NewManager(registry, health, 5*time.Minute)
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "ollama", Model: "llama3.2"},
					{Provider: "anthropic", Model: "claude-sonnet-4-6"},
				})

				eng := engine.New(engine.Config{
					Registry:        registry,
					FailoverManager: manager,
					Manifest:        ollamaManifest,
					TokenCounter:    tokenCounter,
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

		Context("after SetModelPreference changes to a different provider", func() {
			It("returns the new model limit even after a previous stream used a different model", func() {
				registry := provider.NewRegistry()
				anthropicProvider := &mockProvider{
					name: "anthropic",
					streamChunks: []provider.StreamChunk{
						{Content: "response", Done: true},
					},
					models: []provider.Model{
						{ID: "claude-sonnet-4-6", Provider: "anthropic", ContextLength: 200000},
					},
				}
				ollamaProvider := &mockProvider{
					name: "ollama",
					streamChunks: []provider.StreamChunk{
						{Content: "response", Done: true},
					},
					models: []provider.Model{
						{ID: "llama3.2", Provider: "ollama", ContextLength: 4096},
					},
				}
				registry.Register(anthropicProvider)
				registry.Register(ollamaProvider)

				testManifest := agent.Manifest{
					ID:         "test-agent",
					Name:       "Test Agent",
					Complexity: "standard",
					Instructions: agent.Instructions{
						SystemPrompt: "You are a helpful assistant.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				resolver := &mockResolver{limits: map[string]int{
					"anthropic/claude-sonnet-4-6": 200000,
					"ollama/llama3.2":             4096,
				}}
				tokenCounter := ctxstore.NewTiktokenCounterWithResolver(resolver, "anthropic")

				health := failover.NewHealthManager()
				manager := failover.NewManager(registry, health, 5*time.Minute)
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-sonnet-4-6"},
				})

				eng := engine.New(engine.Config{
					Registry:        registry,
					FailoverManager: manager,
					Manifest:        testManifest,
					TokenCounter:    tokenCounter,
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range chunks {
					_ = v
				}
				Expect(eng.ModelContextLimit()).To(Equal(200000))

				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "ollama", Model: "llama3.2"},
				})

				eng.SetModelPreference("ollama", "llama3.2")

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
		It("does not include delegation sections when no registry is set", func() {
			manifest := agent.Manifest{
				ID:   "test-delegator",
				Name: "Test Delegator",
				Instructions: agent.Instructions{
					SystemPrompt: "You are a test delegator.",
				},
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "test"},
				Manifest:     manifest,
			})

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring("You are a test delegator."))
			Expect(prompt).NotTo(ContainSubstring("## Delegation Targets"))
		})

		It("does not include delegation sections when agent cannot delegate", func() {
			manifest := agent.Manifest{
				ID:   "test-non-delegator",
				Name: "Test Non-Delegator",
				Instructions: agent.Instructions{
					SystemPrompt: "You are a test non-delegator.",
				},
				Delegation: agent.Delegation{
					CanDelegate: false,
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
	})

	Describe("Tool continuation with provider preservation", func() {
		It("preserves selected provider in tool continuation requests", func() {
			anthropicProvider := &mockProvider{
				name: "anthropic",
				streamChunks: []provider.StreamChunk{
					{Content: "Response", Done: true},
				},
			}

			registry := provider.NewRegistry()
			registry.Register(anthropicProvider)

			health := failover.NewHealthManager()
			manager := failover.NewManager(registry, health, 5*time.Minute)
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-opus"},
			})

			testManifest := agent.Manifest{
				ID:   "test-agent",
				Name: "Test Agent",
				Instructions: agent.Instructions{
					SystemPrompt: "You are a helpful assistant.",
				},
				ContextManagement: agent.DefaultContextManagement(),
			}

			eng := engine.New(engine.Config{
				Registry:        registry,
				FailoverManager: manager,
				Manifest:        testManifest,
			})

			eng.SetModelPreference("anthropic", "claude-opus")

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Hello")

			Expect(err).NotTo(HaveOccurred())

			for v := range chunks {
				_ = v
			}

			Expect(anthropicProvider.capturedRequest).NotTo(BeNil())
			Expect(anthropicProvider.capturedRequest.Provider).To(Equal("anthropic"))
			Expect(anthropicProvider.capturedRequest.Model).To(Equal("claude-opus"))
		})
	})

	Describe("CategoryResolver wiring", func() {
		// The engine consults CategoryResolver at Stream time to thread
		// MaxTokens / Temperature from the active manifest's category
		// onto provider.ChatRequest. This guards against a regression
		// where the resolver is silently bypassed (which is exactly
		// what the unwired-state-of-the-world looked like before).

		It("threads MaxTokens and Temperature from CategoryConfig when wired", func() {
			capturer := &mockProvider{
				name: "anthropic",
				streamChunks: []provider.StreamChunk{
					{Content: "ok", Done: true},
				},
			}
			cm := manifest
			cm.OrchestratorMeta.Category = "deep" // default routes to temp=0.7, maxTokens=4096
			resolver := engine.NewCategoryResolver(nil)
			eng := engine.New(engine.Config{
				ChatProvider:     capturer,
				Manifest:         cm,
				CategoryResolver: resolver,
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "hi")
			Expect(err).NotTo(HaveOccurred())
			for v := range chunks {
				_ = v
			}
			Expect(capturer.capturedRequest).NotTo(BeNil())
			Expect(capturer.capturedRequest.MaxTokens).To(Equal(4096))
			Expect(capturer.capturedRequest.Temperature).NotTo(BeNil())
			Expect(*capturer.capturedRequest.Temperature).To(BeNumerically("~", 0.7, 1e-9))
		})

		It("leaves ChatRequest fields zero when no resolver is wired (back-compat)", func() {
			capturer := &mockProvider{
				name: "anthropic",
				streamChunks: []provider.StreamChunk{
					{Content: "ok", Done: true},
				},
			}
			cm := manifest
			cm.OrchestratorMeta.Category = "deep"
			eng := engine.New(engine.Config{
				ChatProvider: capturer,
				Manifest:     cm,
				// CategoryResolver intentionally nil
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "hi")
			Expect(err).NotTo(HaveOccurred())
			for v := range chunks {
				_ = v
			}
			Expect(capturer.capturedRequest).NotTo(BeNil())
			Expect(capturer.capturedRequest.MaxTokens).To(BeZero())
			Expect(capturer.capturedRequest.Temperature).To(BeNil())
		})

		It("leaves ChatRequest fields zero when manifest has no Category", func() {
			capturer := &mockProvider{
				name: "anthropic",
				streamChunks: []provider.StreamChunk{
					{Content: "ok", Done: true},
				},
			}
			resolver := engine.NewCategoryResolver(nil)
			eng := engine.New(engine.Config{
				ChatProvider:     capturer,
				Manifest:         manifest, // no OrchestratorMeta.Category
				CategoryResolver: resolver,
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "hi")
			Expect(err).NotTo(HaveOccurred())
			for v := range chunks {
				_ = v
			}
			Expect(capturer.capturedRequest).NotTo(BeNil())
			Expect(capturer.capturedRequest.MaxTokens).To(BeZero())
			Expect(capturer.capturedRequest.Temperature).To(BeNil())
		})
	})
})
