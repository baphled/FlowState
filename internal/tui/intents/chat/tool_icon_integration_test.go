package chat_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("ToolCallWidget Integration", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
		intent.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		intent.SetStreamingForTest(true)
	})

	It("renders $ icon for bash running tool calls", func() {
		intent.Update(chat.StreamChunkMsg{ToolCallName: "bash", ToolStatus: "running"})
		Expect(intent.View()).To(ContainSubstring("$"))
	})

	It("renders ← icon for write running tool calls", func() {
		intent.Update(chat.StreamChunkMsg{ToolCallName: "write", ToolStatus: "running"})
		Expect(intent.View()).To(ContainSubstring("←"))
	})

	It("renders ◆ icon for glob running tool calls", func() {
		intent.Update(chat.StreamChunkMsg{ToolCallName: "glob", ToolStatus: "running"})
		Expect(intent.View()).To(ContainSubstring("◆"))
	})

	It("renders ⚡ for unknown running tool calls", func() {
		intent.Update(chat.StreamChunkMsg{ToolCallName: "custom_tool", ToolStatus: "running"})
		Expect(intent.View()).To(ContainSubstring("⚡"))
	})

	Context("during active tool calls", func() {
		It("renders Writing command… for bash running", func() {
			intent.Update(chat.StreamChunkMsg{ToolCallName: "bash", ToolStatus: "running"})
			Expect(intent.View()).To(ContainSubstring("Writing command…"))
		})

		It("renders Searching… for grep running", func() {
			intent.Update(chat.StreamChunkMsg{ToolCallName: "grep", ToolStatus: "running"})
			Expect(intent.View()).To(ContainSubstring("Searching…"))
		})

		It("renders Running… for unknown tool running", func() {
			intent.Update(chat.StreamChunkMsg{ToolCallName: "custom_tool", ToolStatus: "running"})
			Expect(intent.View()).To(ContainSubstring("Running…"))
		})
	})
})
