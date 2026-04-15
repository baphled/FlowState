package chat_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("swarm activity pane wiring", func() {
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
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	Describe("View renders secondary pane when terminal is wide enough", func() {
		It("includes the Activity Timeline header at 100x24", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			view := intent.View()
			Expect(view).To(ContainSubstring("Activity Timeline"))
		})
	})

	Describe("View falls back to single-pane below dual-pane threshold", func() {
		It("omits the Activity Timeline header at 79x24", func() {
			intent.Update(tea.WindowSizeMsg{Width: 79, Height: 24})

			view := intent.View()
			Expect(view).NotTo(ContainSubstring("Activity Timeline"))
		})
	})
})
