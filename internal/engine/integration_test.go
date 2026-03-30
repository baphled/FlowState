package engine_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("Engine Integration", Label("integration"), func() {
	Describe("failback chain async error recovery through engine", func() {
		var (
			reg *provider.Registry
			eng *engine.Engine
			mgr *failover.Manager
		)

		Context("when the first provider sends an async error chunk", func() {
			BeforeEach(func() {
				reg = provider.NewRegistry()
				reg.Register(&asyncFailProvider{
					name: "broken-provider",
				})
				reg.Register(&workingStreamProvider{
					name: "healthy-provider",
					chunks: []provider.StreamChunk{
						{Content: "response from healthy"},
						{Done: true},
					},
				})

				health := failover.NewHealthManager()
				mgr = failover.NewManager(reg, health, 5*time.Minute)
				mgr.SetBasePreferences([]provider.ModelPreference{
					{Provider: "broken-provider", Model: "broken-model"},
					{Provider: "healthy-provider", Model: "healthy-model"},
				})

				eng = engine.New(engine.Config{
					Registry:        reg,
					FailoverManager: mgr,
					Manifest:        failbackManifest(),
				})
			})

			It("falls back to the healthy provider and delivers its response", func() {
				ctx := context.Background()
				ch, err := eng.Stream(ctx, "test-agent", "hello")
				Expect(err).NotTo(HaveOccurred())

				var contents []string
				for chunk := range ch {
					if chunk.Content != "" {
						contents = append(contents, chunk.Content)
					}
				}

				Expect(contents).To(ContainElement("response from healthy"))
			})

			It("sets lastProvider to the healthy provider", func() {
				ctx := context.Background()
				ch, err := eng.Stream(ctx, "test-agent", "hello")
				Expect(err).NotTo(HaveOccurred())

				for chunk := range ch {
					_ = chunk
				}

				Expect(eng.LastProvider()).To(Equal("healthy-provider"))
				Expect(eng.LastModel()).To(Equal("healthy-model"))
			})
		})

		Context("when all providers send async error chunks", func() {
			BeforeEach(func() {
				reg = provider.NewRegistry()
				reg.Register(&asyncFailProvider{
					name: "broken-a",
				})
				reg.Register(&asyncFailProvider{
					name: "broken-b",
				})

				health := failover.NewHealthManager()
				mgr = failover.NewManager(reg, health, 5*time.Minute)
				mgr.SetBasePreferences([]provider.ModelPreference{
					{Provider: "broken-a", Model: "model-a"},
					{Provider: "broken-b", Model: "model-b"},
				})

				eng = engine.New(engine.Config{
					Registry:        reg,
					FailoverManager: mgr,
					Manifest: agent.Manifest{
						ID:         "test-agent",
						Name:       "Test Agent",
						Complexity: "standard",
						ModelPreferences: map[string][]agent.ModelPref{
							"standard": {
								{Provider: "broken-a", Model: "model-a"},
								{Provider: "broken-b", Model: "model-b"},
							},
						},
					},
				})
			})

			It("returns an error indicating all providers failed", func() {
				ctx := context.Background()
				_, err := eng.Stream(ctx, "test-agent", "hello")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("all providers failed"))
			})
		})

		Context("when a provider fails synchronously and fallback succeeds", func() {
			BeforeEach(func() {
				reg = provider.NewRegistry()
				reg.Register(&syncFailStreamProvider{
					name: "sync-broken",
				})
				reg.Register(&workingStreamProvider{
					name: "fallback-ok",
					chunks: []provider.StreamChunk{
						{Content: "fallback works"},
						{Done: true},
					},
				})

				health := failover.NewHealthManager()
				mgr = failover.NewManager(reg, health, 5*time.Minute)
				mgr.SetBasePreferences([]provider.ModelPreference{
					{Provider: "sync-broken", Model: "broken-model"},
					{Provider: "fallback-ok", Model: "ok-model"},
				})

				eng = engine.New(engine.Config{
					Registry:        reg,
					FailoverManager: mgr,
					Manifest: agent.Manifest{
						ID:         "test-agent",
						Name:       "Test Agent",
						Complexity: "standard",
						ModelPreferences: map[string][]agent.ModelPref{
							"standard": {
								{Provider: "sync-broken", Model: "broken-model"},
								{Provider: "fallback-ok", Model: "ok-model"},
							},
						},
					},
				})
			})

			It("delivers the fallback provider response", func() {
				ctx := context.Background()
				ch, err := eng.Stream(ctx, "test-agent", "hello")
				Expect(err).NotTo(HaveOccurred())

				var contents []string
				for chunk := range ch {
					if chunk.Content != "" {
						contents = append(contents, chunk.Content)
					}
				}

				Expect(contents).To(ContainElement("fallback works"))
				Expect(eng.LastProvider()).To(Equal("fallback-ok"))
			})
		})
	})

	Describe("model preference update affects subsequent streams", func() {
		var (
			reg   *provider.Registry
			eng   *engine.Engine
			mgr   *failover.Manager
			provA *workingStreamProvider
			provB *workingStreamProvider
		)

		BeforeEach(func() {
			reg = provider.NewRegistry()
			provA = &workingStreamProvider{
				name: "provider-a",
				chunks: []provider.StreamChunk{
					{Content: "from provider A"},
					{Done: true},
				},
			}
			provB = &workingStreamProvider{
				name: "provider-b",
				chunks: []provider.StreamChunk{
					{Content: "from provider B"},
					{Done: true},
				},
			}
			reg.Register(provA)
			reg.Register(provB)

			health := failover.NewHealthManager()
			mgr = failover.NewManager(reg, health, 5*time.Minute)
			mgr.SetBasePreferences([]provider.ModelPreference{
				{Provider: "provider-a", Model: "model-a"},
			})

			eng = engine.New(engine.Config{
				Registry:        reg,
				FailoverManager: mgr,
				Manifest: agent.Manifest{
					ID:         "test-agent",
					Name:       "Test Agent",
					Complexity: "standard",
					ModelPreferences: map[string][]agent.ModelPref{
						"standard": {
							{Provider: "provider-a", Model: "model-a"},
						},
					},
				},
			})
		})

		It("initially streams through provider-a", func() {
			ctx := context.Background()
			ch, err := eng.Stream(ctx, "test-agent", "hello")
			Expect(err).NotTo(HaveOccurred())

			var contents []string
			for chunk := range ch {
				if chunk.Content != "" {
					contents = append(contents, chunk.Content)
				}
			}

			Expect(contents).To(ContainElement("from provider A"))
			Expect(eng.LastProvider()).To(Equal("provider-a"))
		})

		It("streams through provider-b after SetModelPreference", func() {
			eng.SetModelPreference("provider-b", "model-b")

			ctx := context.Background()
			ch, err := eng.Stream(ctx, "test-agent", "hello")
			Expect(err).NotTo(HaveOccurred())

			var contents []string
			for chunk := range ch {
				if chunk.Content != "" {
					contents = append(contents, chunk.Content)
				}
			}

			Expect(contents).To(ContainElement("from provider B"))
			Expect(eng.LastProvider()).To(Equal("provider-b"))
			Expect(eng.LastModel()).To(Equal("model-b"))
		})
	})

	Describe("embedded prompt loading", func() {
		var chatProvider *mockProvider

		BeforeEach(func() {
			chatProvider = &mockProvider{
				name: "test-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "test", Done: true},
				},
			}
		})

		Context("when agent manifest system prompt is planner", func() {
			It("uses the planner system prompt from manifest", func() {
				manifest := agent.Manifest{
					ID:   "planner",
					Name: "Planner",
					Instructions: agent.Instructions{
						SystemPrompt: "You are the FlowState Planner managing the planning loop.",
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).To(ContainSubstring("FlowState Planner"))
				Expect(prompt).To(ContainSubstring("planning loop"))
			})
		})

		Context("when agent manifest system prompt is executor", func() {
			It("uses the executor system prompt from manifest", func() {
				manifest := agent.Manifest{
					ID:   "executor",
					Name: "Executor",
					Instructions: agent.Instructions{
						SystemPrompt: "You are the FlowState Task Executor discovering and running plans.",
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).To(ContainSubstring("FlowState Task Executor"))
				Expect(prompt).To(ContainSubstring("discovering and running"))
			})
		})

		Context("when agent manifest ID has no embedded prompt", func() {
			It("falls back to manifest system prompt", func() {
				manifest := agent.Manifest{
					ID:   "unknown-agent-12345",
					Name: "Unknown Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are a helpful fallback assistant.",
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				prompt := eng.BuildSystemPrompt()

				Expect(prompt).To(Equal("You are a helpful fallback assistant."))
			})
		})
	})
})

type asyncFailProvider struct {
	name string
}

func (p *asyncFailProvider) Name() string { return p.name }

func (p *asyncFailProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Error: errors.New("async 404: model not found"), Done: true}
	close(ch)
	return ch, nil
}

