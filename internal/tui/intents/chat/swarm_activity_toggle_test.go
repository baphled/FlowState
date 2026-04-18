package chat_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// Ctrl+T semantics changed in P11: it no longer toggles secondary-pane
// visibility (Wave 1 / T7), it now cycles through the swarm filter
// profiles. This file keeps the Wave-1-era invariants that remain
// meaningful — the pane stays visible across Ctrl+T presses, events keep
// being recorded regardless of which profile is active, and the status-bar
// hint continues to advertise Ctrl+T — and rewrites the old "hides the
// pane" expectations to match the new behaviour.
var _ = Describe("swarm activity pane Ctrl+T binding (P11)", func() {
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
		It("renders the Activity Timeline header when visible", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("Activity Timeline"))
		})
	})

	Describe("Ctrl+T does NOT toggle pane visibility (P11 changed semantics)", func() {
		BeforeEach(func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
		})

		It("keeps the Activity Timeline header rendered", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("Activity Timeline"))
		})
	})

	Describe("Ctrl+T across the full cycle keeps the pane visible", func() {
		It("shows the timeline after each of four presses (cycle length 3 + wrap)", func() {
			for press := range 4 {
				intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
				view := intent.View()
				Expect(view).To(ContainSubstring("Activity Timeline"),
					"timeline must remain visible across the whole cycle (press %d)", press)
			}
		})
	})

	Describe("StreamChunk events continue to be recorded across Ctrl+T presses", func() {
		It("stores events regardless of the active filter profile", func() {
			// Send a delegation chunk; it must be recorded whatever profile is active.
			intent.Update(chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID:     "chain-initial",
					TargetAgent: "background-agent",
					Status:      "started",
				},
			})
			events := intent.SwarmStoreForTest().All()
			Expect(events).To(HaveLen(1),
				"delegation must be stored before any filter cycling")

			// Cycle to profileToolsOnly (hides delegation).
			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			intent.Update(chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID:     "chain-hidden",
					TargetAgent: "other-agent",
					Status:      "started",
				},
			})

			events = intent.SwarmStoreForTest().All()
			Expect(events).To(HaveLen(2),
				"events must be recorded even when the active profile hides them — the filter is a view, not a gate")
		})
	})

	Describe("status-bar hint advertises Ctrl+T", func() {
		It("contains the Ctrl+T substring at every point in the cycle", func() {
			for press := range 3 {
				view := intent.View()
				Expect(view).To(ContainSubstring("Ctrl+T"),
					"status hint must advertise Ctrl+T at press %d", press)
				intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			}
		})
	})

	Describe("Update return value for Ctrl+T", func() {
		It("returns no command on filter cycle (state mutation only)", func() {
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			Expect(cmd).To(BeNil())
		})
	})
})
