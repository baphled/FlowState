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
			messages := []string{"Hello", "World"}
			content := view.RenderContent(messages, "", "normal", false, "")
			Expect(content).To(ContainSubstring("Hello"))
			Expect(content).To(ContainSubstring("World"))
		})

		It("renders input prompt", func() {
			content := view.RenderContent(nil, "", "normal", false, "")
			Expect(content).To(ContainSubstring("> "))
		})

		It("renders current input", func() {
			content := view.RenderContent(nil, "test input", "normal", false, "")
			Expect(content).To(ContainSubstring("test input"))
		})

		It("shows normal mode indicator", func() {
			content := view.RenderContent(nil, "", "normal", false, "")
			Expect(content).To(ContainSubstring("[NORMAL]"))
		})

		It("shows insert mode indicator", func() {
			content := view.RenderContent(nil, "", "insert", false, "")
			Expect(content).To(ContainSubstring("[INSERT]"))
		})

		It("shows streaming response", func() {
			content := view.RenderContent(nil, "", "normal", true, "streaming...")
			Expect(content).To(ContainSubstring("streaming..."))
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
