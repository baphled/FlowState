package chat_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/baphled/flowstate/internal/tui/views/chat"
)

var _ = Describe("ChatView thinking content rendering", Label("integration"), func() {
	var (
		view *chat.View
		th   theme.Theme
	)

	BeforeEach(func() {
		view = chat.NewView()
		view.SetMarkdownRenderer(func(c string, _ int) string { return c })
		th = theme.Default()
		view.SetTheme(th)
	})

	Describe("thinking content with frame animation", func() {
		Context("when a thinking message is added to the chat", func() {
			It("renders the thinking emoji prefix in the output", func() {
				view.AddMessage(chat.Message{Role: "thinking", Content: "Evaluating next step..."})
				content := view.RenderContent(80)
				Expect(content).To(ContainSubstring("💭"))
			})

			It("renders the thinking content text", func() {
				view.AddMessage(chat.Message{Role: "thinking", Content: "Considering trade-offs"})
				content := view.RenderContent(80)
				Expect(content).To(ContainSubstring("Considering trade-offs"))
			})

			It("renders thinking content without You or Assistant labels", func() {
				view.AddMessage(chat.Message{Role: "thinking", Content: "Internal reasoning step"})
				content := view.RenderContent(80)
				Expect(content).NotTo(ContainSubstring("You\n"))
				Expect(content).NotTo(ContainSubstring("Assistant\n"))
			})
		})

		Context("when DelegationStatusWidget has thinking text at different frames", func() {
			It("renders thinking text at frame 0", func() {
				info := &provider.DelegationInfo{
					TargetAgent:  "planner",
					Status:       "running",
					ModelName:    "claude-opus-4-5",
					ProviderName: "anthropic",
				}
				widget := chat.NewDelegationStatusWidgetWithThinking(info, th, "Analysing the problem")
				widget.SetFrame(0)
				rendered := widget.Render()
				Expect(rendered).To(ContainSubstring("Analysing the problem"))
				Expect(rendered).To(ContainSubstring("⠋"))
			})

			It("renders thinking text at frame 3", func() {
				info := &provider.DelegationInfo{
					TargetAgent:  "planner",
					Status:       "running",
					ModelName:    "claude-opus-4-5",
					ProviderName: "anthropic",
				}
				widget := chat.NewDelegationStatusWidgetWithThinking(info, th, "Considering options")
				widget.SetFrame(3)
				rendered := widget.Render()
				Expect(rendered).To(ContainSubstring("Considering options"))
				Expect(rendered).To(ContainSubstring("⠸"))
			})

			It("cycles spinner frames across animation ticks", func() {
				info := &provider.DelegationInfo{
					TargetAgent:  "executor",
					Status:       "running",
					ModelName:    "claude-opus-4-5",
					ProviderName: "anthropic",
				}
				widget := chat.NewDelegationStatusWidgetWithThinking(info, th, "Working...")
				widget.SetFrame(1)
				firstFrame := widget.Render()
				widget.SetFrame(2)
				secondFrame := widget.Render()
				Expect(firstFrame).NotTo(Equal(secondFrame))
			})
		})
	})

	Describe("assistant message footer renders model ID before delegation block", func() {
		Context("when an assistant message with model ID precedes a delegation block", func() {
			It("renders model ID in the assistant footer", func() {
				view.SetModelID("claude-sonnet-4-20250514")
				view.AddMessage(chat.Message{
					Role:    "assistant",
					Content: "I will delegate this task now.",
					ModelID: "claude-sonnet-4-20250514",
				})
				content := view.RenderContent(80)
				Expect(content).To(ContainSubstring("▣"))
				Expect(content).To(ContainSubstring("claude-sonnet-4-20250514"))
			})

			It("places the model ID footer before any delegation block content", func() {
				view.SetModelID("gpt-4o")
				view.AddMessage(chat.Message{
					Role:    "assistant",
					Content: "Delegating to sub-agent.",
					ModelID: "gpt-4o",
				})
				view.AddMessage(chat.Message{
					Role:    "system",
					Content: "delegation block content here",
				})
				content := view.RenderContent(80)
				modelIDPos := strings.Index(content, "gpt-4o")
				delegationPos := strings.Index(content, "delegation block content here")
				Expect(modelIDPos).To(BeNumerically(">=", 0), "model ID should appear in content")
				Expect(delegationPos).To(BeNumerically(">=", 0), "delegation block should appear in content")
				Expect(modelIDPos).To(BeNumerically("<", delegationPos), "model ID footer should appear before delegation block")
			})

			It("renders assistant content before the model footer", func() {
				view.AddMessage(chat.Message{
					Role:    "assistant",
					Content: "Here is my response text.",
					ModelID: "claude-sonnet-4-20250514",
				})
				content := view.RenderContent(80)
				responsePos := strings.Index(content, "Here is my response text.")
				footerPos := strings.Index(content, "▣")
				Expect(responsePos).To(BeNumerically(">=", 0), "response text should appear")
				Expect(footerPos).To(BeNumerically(">=", 0), "footer indicator should appear")
				Expect(responsePos).To(BeNumerically("<", footerPos), "response text should appear before footer")
			})
		})

		Context("when no model ID is set", func() {
			It("does not render the ▣ footer indicator", func() {
				view.AddMessage(chat.Message{
					Role:    "assistant",
					Content: "A message without model ID.",
				})
				content := view.RenderContent(80)
				Expect(content).NotTo(ContainSubstring("▣"))
			})
		})
	})
})
