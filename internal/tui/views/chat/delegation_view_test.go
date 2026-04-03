package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/baphled/flowstate/internal/tui/views/chat"
)

var _ = Describe("DelegationStatusWidget", func() {
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
			ModelName:    "claude-3-opus",
			ProviderName: "anthropic",
			Description:  "Building a plan...",
		}
	})

	It("renders the running state with details", func() {
		widget := chat.NewDelegationStatusWidget(info, th)
		rendered := widget.Render()

		Expect(rendered).To(ContainSubstring("Delegation: planner"))
		Expect(rendered).To(ContainSubstring("claude-3-opus/anthropic"))
		Expect(rendered).To(ContainSubstring("running"))
		Expect(rendered).To(ContainSubstring("Building a plan..."))
		// Spinner frame 0 is ⠋
		Expect(rendered).To(ContainSubstring("⠋"))
	})

	It("renders the completed state with success symbol", func() {
		info.Status = "completed"
		info.Description = "Plan created"

		widget := chat.NewDelegationStatusWidget(info, th)
		rendered := widget.Render()

		Expect(rendered).To(ContainSubstring("Delegation: planner"))
		Expect(rendered).To(ContainSubstring("completed"))
		Expect(rendered).To(ContainSubstring("Plan created"))
		Expect(rendered).To(ContainSubstring("✓"))
	})

	It("renders the failed state with error symbol", func() {
		info.Status = "failed"
		info.Description = "Something went wrong"

		widget := chat.NewDelegationStatusWidget(info, th)
		rendered := widget.Render()

		Expect(rendered).To(ContainSubstring("Delegation: planner"))
		Expect(rendered).To(ContainSubstring("failed"))
		Expect(rendered).To(ContainSubstring("✗"))
	})

	It("handles nil info gracefully", func() {
		widget := chat.NewDelegationStatusWidget(nil, th)
		Expect(widget.Render()).To(BeEmpty())
	})

	It("updates spinner frame", func() {
		widget := chat.NewDelegationStatusWidget(info, th)
		widget.SetFrame(1)
		rendered := widget.Render()
		// Spinner frame 1 is ⠙
		Expect(rendered).To(ContainSubstring("⠙"))
	})

	It("renders the model name even if provider is missing (Fix E)", func() {
		info.ModelName = "claude-opus-4.5"
		info.ProviderName = ""
		widget := chat.NewDelegationStatusWidget(info, th)
		rendered := widget.Render()
		Expect(rendered).To(ContainSubstring("claude-opus-4.5"))
		// Should not panic or omit model name if provider is empty
	})
})
