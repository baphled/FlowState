package chat_test

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// Covers P1/B11 — pressing Ctrl+E on an empty swarm store must surface user
// feedback rather than silently dropping the keypress. Previously
// openEventDetails() returned nil when len(allEvents) == 0, leaving the user
// with no indication that the key had been received.
var _ = Describe("openEventDetails empty-timeline feedback (B11)", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
		intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	Describe("when the swarm store is empty", func() {
		It("surfaces a notification instead of silently returning nil", func() {
			// Precondition: store is empty.
			Expect(intent.SwarmStoreForTest().All()).To(BeEmpty())

			// Dispatch Ctrl+E via the public key path so this test also
			// covers the keymap wiring, not just the internal helper.
			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlE})

			activeMessages := collectActiveNotificationMessages(intent)
			Expect(activeMessages).NotTo(BeEmpty(),
				"Ctrl+E on an empty timeline must emit a notification")

			// The wording does not need to match exactly; the contract is
			// that the user is told the timeline is empty. Accept any of
			// the reasonable phrasings the implementation might pick.
			joined := strings.ToLower(strings.Join(activeMessages, " | "))
			Expect(joined).To(
				SatisfyAny(
					ContainSubstring("no activity"),
					ContainSubstring("no events"),
					ContainSubstring("timeline is empty"),
					ContainSubstring("empty"),
				),
				"notification must indicate the timeline is empty, got: %v", activeMessages)
		})

		It("does not open the event details modal", func() {
			Expect(intent.SwarmStoreForTest().All()).To(BeEmpty())

			cmd := intent.OpenEventDetailsForTest()
			if cmd == nil {
				// Fine — implementation may add the notification
				// synchronously and return nil. The key contract is that
				// no ShowModalMsg is emitted.
				return
			}
			msg := cmd()
			if msg == nil {
				return
			}
			_, ok := msg.(chat.ShowModalMsgForTest)
			Expect(ok).To(BeFalse(),
				"empty-timeline Ctrl+E must not emit a ShowModalMsg")
		})
	})

	Describe("when the swarm store has at least one event", func() {
		It("still opens the event details modal as before", func() {
			intent.SwarmStoreForTest().Append(streaming.SwarmEvent{
				Type:    streaming.EventDelegation,
				Status:  "started",
				AgentID: "qa",
			})

			cmd := intent.OpenEventDetailsForTest()
			Expect(cmd).NotTo(BeNil(),
				"non-empty timeline must still open the modal")

			msg := cmd()
			_, ok := msg.(chat.ShowModalMsgForTest)
			Expect(ok).To(BeTrue(), "expected a ShowModalMsg")
		})
	})
})

// collectActiveNotificationMessages extracts Message strings from the
// intent's notification manager so the test can assert the feedback shown
// to the user without coupling to rendering specifics.
func collectActiveNotificationMessages(i *chat.Intent) []string {
	mgr := i.NotificationManagerForTest()
	if mgr == nil {
		return nil
	}
	var out []string
	for _, n := range mgr.Active() {
		out = append(out, n.Message)
		if n.Title != "" {
			out = append(out, n.Title)
		}
	}
	return out
}
