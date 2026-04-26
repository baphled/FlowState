package chat_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/components/notification"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// applyEndOfStreamScrollHeuristic distinguishes "passive drift" (the
// content auto-grew past the user's reading window during streaming)
// from "active scroll" (the user explicitly moved the viewport in the
// recent past). The first case snaps to bottom so the final assistant
// content is visible; the second leaves the position alone but flashes
// a non-blocking "press End" hint so the user knows the stream
// finished.
//
// This spec is the regression gate for the long-stream "didn't display
// complete" symptom: with the heuristic in place, a Done chunk
// arriving while atBottom is false no longer leaves the final content
// stranded below the visible window.
var _ = Describe("End-of-stream scroll heuristic", Label("integration"), func() {
	var (
		intent  *chat.Intent
		notifMg *notification.InMemoryManager
	)

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })

		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "planner",
			SessionID:    "session-scroll",
			ProviderName: "test-provider",
			ModelName:    "test-model",
			TokenBudget:  100_000,
		})
		notifMg = notification.NewInMemoryManager()
		intent.SetNotificationManagerForTest(notifMg)
	})

	It("snaps atBottom back to true on Done when the user has not scrolled recently", func() {
		intent.SetAtBottomForTest(false)
		intent.SetLastUserScrollAtForTest(time.Time{}) // never scrolled

		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{Content: "final content", Done: true})

		Expect(intent.AtBottomForTest()).To(BeTrue(),
			"a Done chunk landing on a never-scrolled session must snap to bottom")
		Expect(notifMg.Active()).To(BeEmpty(),
			"no hint notification when the snap fires — the user does not need a prompt")
	})

	It("snaps atBottom back to true on Done when the user scrolled long ago (outside grace window)", func() {
		intent.SetAtBottomForTest(false)
		intent.SetLastUserScrollAtForTest(time.Now().Add(-30 * time.Second))

		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{Content: "final content", Done: true})

		Expect(intent.AtBottomForTest()).To(BeTrue(),
			"30s-old user scroll is outside the 5s grace window; treat as passive drift")
		Expect(notifMg.Active()).To(BeEmpty())
	})

	It("leaves atBottom false and surfaces a hint when the user scrolled within the grace window", func() {
		intent.SetAtBottomForTest(false)
		intent.SetLastUserScrollAtForTest(time.Now().Add(-1 * time.Second))

		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{Content: "final content", Done: true})

		Expect(intent.AtBottomForTest()).To(BeFalse(),
			"recent active scroll must NOT be overridden by the snap — the user is reading")
		active := notifMg.Active()
		Expect(active).NotTo(BeEmpty(),
			"a hint notification must surface so the user knows the stream completed")
		Expect(active[0].Title).To(Equal("Response complete"))
		Expect(active[0].Message).To(ContainSubstring("End"),
			"the hint must mention the End key so the user knows how to jump to the new content")
		Expect(active[0].Level).To(Equal(notification.LevelInfo))
	})

	It("leaves atBottom true alone when it was already true on Done (no work to do)", func() {
		intent.SetAtBottomForTest(true)
		intent.SetLastUserScrollAtForTest(time.Now().Add(-1 * time.Second))

		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{Content: "final content", Done: true})

		Expect(intent.AtBottomForTest()).To(BeTrue(),
			"atBottom was already true; the heuristic must be a no-op")
		Expect(notifMg.Active()).To(BeEmpty(),
			"no hint when the user is already at the bottom — there is nothing they're missing")
	})

	It("does not surface a hint when the notification manager is unset", func() {
		intent.SetNotificationManagerForTest(nil)
		intent.SetAtBottomForTest(false)
		intent.SetLastUserScrollAtForTest(time.Now().Add(-1 * time.Second))

		Expect(func() {
			intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{Content: "final content", Done: true})
		}).NotTo(Panic(),
			"a nil notification manager must not panic the heuristic")
		Expect(intent.AtBottomForTest()).To(BeFalse(),
			"recent active scroll still wins; the missing notifier just suppresses the hint silently")
	})
})
