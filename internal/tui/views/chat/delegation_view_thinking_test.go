package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/baphled/flowstate/internal/tui/views/chat"
)

var _ = Describe("DelegationStatusWidget thinking display", Label("integration"), func() {
	var (
		th   theme.Theme
		info *provider.DelegationInfo
	)

	BeforeEach(func() {
		th = theme.Default()
		info = &provider.DelegationInfo{
			SourceAgent:  "coordinator",
			TargetAgent:  "planner",
			Status:       "running",
			ModelName:    "claude-opus-4-5",
			ProviderName: "anthropic",
			Description:  "Analysing requirements",
		}
	})

	Describe("thinking content rendering", func() {
		Context("when thinking text is provided", func() {
			It("renders the thinking text in the widget output", func() {
				widget := chat.NewDelegationStatusWidgetWithThinking(info, th, "Evaluating constraints...")
				rendered := widget.Render()

				Expect(rendered).To(ContainSubstring("Evaluating constraints..."))
			})

			It("renders thinking text alongside delegation status", func() {
				widget := chat.NewDelegationStatusWidgetWithThinking(info, th, "Breaking down the problem")
				rendered := widget.Render()

				Expect(rendered).To(ContainSubstring("planner"))
				Expect(rendered).To(ContainSubstring("Breaking down the problem"))
			})

			It("renders thinking text alongside provider metadata", func() {
				widget := chat.NewDelegationStatusWidgetWithThinking(info, th, "Forming a plan")
				rendered := widget.Render()

				Expect(rendered).To(ContainSubstring("claude-opus-4-5/anthropic"))
				Expect(rendered).To(ContainSubstring("Forming a plan"))
			})
		})

		Context("when thinking text is empty", func() {
			It("renders without any thinking section", func() {
				widget := chat.NewDelegationStatusWidgetWithThinking(info, th, "")
				rendered := widget.Render()

				Expect(rendered).To(ContainSubstring("planner"))
				Expect(rendered).NotTo(ContainSubstring("💭"))
			})

			It("behaves identically to NewDelegationStatusWidget with no thinking", func() {
				withoutThinking := chat.NewDelegationStatusWidget(info, th)
				withEmptyThinking := chat.NewDelegationStatusWidgetWithThinking(info, th, "")

				Expect(withoutThinking.Render()).To(Equal(withEmptyThinking.Render()))
			})
		})

		Context("when delegation is in completed state with thinking", func() {
			BeforeEach(func() {
				info.Status = "completed"
				info.Description = "Plan created successfully"
			})

			It("renders completed status alongside thinking text", func() {
				widget := chat.NewDelegationStatusWidgetWithThinking(info, th, "Final review complete")
				rendered := widget.Render()

				Expect(rendered).To(ContainSubstring("✓"))
				Expect(rendered).To(ContainSubstring("Final review complete"))
			})
		})

		Context("when delegation info is nil", func() {
			It("returns empty string even when thinking text is provided", func() {
				widget := chat.NewDelegationStatusWidgetWithThinking(nil, th, "Some thinking")
				Expect(widget.Render()).To(BeEmpty())
			})
		})
	})

	Describe("delegation status with provider info", func() {
		Context("with full provider details", func() {
			It("renders source and target agent names", func() {
				widget := chat.NewDelegationStatusWidget(info, th)
				rendered := widget.Render()

				Expect(rendered).To(ContainSubstring("planner"))
			})

			It("renders model and provider name together", func() {
				widget := chat.NewDelegationStatusWidget(info, th)
				rendered := widget.Render()

				Expect(rendered).To(ContainSubstring("claude-opus-4-5/anthropic"))
			})

			It("renders running status with spinner", func() {
				widget := chat.NewDelegationStatusWidget(info, th)
				rendered := widget.Render()

				Expect(rendered).To(ContainSubstring("running"))
				Expect(rendered).To(ContainSubstring("⠋"))
			})

			It("renders description text when present", func() {
				widget := chat.NewDelegationStatusWidget(info, th)
				rendered := widget.Render()

				Expect(rendered).To(ContainSubstring("Analysing requirements"))
			})
		})

		Context("with only model name (no provider)", func() {
			BeforeEach(func() {
				info.ProviderName = ""
			})

			It("still renders the model name", func() {
				widget := chat.NewDelegationStatusWidget(info, th)
				rendered := widget.Render()

				Expect(rendered).To(ContainSubstring("claude-opus-4-5"))
			})

			It("does not panic when provider name is missing", func() {
				widget := chat.NewDelegationStatusWidget(info, th)
				Expect(func() { widget.Render() }).NotTo(Panic())
			})
		})

		Context("with failed status", func() {
			BeforeEach(func() {
				info.Status = "failed"
			})

			It("renders failure icon", func() {
				widget := chat.NewDelegationStatusWidget(info, th)
				rendered := widget.Render()

				Expect(rendered).To(ContainSubstring("✗"))
			})

			It("renders the failed status label", func() {
				widget := chat.NewDelegationStatusWidget(info, th)
				rendered := widget.Render()

				Expect(rendered).To(ContainSubstring("failed"))
			})
		})
	})
})
