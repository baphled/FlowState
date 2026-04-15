package chat_test

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("swarm activity pane Ctrl+T toggle (Wave 1 / T7)", func() {
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

	Describe("default visibility", func() {
		It("starts with secondaryPaneVisible == true", func() {
			Expect(intent.SecondaryPaneVisibleForTest()).To(BeTrue())
		})

		It("renders the Activity Timeline header when visible", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("Activity Timeline"))
		})
	})

	Describe("Ctrl+T hides the pane", func() {
		BeforeEach(func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
		})

		It("flips the field to false", func() {
			Expect(intent.SecondaryPaneVisibleForTest()).To(BeFalse())
		})

		It("omits the Activity Timeline header from View()", func() {
			view := intent.View()
			Expect(view).NotTo(ContainSubstring("Activity Timeline"))
		})

		It("falls back to single-pane with no vertical separator at column 70", func() {
			view := intent.View()
			// The dual-pane separator '│' appears at column 70 when rendered
			// via ScreenLayout's 70/30 branch. Single-pane rendering must not
			// contain the separator as a structural column marker.
			// We scan each line: no line should have '│' at byte-column 70
			// for single-pane fallback.
			for _, line := range strings.Split(view, "\n") {
				runes := []rune(line)
				if len(runes) > 70 && runes[70] == '│' {
					Fail("expected no separator at column 70 in single-pane fallback, got: " + line)
				}
			}
		})
	})

	Describe("Ctrl+T twice restores visibility", func() {
		It("flips the field back to true and re-renders the timeline", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			Expect(intent.SecondaryPaneVisibleForTest()).To(BeFalse())

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			Expect(intent.SecondaryPaneVisibleForTest()).To(BeTrue())

			view := intent.View()
			Expect(view).To(ContainSubstring("Activity Timeline"))
		})
	})

	Describe("StreamChunk events arriving while the pane is hidden", func() {
		It("still records events to the store so toggling back reveals them", func() {
			// Hide the pane via Ctrl+T.
			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			Expect(intent.SecondaryPaneVisibleForTest()).To(BeFalse())

			// Send a delegation chunk while the pane is hidden.
			intent.Update(chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID:     "chain-hidden",
					TargetAgent: "background-agent",
					Status:      "started",
				},
			})

			// The store must still reflect the event.
			events := intent.SwarmStoreForTest().All()
			Expect(events).To(HaveLen(1),
				"events must be recorded even when the pane is hidden — the pane is a view, not a filter")
			Expect(events[0].AgentID).To(Equal("background-agent"))

			// Toggling back reveals the event in the view.
			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			view := intent.View()
			Expect(view).To(ContainSubstring("Activity Timeline"))
			Expect(view).To(ContainSubstring("background-agent"))
		})
	})

	Describe("status-bar hint advertises Ctrl+T", func() {
		It("contains the Ctrl+T substring when the pane is visible", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("Ctrl+T"))
		})

		It("contains the Ctrl+T substring when the pane is hidden", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			view := intent.View()
			Expect(view).To(ContainSubstring("Ctrl+T"))
		})
	})

	Describe("Update return value for Ctrl+T", func() {
		It("returns no command on toggle (state mutation only)", func() {
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			Expect(cmd).To(BeNil())
		})
	})
})
