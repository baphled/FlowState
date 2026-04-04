package widgets_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("MessageWidget metadata rendering", Label("integration"), func() {
	var th theme.Theme

	BeforeEach(func() {
		th = theme.Default()
	})

	Describe("tool result rendering with BlockTool", func() {
		Context("when ToolName is set on a tool_result message", func() {
			It("renders the tool name in the output", func() {
				w := widgets.NewMessageWidget("tool_result", "file contents here", th)
				w.SetToolName("read")
				output := w.Render(80)
				Expect(output).To(ContainSubstring("read"))
			})

			It("does not render the package emoji when ToolName is set", func() {
				w := widgets.NewMessageWidget("tool_result", "output data", th)
				w.SetToolName("bash")
				output := w.Render(80)
				Expect(output).NotTo(ContainSubstring("📤"))
			})

			It("renders with tool input visible via BlockTool", func() {
				w := widgets.NewMessageWidget("tool_result", "build output", th)
				w.SetToolName("bash")
				w.SetToolInput("go build ./...")
				output := w.Render(80)
				Expect(output).To(ContainSubstring("go build ./..."))
			})

			It("renders the $ prefix for bash tool via BlockTool", func() {
				w := widgets.NewMessageWidget("tool_result", "output", th)
				w.SetToolName("bash")
				w.SetToolInput("ls -la")
				output := w.Render(80)
				Expect(output).To(ContainSubstring("$"))
			})
		})
	})

	Describe("assistant label with agent colour", func() {
		Context("when AgentColor is set", func() {
			It("renders the Assistant label with the custom agent colour", func() {
				w := widgets.NewMessageWidget("assistant", "hello", th)
				w.SetAgentColor(lipgloss.Color("#ff6600"))
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := w.Render(80)
				Expect(output).To(ContainSubstring("Assistant"))
			})

			It("renders the Assistant label when AgentColor is zero value", func() {
				w := widgets.NewMessageWidget("assistant", "hello", th)
				w.SetAgentColor(lipgloss.Color(""))
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := w.Render(80)
				Expect(output).To(ContainSubstring("Assistant"))
			})

			It("uses theme secondary colour when AgentColor is not set", func() {
				w := widgets.NewMessageWidget("assistant", "hello", th)
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := w.Render(80)
				Expect(output).To(ContainSubstring("Assistant"))
			})
		})
	})

	Describe("model ID rendering in footer", func() {
		Context("when ModelID is set on an assistant message", func() {
			It("renders the ▣ indicator in the output", func() {
				w := widgets.NewMessageWidget("assistant", "response text", th)
				w.SetModelID("claude-sonnet-4-20250514")
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := w.Render(80)
				Expect(output).To(ContainSubstring("▣"))
			})

			It("renders the model ID string in the footer", func() {
				w := widgets.NewMessageWidget("assistant", "response text", th)
				w.SetModelID("gpt-4o-mini")
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := w.Render(80)
				Expect(output).To(ContainSubstring("gpt-4o-mini"))
			})

			It("does not render footer when ModelID is empty", func() {
				w := widgets.NewMessageWidget("assistant", "response text", th)
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := w.Render(80)
				Expect(output).NotTo(ContainSubstring("▣"))
			})
		})
	})

	Describe("duration rendering in footer", func() {
		Context("when Duration and ModelID are set on an assistant message", func() {
			It("renders duration in milliseconds for sub-second values", func() {
				w := widgets.NewMessageWidget("assistant", "fast response", th)
				w.SetModelID("claude-haiku")
				w.SetDuration(350 * time.Millisecond)
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := stripANSI(w.Render(80))
				Expect(output).To(ContainSubstring("350ms"))
			})

			It("renders duration in seconds for second-range values", func() {
				w := widgets.NewMessageWidget("assistant", "response", th)
				w.SetModelID("claude-sonnet-4-20250514")
				w.SetDuration(7 * time.Second)
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := stripANSI(w.Render(80))
				Expect(output).To(ContainSubstring("7s"))
			})

			It("renders duration in minutes and seconds for long durations", func() {
				w := widgets.NewMessageWidget("assistant", "long response", th)
				w.SetModelID("claude-opus-4-5")
				w.SetDuration(1*time.Minute + 15*time.Second)
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := stripANSI(w.Render(80))
				Expect(output).To(ContainSubstring("1m 15s"))
			})
		})
	})

	Describe("interrupted indicator in footer", func() {
		Context("when the message was interrupted", func() {
			It("renders the interrupted label in the footer", func() {
				w := widgets.NewMessageWidget("assistant", "partial response", th)
				w.SetModelID("claude-sonnet-4-20250514")
				w.SetInterrupted(true)
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := w.Render(80)
				Expect(output).To(ContainSubstring("interrupted"))
			})
		})

		Context("when the message was not interrupted", func() {
			It("does not render the interrupted label", func() {
				w := widgets.NewMessageWidget("assistant", "complete response", th)
				w.SetModelID("claude-sonnet-4-20250514")
				w.SetInterrupted(false)
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := w.Render(80)
				Expect(output).NotTo(ContainSubstring("interrupted"))
			})
		})
	})
})
