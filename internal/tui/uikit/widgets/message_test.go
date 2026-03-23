package widgets_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("MessageWidget", func() {
	var th theme.Theme

	BeforeEach(func() {
		th = theme.Default()
	})

	Describe("NewMessageWidget", func() {
		It("creates a widget with the given role and content", func() {
			w := widgets.NewMessageWidget("user", "hello", th)
			Expect(w).NotTo(BeNil())
		})
	})

	Describe("Render", func() {
		Context("user messages", func() {
			It("includes the You label", func() {
				w := widgets.NewMessageWidget("user", "hello world", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("You"))
			})

			It("includes the message content", func() {
				w := widgets.NewMessageWidget("user", "test message", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("test message"))
			})
		})

		Context("assistant messages", func() {
			It("includes the Assistant label", func() {
				w := widgets.NewMessageWidget("assistant", "hi there", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("Assistant"))
			})

			It("includes the message content", func() {
				w := widgets.NewMessageWidget("assistant", "response text", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("response text"))
			})
		})

		Context("system messages", func() {
			It("renders as dimmed annotation for slash command output", func() {
				w := widgets.NewMessageWidget("system", "Available commands:\n  /help", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("Available commands"))
				Expect(output).NotTo(ContainSubstring("You"))
				Expect(output).NotTo(ContainSubstring("Assistant"))
			})
		})

		Context("with nil theme", func() {
			It("still renders without panic", func() {
				w := widgets.NewMessageWidget("user", "no theme", nil)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("no theme"))
			})
		})

		Context("with custom markdown renderer", func() {
			It("uses the renderer for assistant messages", func() {
				w := widgets.NewMessageWidget("assistant", "markdown content", th)
				w.SetMarkdownRenderer(func(content string, _ int) string {
					return "[rendered]" + content
				})
				output := w.Render(80)
				Expect(output).To(ContainSubstring("[rendered]markdown content"))
			})

			It("does not use the renderer for user messages", func() {
				w := widgets.NewMessageWidget("user", "plain text", th)
				w.SetMarkdownRenderer(func(_ string, _ int) string {
					return "[should-not-appear]"
				})
				output := w.Render(80)
				Expect(output).NotTo(ContainSubstring("[should-not-appear]"))
				Expect(output).To(ContainSubstring("plain text"))
			})
		})
	})
})
