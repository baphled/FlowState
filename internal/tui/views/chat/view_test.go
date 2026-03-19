package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/views/chat"
)

var _ = Describe("ChatView", func() {
	var view *chat.View

	BeforeEach(func() {
		view = chat.NewView()
	})

	Describe("NewView", func() {
		It("creates a view with default dimensions", func() {
			Expect(view.Width()).To(Equal(80))
			Expect(view.Height()).To(Equal(24))
		})
	})

	Describe("SetDimensions", func() {
		It("updates width and height", func() {
			view.SetDimensions(120, 40)
			Expect(view.Width()).To(Equal(120))
			Expect(view.Height()).To(Equal(40))
		})
	})

	Describe("RenderContent", func() {
		It("renders messages", func() {
			view.AddMessage(chat.Message{Role: "user", Content: "Hello"})
			view.AddMessage(chat.Message{Role: "user", Content: "World"})
			content := view.RenderContent(80)
			Expect(content).To(ContainSubstring("Hello"))
			Expect(content).To(ContainSubstring("World"))
		})

		Context("styled message labels", func() {
			It("shows You label for user messages", func() {
				view.AddMessage(chat.Message{Role: "user", Content: "hi"})
				content := view.RenderContent(80)
				Expect(content).To(ContainSubstring("You"))
			})

			It("shows Assistant label for assistant messages", func() {
				view.SetMarkdownRenderer(func(c string, _ int) string { return c })
				view.AddMessage(chat.Message{Role: "assistant", Content: "reply"})
				content := view.RenderContent(80)
				Expect(content).To(ContainSubstring("Assistant"))
			})
		})

		It("does not render spinner in content area when streaming", func() {
			view.SetStreaming(true, "")
			content := view.RenderContent(80)
			Expect(content).NotTo(ContainSubstring("Thinking"))
		})

		Describe("Markdown", func() {
			Context("with an assistant message containing a code block", func() {
				It("renders with ANSI escape sequences", func() {
					view.AddMessage(chat.Message{
						Role:    "assistant",
						Content: "```go\nfmt.Println(\"hello\")\n```",
					})
					output := view.RenderContent(80)
					Expect(output).To(ContainSubstring("\x1b["))
				})
			})

			Context("with a user message", func() {
				It("keeps plain text without ANSI", func() {
					view.AddMessage(chat.Message{Role: "user", Content: "hello"})
					output := view.RenderContent(80)
					Expect(output).NotTo(ContainSubstring("\x1b["))
					Expect(output).To(ContainSubstring("hello"))
				})
			})

			Context("when glamour rendering fails", func() {
				It("falls back to raw text", func() {
					view.SetMarkdownRenderer(func(content string, _ int) string {
						return content
					})
					view.AddMessage(chat.Message{
						Role:    "assistant",
						Content: "raw fallback text",
					})
					output := view.RenderContent(80)
					Expect(output).To(ContainSubstring("raw fallback text"))
					Expect(output).NotTo(ContainSubstring("\x1b["))
				})
			})
		})
	})

	Describe("ResultSend", func() {
		It("contains the message", func() {
			result := chat.ResultSend{Message: "hello"}
			Expect(result.Message).To(Equal("hello"))
		})
	})

	Describe("ResultCancel", func() {
		It("exists as a signal type", func() {
			result := chat.ResultCancel{}
			Expect(result).To(BeAssignableToTypeOf(chat.ResultCancel{}))
		})
	})
})
