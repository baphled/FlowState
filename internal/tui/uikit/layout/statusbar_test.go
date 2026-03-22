package layout_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
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
		It("shows no badge or mode indicator when idle", func() {
			output := sb.RenderContent(80)
			Expect(output).NotTo(ContainSubstring("CHAT"))
			Expect(output).NotTo(ContainSubstring("NORMAL"))
		})

		Context("streaming", func() {
			It("shows spinner character when streaming", func() {
				sb.SetStreaming(true, 0)
				output := sb.RenderContent(80)
				Expect(output).To(ContainSubstring("⠋"))
			})

			It("shows no spinner when idle", func() {
				sb.SetStreaming(false, 0)
				output := sb.RenderContent(80)
				Expect(output).NotTo(ContainSubstring("⠋"))
			})
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
		var th theme.Theme

		BeforeEach(func() {
			th = theme.Default()
		})

		It("shows success style when under 70%", func() {
			color := layout.TokenColor(60, 100, th)
			Expect(color).To(Equal(th.SuccessColor()))
		})
		It("shows warning style between 70-90%", func() {
			color := layout.TokenColor(80, 100, th)
			Expect(color).To(Equal(th.WarningColor()))
		})
		It("shows error style over 90%", func() {
			color := layout.TokenColor(95, 100, th)
			Expect(color).To(Equal(th.ErrorColor()))
		})
		It("shows muted style when budget is zero", func() {
			color := layout.TokenColor(10, 0, th)
			Expect(color).To(Equal(th.MutedColor()))
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
		It("updates agentID", func() {
			sb.Update(layout.StatusBarMsg{AgentID: "test-agent"})
			Expect(sb.RenderContent(80)).To(ContainSubstring("test-agent"))
		})
	})

	Describe("loaded skills display", func() {
		It("renders loaded skills when set", func() {
			sb.Update(layout.StatusBarMsg{
				Provider:     "Anthropic",
				Model:        "claude-3-opus",
				LoadedSkills: []string{"pre-action", "memory-keeper"},
			})
			output := sb.RenderContent(80)
			Expect(output).To(ContainSubstring("pre-action"))
			Expect(output).To(ContainSubstring("memory-keeper"))
		})

		It("renders no skill line when skills are empty", func() {
			sb.Update(layout.StatusBarMsg{
				Provider: "Anthropic",
				Model:    "claude-3-opus",
			})
			output := sb.RenderContent(80)
			Expect(output).NotTo(ContainSubstring("Skills:"))
		})

		It("preserves skills across updates that do not set skills", func() {
			sb.Update(layout.StatusBarMsg{
				LoadedSkills: []string{"golang", "tdd-first"},
			})
			sb.Update(layout.StatusBarMsg{
				Provider: "OpenAI",
			})
			output := sb.RenderContent(80)
			Expect(output).To(ContainSubstring("golang"))
			Expect(output).To(ContainSubstring("tdd-first"))
		})
	})
})
