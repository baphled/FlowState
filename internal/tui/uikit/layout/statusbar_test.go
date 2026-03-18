package layout_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/charmbracelet/lipgloss"
)

var _ = Describe("StatusBar", func() {
	var sb *layout.StatusBar

	BeforeEach(func() {
		sb = layout.NewStatusBar(80)
	})

	Describe("RenderContent", func() {
		BeforeEach(func() {
			sb.Update(layout.StatusBarMsg{
				Provider:    "Anthropic",
				Model:       "claude-3-opus",
				TokensUsed:  1000,
				TokenBudget: 10000,
				Mode:        "NORMAL",
			})
		})

		It("contains provider name", func() {
			Expect(sb.RenderContent(80)).To(ContainSubstring("Anthropic"))
		})
		It("contains model name", func() {
			Expect(sb.RenderContent(80)).To(ContainSubstring("claude-3-opus"))
		})
		It("contains token count", func() {
			Expect(sb.RenderContent(80)).To(ContainSubstring("1000 / 10000"))
		})
		It("shows NORMAL mode by default", func() {
			Expect(sb.RenderContent(80)).To(ContainSubstring("NORMAL"))
		})

		Context("width responsiveness", func() {
			It("truncates model name on narrow width", func() {
				sb.Update(layout.StatusBarMsg{
					Provider:    "Anthropic",
					Model:       "very-long-model-name-that-should-be-truncated",
					TokensUsed:  100,
					TokenBudget: 1000,
				})
				output := sb.RenderContent(40)
				Expect(output).To(ContainSubstring("Anthropic"))
				Expect(output).NotTo(ContainSubstring("very-long-model-name-that-should-be-truncated"))
			})
		})
	})

	Context("token colour thresholds", func() {
		It("shows green style when under 70%", func() {
			Expect(layout.TokenColor(60, 100)).To(Equal(lipgloss.Color("#00FF00")))
		})
		It("shows yellow style between 70-90%", func() {
			Expect(layout.TokenColor(80, 100)).To(Equal(lipgloss.Color("#FFAA00")))
		})
		It("shows red style over 90%", func() {
			Expect(layout.TokenColor(95, 100)).To(Equal(lipgloss.Color("#FF0000")))
		})
	})

	Describe("Update", func() {
		It("updates provider and model", func() {
			sb.Update(layout.StatusBarMsg{Provider: "NewProv", Model: "NewMod"})
			Expect(sb.RenderContent(80)).To(ContainSubstring("NewProv"))
			Expect(sb.RenderContent(80)).To(ContainSubstring("NewMod"))
		})
		It("updates token counts", func() {
			sb.Update(layout.StatusBarMsg{TokensUsed: 50, TokenBudget: 100})
			Expect(sb.RenderContent(80)).To(ContainSubstring("50 / 100"))
		})
	})
})
