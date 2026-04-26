package chat_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// Regression: a long planner reviewer summary (~12KB delivered as
// 3000+ chunks over several minutes) was persisted to session JSON in
// full but appeared truncated in the TUI viewport. The view-level
// burst-stream spec (internal/tui/views/chat) proves view.RenderContent
// emits the complete content; this spec drives the *intent* through
// the same chunk shape via HandleStreamChunkMsgForTest so we cover the
// engine→intent→view->viewport-content path end-to-end.
//
// If this spec passes, the truncation symptom must be downstream of
// what we control here (Bubble Tea viewport SetContent / terminal
// rendering / scroll position). If it fails, the bug is in
// handleStreamChunkMsg or its handlers and we have a reproducer.
var _ = Describe("Intent: long burst-stream renders the full final message", Label("integration"), func() {
	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })
	})

	It("emits the full committed assistant content on Done after a 200-chunk burst", func() {
		intent := chat.NewIntent(chat.IntentConfig{
			AgentID:      "planner",
			SessionID:    "session-burst",
			ProviderName: "test-provider",
			ModelName:    "test-model",
			TokenBudget:  100_000,
		})
		// Identity renderer so glamour does not interleave per-character
		// ANSI colour codes with the chunk markers — the assertions are
		// substring searches and would otherwise be foiled by the styling
		// even though the underlying content is intact.
		intent.SetMarkdownRendererForTest(func(s string, _ int) string { return s })

		const chunks = 200
		var fullExpected strings.Builder
		for n := 0; n < chunks; n++ {
			body := burstChunkBody(n)
			fullExpected.WriteString(body)
			intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{
				Content: body,
				Done:    false,
			})
		}
		// Done sentinel with no content — matches the real-world shape
		// that produced the stalled-session symptom.
		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{Done: true})

		rendered := intent.RenderedViewportContentForTest(120)

		Expect(rendered).To(ContainSubstring(burstChunkBody(0)),
			"first chunk's marker missing — start of the response was not committed")
		Expect(rendered).To(ContainSubstring(burstChunkBody(chunks/2)),
			"mid-burst chunk's marker missing — middle of the response was lost")
		Expect(rendered).To(ContainSubstring(burstChunkBody(chunks-1)),
			"last content chunk's marker missing — tail of the response was lost")
		Expect(rendered).To(ContainSubstring(fullExpected.String()),
			"the rendered viewport content must contain the complete concatenation of all chunks")
	})

	It("emits the full content when the Done chunk also carries the final payload", func() {
		intent := chat.NewIntent(chat.IntentConfig{
			AgentID:      "planner",
			SessionID:    "session-done-with-content",
			ProviderName: "test-provider",
			ModelName:    "test-model",
			TokenBudget:  100_000,
		})
		intent.SetMarkdownRendererForTest(func(s string, _ int) string { return s })

		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{Content: "alpha. ", Done: false})
		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{Content: "beta. ", Done: false})
		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{Content: "gamma.", Done: true})

		rendered := intent.RenderedViewportContentForTest(120)
		Expect(rendered).To(ContainSubstring("alpha."))
		Expect(rendered).To(ContainSubstring("beta."))
		Expect(rendered).To(ContainSubstring("gamma."),
			"the Done-with-content payload must appear in the final viewport content")
	})
})

func burstChunkBody(n int) string {
	return "[chunk-" + burstPaddedIdx(n) + "-marker-with-discriminator] "
}

func burstPaddedIdx(n int) string {
	switch {
	case n < 10:
		return "00" + string(rune('0'+n))
	case n < 100:
		return "0" + string(rune('0'+n/10)) + string(rune('0'+n%10))
	}
	return string(rune('0'+n/100)) + string(rune('0'+(n/10)%10)) + string(rune('0'+n%10))
}
