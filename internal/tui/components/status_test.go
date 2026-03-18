package components_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/components"
	"github.com/charmbracelet/lipgloss"
)

var _ = Describe("StatusBar", func() {
	var s *components.StatusBar

	BeforeEach(func() {
		s = components.New()
	})

	Describe("RenderContent", func() {
		BeforeEach(func() {
			s.Update(components.StatusBarMsg{
				Provider:    "Anthropic",
				Model:       "claude-3-opus",
				TokensUsed:  1000,
				TokenBudget: 10000,
				Mode:        "NORMAL",
			})
		})

		It("contains provider name", func() {
			Expect(s.RenderContent(80)).To(ContainSubstring("Anthropic"))
		})
		It("contains model name", func() {
			Expect(s.RenderContent(80)).To(ContainSubstring("claude-3-opus"))
		})
		It("contains token count", func() {
			Expect(s.RenderContent(80)).To(ContainSubstring("1000 / 10000"))
		})
		It("shows NORMAL mode by default", func() {
			Expect(s.RenderContent(80)).To(ContainSubstring("NORMAL"))
		})

		Context("width responsiveness", func() {
			It("truncates model name on narrow width", func() {
				s.Update(components.StatusBarMsg{
					Provider:    "Anthropic",
					Model:       "very-long-model-name-that-should-be-truncated",
					TokensUsed:  100,
					TokenBudget: 1000,
				})
				// We expect it NOT to contain the full name if width is small
				output := s.RenderContent(40)
				Expect(output).To(ContainSubstring("Anthropic")) // Always show provider
				Expect(output).NotTo(ContainSubstring("very-long-model-name-that-should-be-truncated"))
			})
		})
	})

	Context("token colour thresholds", func() {
		It("shows green style when under 70%", func() {
			Expect(components.TokenColor(60, 100)).To(Equal(lipgloss.Color("#00FF00")))
		})
		It("shows yellow style between 70-90%", func() {
			Expect(components.TokenColor(80, 100)).To(Equal(lipgloss.Color("#FFAA00")))
		})
		It("shows red style over 90%", func() {
			Expect(components.TokenColor(95, 100)).To(Equal(lipgloss.Color("#FF0000")))
		})
	})

	Describe("Update", func() {
		It("updates provider and model", func() {
			s.Update(components.StatusBarMsg{Provider: "NewProv", Model: "NewMod"})
			Expect(s.RenderContent(80)).To(ContainSubstring("NewProv"))
			Expect(s.RenderContent(80)).To(ContainSubstring("NewMod"))
		})
		It("updates token counts", func() {
			s.Update(components.StatusBarMsg{TokensUsed: 50, TokenBudget: 100})
			Expect(s.RenderContent(80)).To(ContainSubstring("50 / 100"))
		})
	})
})
