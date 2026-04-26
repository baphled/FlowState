package chat_test

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// Reproducer for the stalled-session symptom where a long planner
// reviewer summary persisted to JSON in full but appeared truncated
// on screen. Drives the Intent through real Init → WindowSize →
// burst of StreamChunkMsg → Done → View() calls so the test exercises
// the actual msgViewport.SetContent + GotoBottom path, not just
// view.RenderContent in isolation.
//
// Two scenarios are gated:
//
//   1. atBottom path: the user passively followed the stream. The
//      viewport's View() must include the tail of the final committed
//      assistant content after Done — which is what GotoBottom() makes
//      visible inside the viewport's window.
//
//   2. user-scrolled path: the user actively scrolled away during the
//      stream. The new end-of-stream heuristic snaps to bottom (because
//      the seeded scroll timestamp is outside the grace window) so the
//      tail still becomes visible. Without the heuristic this case
//      reproduces the original "didn't display complete" symptom.
var _ = Describe("Long-stream viewport reproducer", Label("integration"), func() {
	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })
	})

	// burstParagraphBody mirrors the real-world planner reviewer-summary
	// shape: a paragraph fragment followed by a markdown line break, so
	// the viewport sees a multi-line content block (the user's actual bug
	// is multi-line content auto-growing past the viewport during stream,
	// not a single horizontally-clipped line).
	burstParagraphBody := func(n int) string {
		return burstChunkBody(n) + "\n"
	}

	driveBurst := func(intent *chat.Intent, chunks int) string {
		var fullExpected strings.Builder
		intent.SetMarkdownRendererForTest(func(s string, _ int) string { return s })

		// Initialise the viewport with a realistic terminal size — the
		// Update path requires a WindowSizeMsg before msgViewport is
		// constructed (intent.go:1342).
		intent.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

		for n := 0; n < chunks; n++ {
			body := burstParagraphBody(n)
			fullExpected.WriteString(body)
			intent.Update(chat.StreamChunkMsg{Content: body, Done: false})
		}
		intent.Update(chat.StreamChunkMsg{Done: true})
		return fullExpected.String()
	}

	It("renders the tail of the final assistant content in the viewport when atBottom was true throughout", func() {
		intent := chat.NewIntent(chat.IntentConfig{
			AgentID:      "planner",
			SessionID:    "session-viewport-passive",
			ProviderName: "test-provider",
			ModelName:    "test-model",
			TokenBudget:  100_000,
		})
		// Mirror the post-sendMessage state — atBottom is set true on
		// every user submit (intent.go:2832). Without seeding this the
		// reproducer is testing a state the user can never reach
		// organically (atBottom defaults to false on a fresh intent).
		intent.SetAtBottomForTest(true)

		// 80 chunks → ~5 KB of marker text spread over many newlines;
		// large enough that the viewport's window will not show all of
		// it at once, so the tail-visible assertion actually exercises
		// GotoBottom.
		_ = driveBurst(intent, 80)

		full := intent.RenderedViewportContentForTest(120)
		_, _, vpView := intent.MsgViewportDebugForTest()
		Expect(full).To(ContainSubstring(burstChunkBody(0)))
		Expect(full).To(ContainSubstring(burstChunkBody(79)))
		Expect(vpView).To(ContainSubstring(burstChunkBody(79)),
			"GotoBottom on the auto-grown content failed to bring the tail "+
				"into the viewport window — passive-following user perceives "+
				"the response as truncated")
	})

	It("snaps to bottom on Done when the user scrolled before the grace window expired", func() {
		intent := chat.NewIntent(chat.IntentConfig{
			AgentID:      "planner",
			SessionID:    "session-viewport-scrolled",
			ProviderName: "test-provider",
			ModelName:    "test-model",
			TokenBudget:  100_000,
		})

		// Pre-arm the heuristic: the user "scrolled away" 30s ago — well
		// outside the 5s active-scroll grace window. The post-Done
		// heuristic must snap them back to the bottom.
		intent.SetAtBottomForTest(false)
		intent.SetLastUserScrollAtForTest(time.Now().Add(-30 * time.Second))

		_ = driveBurst(intent, 80)

		Expect(intent.AtBottomForTest()).To(BeTrue(),
			"end-of-stream heuristic must restore atBottom for an out-of-grace scroll")
		view := intent.View()
		Expect(view).To(ContainSubstring(burstChunkBody(79)),
			"after the heuristic snaps and refresh runs, the tail must be visible")
	})
})
