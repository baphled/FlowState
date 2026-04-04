package widgets_test

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("MessageFooter integration rendering", Label("integration"), func() {
	var th theme.Theme

	BeforeEach(func() {
		th = theme.Default()
	})

	Describe("message content before footer", func() {
		Context("when an assistant message has both content and a footer", func() {
			It("renders message content before the ▣ footer indicator", func() {
				w := widgets.NewMessageWidget("assistant", "The response text here.", th)
				w.SetModelID("claude-sonnet-4-20250514")
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := w.Render(80)
				contentPos := strings.Index(output, "The response text here.")
				footerPos := strings.Index(output, "▣")
				Expect(contentPos).To(BeNumerically(">=", 0), "response content should appear")
				Expect(footerPos).To(BeNumerically(">=", 0), "footer indicator should appear")
				Expect(contentPos).To(BeNumerically("<", footerPos), "content must appear before footer")
			})

			It("renders the Assistant label before the message content", func() {
				w := widgets.NewMessageWidget("assistant", "response body", th)
				w.SetModelID("gpt-4o")
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := w.Render(80)
				labelPos := strings.Index(output, "Assistant")
				contentPos := strings.Index(output, "response body")
				Expect(labelPos).To(BeNumerically(">=", 0), "Assistant label should appear")
				Expect(contentPos).To(BeNumerically(">=", 0), "content should appear")
				Expect(labelPos).To(BeNumerically("<", contentPos), "label must appear before content")
			})
		})
	})

	Describe("single footer line with model ID, duration, and mode", func() {
		Context("when all metadata fields are set", func() {
			It("renders model ID, duration, and mode in the footer output", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "claude-sonnet-4-20250514", 3*time.Second, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("claude-sonnet-4-20250514"))
				Expect(output).To(ContainSubstring("3s"))
				Expect(output).To(ContainSubstring("Chat"))
			})

			It("combines all three segments in a single line", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("build", "gpt-4o", 2*time.Second, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(strings.Count(output, "\n")).To(BeZero(), "footer should be a single line")
			})

			It("separates segments with the · character", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("plan", "claude-opus-4-5", 10*time.Second, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("·"))
			})

			It("title-cases the mode segment in the footer line", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("build", "model-x", 1*time.Second, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("Build"))
				Expect(output).NotTo(ContainSubstring("build ·"))
			})
		})

		Context("when mode is omitted", func() {
			It("renders model ID and duration without a mode segment", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("", "claude-haiku", 500*time.Millisecond, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("claude-haiku"))
				Expect(output).To(ContainSubstring("500ms"))
				Expect(output).NotTo(ContainSubstring("· ·"))
			})
		})

		Context("when agentColor is provided alongside metadata", func() {
			It("renders the ▣ indicator regardless of agentColor value", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "claude-sonnet-4-20250514", 1*time.Second, false, lipgloss.Color("#9900ff"))
				output := f.Render()
				Expect(output).To(ContainSubstring("▣"))
			})
		})

		Context("when interrupted is appended", func() {
			It("appends interrupted as a final segment in the single footer line", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "claude-sonnet-4-20250514", 2*time.Second, true, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("interrupted"))
				Expect(strings.Count(output, "\n")).To(BeZero(), "footer must remain single line")
			})
		})
	})
})
