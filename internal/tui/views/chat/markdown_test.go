package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/views/chat"
)

var _ = Describe("MarkdownRendering", func() {
	Describe("assistant code block", func() {
		It("contains ANSI escapes", func() {
			v := chat.NewView()
			v.AddMessage(chat.Message{
				Role:    "assistant",
				Content: "```go\nfmt.Println(\"hello\")\n```",
			})
			out := v.RenderContent(80)
			Expect(out).To(ContainSubstring("\x1b["))
		})
	})

	Describe("user message", func() {
		It("stays plain without ANSI", func() {
			v := chat.NewView()
			v.AddMessage(chat.Message{Role: "user", Content: "hello"})
			out := v.RenderContent(80)
			Expect(out).NotTo(ContainSubstring("\x1b["))
			Expect(out).To(ContainSubstring("hello"))
		})
	})

	Describe("fallback rendering", func() {
		It("preserves raw text when renderer fails", func() {
			v := chat.NewView()
			v.SetMarkdownRenderer(func(content string, _ int) string {
				return content
			})
			v.AddMessage(chat.Message{
				Role:    "assistant",
				Content: "raw fallback text",
			})
			out := v.RenderContent(80)
			Expect(out).To(ContainSubstring("raw fallback text"))
			Expect(out).NotTo(ContainSubstring("\x1b["))
		})
	})
})

var _ = Describe("StreamingContentRendering", func() {
	Describe("streaming content", func() {
		It("contains ANSI escapes from glamour", func() {
			v := chat.NewView()
			v.HandleChunk("## Hello\n\nSome content", false, "", "", "")
			out := v.RenderContent(80)
			Expect(out).To(ContainSubstring("\x1b["))
		})

		It("contains Assistant label", func() {
			v := chat.NewView()
			v.HandleChunk("## Hello\n\nSome content", false, "", "", "")
			out := v.RenderContent(80)
			Expect(out).To(ContainSubstring("Assistant"))
		})

		It("preserves tool call display", func() {
			v := chat.NewView()
			v.HandleChunk("thinking", false, "", "web_search", "running")
			out := v.RenderContent(80)
			Expect(out).To(ContainSubstring("🌐"))
			Expect(out).To(ContainSubstring("Fetching…"))
		})
	})
})
