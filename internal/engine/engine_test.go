package engine_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/tool"
)

type mockProvider struct {
	name         string
	streamChunks []provider.StreamChunk
	streamErr    error
	chatResp     provider.ChatResponse
	chatErr      error
	embedResult  []float64
	embedErr     error
	models       []provider.Model
	modelsErr    error
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
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
}

func (t *mockTool) Name() string        { return t.name }
func (t *mockTool) Description() string { return t.description }
func (t *mockTool) Execute(_ context.Context, _ tool.ToolInput) (tool.ToolResult, error) {
	return tool.ToolResult{}, nil
}
func (t *mockTool) Schema() tool.ToolSchema { return tool.ToolSchema{} }

var _ = Describe("Engine", func() {
	var (
		chatProvider      *mockProvider
		embeddingProvider *mockProvider
		manifest          agent.AgentManifest
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

		manifest = agent.AgentManifest{
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

		It("appends always-active skill content", func() {
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Skills:       skills,
			})

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring("Always remember context."))
		})

		It("does not include non-active skills", func() {
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Skills:       skills,
			})

			prompt := eng.BuildSystemPrompt()

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
	})

	Describe("Stream", func() {
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
})
