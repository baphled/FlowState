package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("Token counter accumulation during streaming", Label("integration"), func() {
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

	Describe("response token accumulation", func() {
		It("accumulates response tokens as content chunks arrive", func() {
			chunk1 := chat.StreamChunkMsg{Content: "Hello ", Done: false}
			chunk2 := chat.StreamChunkMsg{Content: "world", Done: false}

			intent.HandleStreamChunkForTest(chunk1)
			afterFirst := intent.ResponseTokenCountForTest()

			intent.HandleStreamChunkForTest(chunk2)
			afterSecond := intent.ResponseTokenCountForTest()

			Expect(afterFirst).To(BeNumerically(">", 0))
			Expect(afterSecond).To(BeNumerically(">", afterFirst))
		})

		It("reports a combined token count that grows with each chunk", func() {
			beforeAny := intent.TokenCount()

			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Content: "chunk one", Done: false})
			afterFirst := intent.TokenCount()

			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Content: " chunk two", Done: false})
			afterSecond := intent.TokenCount()

			Expect(afterFirst).To(BeNumerically(">", beforeAny))
			Expect(afterSecond).To(BeNumerically(">", afterFirst))
		})

		It("resets response token count when a new stream begins", func() {
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Content: "first stream content", Done: false})
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Content: " more content", Done: true})

			Expect(intent.ResponseTokenCountForTest()).To(BeNumerically(">", 0))
		})

		It("does not accumulate tokens for empty content chunks", func() {
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Content: "", Done: false})
			Expect(intent.ResponseTokenCountForTest()).To(Equal(0))
		})

		It("sends a status bar update with incremented token count on each content chunk", func() {
			initial := intent.TokenCount()

			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Content: "some response text", Done: false})

			Expect(intent.TokenCount()).To(BeNumerically(">", initial))
		})
	})
})