func (p *asyncFailProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errors.New("chat failed")
}

func (p *asyncFailProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (p *asyncFailProvider) Models() ([]provider.Model, error) {
	return []provider.Model{{ID: "bad-model", Provider: p.name}}, nil
}

type workingStreamProvider struct {
	name   string
	chunks []provider.StreamChunk
}

func (p *workingStreamProvider) Name() string { return p.name }

func (p *workingStreamProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, len(p.chunks))
	for _, chunk := range p.chunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func (p *workingStreamProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *workingStreamProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (p *workingStreamProvider) Models() ([]provider.Model, error) {
	return []provider.Model{{ID: "test-model", Provider: p.name}}, nil
}

type syncFailStreamProvider struct {
	name string
}

func (p *syncFailStreamProvider) Name() string { return p.name }

func (p *syncFailStreamProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errors.New("connection refused")
}

func (p *syncFailStreamProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errors.New("chat failed")
}

func (p *syncFailStreamProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (p *syncFailStreamProvider) Models() ([]provider.Model, error) {
	return []provider.Model{{ID: "broken-model", Provider: p.name}}, nil
}

func failbackManifest() agent.Manifest {
	return agent.Manifest{
		ID:         "test-agent",
		Name:       "Test Agent",
		Complexity: "standard",
		ModelPreferences: map[string][]agent.ModelPref{
			"standard": {
				{Provider: "broken-provider", Model: "broken-model"},
				{Provider: "healthy-provider", Model: "healthy-model"},
			},
		},
	}
}
