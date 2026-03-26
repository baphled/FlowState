package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
)

var _ = Describe("DelegationStatusComponent", func() {
	var (
		c    *chat.DelegationStatusComponent
		th   theme.Theme
		info *provider.DelegationInfo
	)

	BeforeEach(func() {
		th = theme.Default()
		c = chat.NewDelegationStatusComponent(th)
		info = &provider.DelegationInfo{
			TargetAgent:  "planner",
			Status:       "running",
			ModelName:    "claude-3-opus",
			ProviderName: "anthropic",
			Description:  "Thinking...",
		}
	})

	It("starts empty", func() {
		Expect(c.View()).To(BeEmpty())
	})

	It("renders info after SetInfo", func() {
		c.SetInfo(info)
		rendered := c.View()
		Expect(rendered).To(ContainSubstring("planner"))
		Expect(rendered).To(ContainSubstring("Thinking..."))
	})

	It("updates frame", func() {
		c.SetInfo(info)
		c.SetFrame(1)
		rendered := c.View()
		Expect(rendered).To(ContainSubstring("⠙")) // Frame 1
	})

	It("clears widget when info is nil", func() {
		c.SetInfo(info)
		Expect(c.View()).NotTo(BeEmpty())
		c.SetInfo(nil)
		Expect(c.View()).To(BeEmpty())
	})

	It("advances the spinner frame on SpinnerTickMsg", func() {
		c.SetInfo(info)
		c.Update(chat.SpinnerTickMsg{})
		rendered := c.View()
		Expect(rendered).To(ContainSubstring("⠙"))
	})

	It("does not advance frame without a widget", func() {
		c.Update(chat.SpinnerTickMsg{})
		Expect(c.View()).To(BeEmpty())
	})
})
