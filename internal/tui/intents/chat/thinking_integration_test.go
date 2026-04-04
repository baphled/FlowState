package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents/chat"
	chatview "github.com/baphled/flowstate/internal/tui/views/chat"
)

var _ = Describe("Thinking Content Integration", Label("integration"), func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })

		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "test-provider",
			ModelName:    "test-model",
			TokenBudget:  4096,
		})
	})

	Describe("thinking chunk processing", func() {
		It("accumulates thinking content across partial chunks before streaming ends", func() {
			chunk1 := chat.StreamChunkMsg{Thinking: "I am", Done: false}
			chunk2 := chat.StreamChunkMsg{Thinking: " reasoning", Done: false}

			intent.HandleStreamChunkForTest(chunk1)
			intent.HandleStreamChunkForTest(chunk2)

			messages := intent.AllViewMessagesForTest()
			for _, m := range messages {
				Expect(m.Role).NotTo(Equal("thinking"))
			}
		})

		It("stores thinking content as a 'thinking' message when stream completes", func() {
			chunk1 := chat.StreamChunkMsg{Thinking: "I am reasoning about this", Done: false}
			chunk2 := chat.StreamChunkMsg{Content: "Here is the answer", Done: true}

			intent.HandleStreamChunkForTest(chunk1)
			intent.HandleStreamChunkForTest(chunk2)

			messages := intent.AllViewMessagesForTest()
			thinkingMsgs := filterByRole(messages, "thinking")
			Expect(thinkingMsgs).To(HaveLen(1))
			Expect(thinkingMsgs[0].Content).To(Equal("I am reasoning about this"))
		})

		It("stores thinking content associated with the correct assistant response", func() {
			thinkChunk := chat.StreamChunkMsg{Thinking: "Step 1: analyse the problem", Done: false}
			doneChunk := chat.StreamChunkMsg{Content: "The answer is 42", Done: true}

			intent.HandleStreamChunkForTest(thinkChunk)
			intent.HandleStreamChunkForTest(doneChunk)

			messages := intent.AllViewMessagesForTest()

			thinkingMsgs := filterByRole(messages, "thinking")
			assistantMsgs := filterByRole(messages, "assistant")
			Expect(thinkingMsgs).To(HaveLen(1))
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(thinkingMsgs[0].Content).To(Equal("Step 1: analyse the problem"))
			Expect(assistantMsgs[0].Content).To(ContainSubstring("The answer is 42"))
		})

		It("does not add a thinking message when no thinking content was provided", func() {
			doneChunk := chat.StreamChunkMsg{Content: "Direct answer", Done: true}

			intent.HandleStreamChunkForTest(doneChunk)

			messages := intent.AllViewMessagesForTest()
			thinkingMsgs := filterByRole(messages, "thinking")
			Expect(thinkingMsgs).To(BeEmpty())
		})

		It("concatenates multiple thinking fragments into a single thinking message", func() {
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Thinking: "Part A ", Done: false})
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Thinking: "Part B ", Done: false})
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Thinking: "Part C", Done: false})
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Content: "Answer", Done: true})

			messages := intent.AllViewMessagesForTest()
			thinkingMsgs := filterByRole(messages, "thinking")
			Expect(thinkingMsgs).To(HaveLen(1))
			Expect(thinkingMsgs[0].Content).To(Equal("Part A Part B Part C"))
		})

		It("clears the thinking buffer after flushing so subsequent streams are independent", func() {
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Thinking: "First reasoning", Done: false})
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Content: "First answer", Done: true})

			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Content: "Second answer (no thinking)", Done: true})

			messages := intent.AllViewMessagesForTest()
			thinkingMsgs := filterByRole(messages, "thinking")
			Expect(thinkingMsgs).To(HaveLen(1))
			Expect(thinkingMsgs[0].Content).To(Equal("First reasoning"))
		})

		It("handles thinking chunk followed by content chunk in same done message", func() {
			combined := chat.StreamChunkMsg{
				Thinking: "Quick thought",
				Content:  "Quick answer",
				Done:     true,
			}

			intent.HandleStreamChunkForTest(combined)

			messages := intent.AllViewMessagesForTest()
			thinkingMsgs := filterByRole(messages, "thinking")
			Expect(thinkingMsgs).To(HaveLen(1))
			Expect(thinkingMsgs[0].Content).To(Equal("Quick thought"))
		})
	})
})

func filterByRole(messages []chatview.Message, role string) []chatview.Message {
	var result []chatview.Message
	for _, m := range messages {
		if m.Role == role {
			result = append(result, m)
		}
	}
	return result
}
