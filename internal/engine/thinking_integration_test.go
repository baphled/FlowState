package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("Thinking chunks through Engine Stream", Label("integration"), func() {
	var (
		thinkingProvider *mockProvider
		eng              *engine.Engine
	)

	BeforeEach(func() {
		thinkingProvider = &mockProvider{
			name: "thinking-provider",
		}
		eng = engine.New(engine.Config{
			ChatProvider: thinkingProvider,
			Manifest: agent.Manifest{
				ID:   "thinking-agent",
				Name: "Thinking Agent",
				Instructions: agent.Instructions{
					SystemPrompt: "You are a thoughtful assistant.",
				},
				ContextManagement: agent.DefaultContextManagement(),
			},
		})
		eng.SetModelPreference("thinking-provider", "thinking-model")
	})

	It("StreamChunk carries Thinking field with reasoning content", func() {
		thinkingProvider.streamChunks = []provider.StreamChunk{
			{Thinking: "Let me reason about this carefully."},
			{Content: "Here is my answer.", Done: true},
		}

		ctx := context.Background()
		ch, err := eng.Stream(ctx, "thinking-agent", "What is the answer?")

		Expect(err).NotTo(HaveOccurred())

		var thinkingChunks []provider.StreamChunk
		for chunk := range ch {
			if chunk.Thinking != "" {
				thinkingChunks = append(thinkingChunks, chunk)
			}
		}

		Expect(thinkingChunks).NotTo(BeEmpty())
		Expect(thinkingChunks[0].Thinking).To(Equal("Let me reason about this carefully."))
	})

	It("passes thinking-only chunks through with empty Content", func() {
		thinkingProvider.streamChunks = []provider.StreamChunk{
			{Thinking: "Step one of reasoning."},
			{Thinking: "Step two of reasoning."},
			{Content: "Final answer.", Done: true},
		}

		ctx := context.Background()
		ch, err := eng.Stream(ctx, "thinking-agent", "Reason step by step.")

		Expect(err).NotTo(HaveOccurred())

		var thinking []string
		var content []string
		for chunk := range ch {
			if chunk.Thinking != "" {
				thinking = append(thinking, chunk.Thinking)
			}
			if chunk.Content != "" {
				content = append(content, chunk.Content)
			}
		}

		Expect(thinking).To(ConsistOf("Step one of reasoning.", "Step two of reasoning."))
		Expect(content).To(ConsistOf("Final answer."))
	})

	It("transitions from thinking to content chunks in order", func() {
		thinkingProvider.streamChunks = []provider.StreamChunk{
			{Thinking: "Analysing the question."},
			{Content: "The answer is 42.", Done: true},
		}

		ctx := context.Background()
		ch, err := eng.Stream(ctx, "thinking-agent", "What is the meaning?")

		Expect(err).NotTo(HaveOccurred())

		type orderedChunk struct {
			thinking string
			content  string
		}
		var received []orderedChunk
		for chunk := range ch {
			received = append(received, orderedChunk{thinking: chunk.Thinking, content: chunk.Content})
		}

		Expect(received).NotTo(BeEmpty())
		Expect(received[0].thinking).To(Equal("Analysing the question."))
		Expect(received[1].content).To(Equal("The answer is 42."))
	})

	It("passes thinking chunks through to consumers unchanged", func() {
		thinkingProvider.streamChunks = []provider.StreamChunk{
			{Thinking: "deep reasoning here"},
			{Content: "conclusion", Done: true},
		}

		ctx := context.Background()
		ch, err := eng.Stream(ctx, "thinking-agent", "Think about it.")

		Expect(err).NotTo(HaveOccurred())

		var allChunks []provider.StreamChunk
		for chunk := range ch {
			allChunks = append(allChunks, chunk)
		}

		var thinkingFound bool
		for _, chunk := range allChunks {
			if chunk.Thinking == "deep reasoning here" {
				thinkingFound = true
			}
		}
		Expect(thinkingFound).To(BeTrue())
	})
})
