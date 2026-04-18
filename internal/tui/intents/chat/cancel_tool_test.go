package chat_test

import (
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// Ctrl+K — tool-scoped cancel (P17.S2).
//
// The engine has no tool-scoped cancel API, so the product code cancels
// the whole stream via the same path double-Esc uses. These specs pin
// the three observable outcomes:
//
//   - When a tool is active: the stored streamCancel fires, the
//     activeToolCall slot is cleared, the user-cancelled flag is set
//     (so handleStreamChunk does not surface a spurious error), and a
//     notification titled "Tool cancelled" appears.
//   - When no tool is active: the key is a no-op — no cancel, no
//     notification.
//   - The behaviour is idempotent: hitting Ctrl+K twice in quick
//     succession does not explode and leaves the intent in the same
//     post-cancel state.
var _ = Describe("Ctrl+K tool cancel", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "tool-cancel-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	Describe("when a tool is actively executing", func() {
		It("cancels the stream, clears the active tool slot, and surfaces a notification", func() {
			var cancelCalls int32
			intent.SetStreamCancelForTest(func() {
				atomic.AddInt32(&cancelCalls, 1)
			})
			intent.SetActiveToolCallForTest("bash: sleep 60")

			cmd := intent.CancelActiveToolForTest()
			Expect(cmd).To(BeNil())

			Expect(atomic.LoadInt32(&cancelCalls)).To(Equal(int32(1)),
				"expected the stream cancel closure to fire exactly once")
			Expect(intent.ActiveToolCallForTest()).To(BeEmpty(),
				"expected the active tool slot to be cleared")
			Expect(intent.UserCancelledForTest()).To(BeTrue(),
				"expected the user-cancelled flag to be set so the stream "+
					"consumer does not surface a spurious error")

			mgr := intent.NotificationManagerForTest()
			Expect(mgr).NotTo(BeNil())
			var titles, messages []string
			for _, n := range mgr.Active() {
				titles = append(titles, n.Title)
				messages = append(messages, n.Message)
			}
			Expect(strings.Join(titles, "|")).To(ContainSubstring("Tool cancelled"))
			Expect(strings.Join(messages, "|")).To(ContainSubstring("Tool execution cancelled."))
		})
	})

	Describe("when no tool is executing", func() {
		It("leaves the intent untouched and does not fire a notification", func() {
			var cancelCalls int32
			intent.SetStreamCancelForTest(func() {
				atomic.AddInt32(&cancelCalls, 1)
			})
			// activeToolCall deliberately left empty.

			cmd := intent.CancelActiveToolForTest()
			Expect(cmd).To(BeNil())

			Expect(atomic.LoadInt32(&cancelCalls)).To(Equal(int32(0)),
				"expected no cancel when no tool is running")
			Expect(intent.UserCancelledForTest()).To(BeFalse())

			mgr := intent.NotificationManagerForTest()
			for _, n := range mgr.Active() {
				Expect(n.Title).NotTo(ContainSubstring("Tool cancelled"),
					"no tool-cancel notification should appear in the "+
						"no-tool-running path")
			}
		})
	})

	Describe("calling cancel twice in quick succession", func() {
		It("is idempotent: second call is a no-op with no new notification", func() {
			var cancelCalls int32
			intent.SetStreamCancelForTest(func() {
				atomic.AddInt32(&cancelCalls, 1)
			})
			intent.SetActiveToolCallForTest("bash: sleep 60")

			_ = intent.CancelActiveToolForTest()
			_ = intent.CancelActiveToolForTest()

			Expect(atomic.LoadInt32(&cancelCalls)).To(Equal(int32(1)),
				"expected the second cancel to short-circuit on the "+
					"now-empty activeToolCall guard")
		})
	})
})
