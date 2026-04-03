package widgets_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("MessageFooter", func() {
	var th theme.Theme

	BeforeEach(func() {
		th = theme.Default()
	})

	Describe("NewMessageFooter", func() {
		It("creates a footer widget without panicking", func() {
			f := widgets.NewMessageFooter(th)
			Expect(f).NotTo(BeNil())
		})

		It("renders empty string when no metadata is set", func() {
			f := widgets.NewMessageFooter(th)
			Expect(f.Render()).To(BeEmpty())
		})
	})

	Describe("Render", func() {
		Context("with mode, modelID, and duration", func() {
			It("renders ▣ with mode, modelID, and duration", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "claude-sonnet-4-20250514", 5*time.Second, false, lipgloss.Color(""))
				output := f.Render()
				Expect(output).To(ContainSubstring("▣"))
				Expect(output).To(ContainSubstring("Chat"))
				Expect(output).To(ContainSubstring("claude-sonnet-4-20250514"))
				Expect(output).To(ContainSubstring("5s"))
			})

			It("separates segments with ·", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("build", "gpt-4o", 3*time.Second, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("·"))
			})
		})

		Context("when interrupted", func() {
			It("appends · interrupted suffix", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "claude-sonnet-4-20250514", 2*time.Second, true, lipgloss.Color(""))
				output := f.Render()
				Expect(output).To(ContainSubstring("interrupted"))
			})

			It("does not append interrupted when not interrupted", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "claude-sonnet-4-20250514", 2*time.Second, false, lipgloss.Color(""))
				output := f.Render()
				Expect(output).NotTo(ContainSubstring("interrupted"))
			})
		})

		Context("when agentColor is set", func() {
			It("renders without panic when agentColor is provided", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "claude-sonnet-4-20250514", 1*time.Second, false, lipgloss.Color("#ff6600"))
				output := f.Render()
				Expect(output).To(ContainSubstring("▣"))
			})

			It("still renders ▣ indicator with zero agentColor", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "claude-sonnet-4-20250514", 1*time.Second, false, lipgloss.Color(""))
				output := f.Render()
				Expect(output).To(ContainSubstring("▣"))
			})
		})

		Context("when mode is empty", func() {
			It("renders without mode segment", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("", "claude-sonnet-4-20250514", 1*time.Second, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("▣"))
				Expect(output).To(ContainSubstring("claude-sonnet-4-20250514"))
				Expect(output).NotTo(ContainSubstring("· ·"))
			})
		})

		Context("duration formatting", func() {
			It("renders 0ms for zero duration", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "model-x", 0, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("0ms"))
			})

			It("renders duration in ms for sub-second durations", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "model-x", 450*time.Millisecond, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("450ms"))
			})

			It("renders duration in seconds for second-range durations", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "model-x", 5*time.Second, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("5s"))
			})

			It("renders duration in minutes and seconds for long durations", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "model-x", 2*time.Minute+30*time.Second, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("2m 30s"))
			})

			It("renders 1s exactly at the one-second boundary", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "model-x", 1*time.Second, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("1s"))
			})

			It("renders 59s for 59 seconds", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "model-x", 59*time.Second, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("59s"))
			})

			It("renders 1m 0s for exactly 60 seconds", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("chat", "model-x", 60*time.Second, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("1m 0s"))
			})
		})

		Context("mode title-casing", func() {
			It("title-cases the mode", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("build", "model-x", 1*time.Second, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("Build"))
			})

			It("title-cases plan mode", func() {
				f := widgets.NewMessageFooter(th)
				f.SetMetadata("plan", "model-x", 1*time.Second, false, lipgloss.Color(""))
				output := stripANSI(f.Render())
				Expect(output).To(ContainSubstring("Plan"))
			})
		})
	})
})
