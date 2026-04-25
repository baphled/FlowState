package chat_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/charmbracelet/lipgloss"

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

	Describe("Tool call rendering", func() {
		It("renders tool call widget when streaming with active tool call", func() {
			view.SetStreaming(true, "processing...")
			view.SetToolCall("web_search", "running")
			content := view.RenderContent(80)
			Expect(content).To(ContainSubstring("🌐"))
			Expect(content).To(ContainSubstring("Fetching…"))
		})

		It("renders assistant text before tool call indicator during streaming", func() {
			view.SetMarkdownRenderer(func(c string, _ int) string { return c })
			view.SetStreaming(true, "")
			view.HandleChunk("Hello from assistant", false, "", "bash", "running")
			content := view.RenderContent(80)

			textPos := strings.Index(content, "Hello from assistant")
			toolCallPos := strings.Index(content, "Writing command…")

			Expect(textPos).To(BeNumerically(">=", 0), "assistant text should appear in content")
			Expect(toolCallPos).To(BeNumerically(">=", 0), "tool call indicator should appear in content")
			Expect(textPos).To(BeNumerically("<", toolCallPos), "assistant text should appear before tool call indicator")
		})

		It("does not render tool call when none is set", func() {
			view.SetStreaming(true, "processing...")
			content := view.RenderContent(80)
			Expect(content).NotTo(ContainSubstring("⚡"))
		})

		It("renders tool call with complete status", func() {
			view.SetStreaming(true, "")
			view.SetToolCall("file_read", "complete")
			content := view.RenderContent(80)
			Expect(content).To(ContainSubstring("⚡"))
			Expect(content).To(ContainSubstring("file_read"))
			Expect(content).To(ContainSubstring("complete"))
		})
	})

	Describe("Message ordering", func() {
		BeforeEach(func() {
			view.SetMarkdownRenderer(func(c string, _ int) string { return c })
		})

		Describe("FlushPartialResponse", func() {
			It("commits accumulated response text as an assistant message", func() {
				view.SetStreaming(true, "partial text")
				view.FlushPartialResponse()

				msgs := view.Messages()
				Expect(msgs).To(HaveLen(1))
				Expect(msgs[0].Role).To(Equal("assistant"))
				Expect(msgs[0].Content).To(Equal("partial text"))
			})

			It("is a no-op when response is empty", func() {
				view.SetStreaming(true, "")
				view.FlushPartialResponse()

				Expect(view.Messages()).To(BeEmpty())
			})

			It("clears the partial response after flushing", func() {
				view.SetStreaming(true, "text to flush")
				view.FlushPartialResponse()

				Expect(view.Response()).To(BeEmpty())
			})

			It("preserves streaming state after flush", func() {
				view.SetStreaming(true, "in flight")
				view.FlushPartialResponse()

				Expect(view.IsStreaming()).To(BeTrue())
			})
		})

		Describe("committed tool_call appears after response text in rendered output", func() {
			It("places flushed response before tool_call message in messages slice", func() {
				view.SetStreaming(true, "text before tool call")
				view.FlushPartialResponse()
				view.AddMessage(chat.Message{Role: "tool_call", Content: "bash"})

				msgs := view.Messages()
				Expect(msgs).To(HaveLen(2))
				Expect(msgs[0].Role).To(Equal("assistant"))
				Expect(msgs[1].Role).To(Equal("tool_call"))
			})

			It("renders committed response text before tool_call data in output (tool_call itself is suppressed; ordering pinned via tool_result)", func() {
				view.SetStreaming(true, "text before tool call")
				view.FlushPartialResponse()
				view.AddMessage(chat.Message{Role: "tool_call", Content: "bash"})
				// Add a paired tool_result so we have something visible
				// after the response text — tool_call rendering is
				// suppressed because tool_result carries the rich block.
				view.AddMessage(chat.Message{Role: "tool_result", Content: "ls output", ToolName: "bash", ToolInput: "ls"})

				content := view.RenderContent(80)

				textPos := strings.Index(content, "text before tool call")
				toolResultPos := strings.Index(content, "ls output")

				Expect(textPos).To(BeNumerically(">=", 0), "response text should appear")
				Expect(toolResultPos).To(BeNumerically(">=", 0), "tool_result should appear in place of tool_call")
				Expect(textPos).To(BeNumerically("<", toolResultPos), "response text before tool block")
			})
		})

		Describe("delegation completion message ordering", func() {
			It("places flushed response before delegation completion in messages slice", func() {
				view.SetStreaming(true, "thinking about delegation")
				view.FlushPartialResponse()
				view.AddMessage(chat.Message{Role: "system", Content: "delegation done"})

				msgs := view.Messages()
				Expect(msgs).To(HaveLen(2))
				Expect(msgs[0].Role).To(Equal("assistant"))
				Expect(msgs[1].Role).To(Equal("system"))
			})
		})
	})
})

var _ = Describe("ChatView agent and model metadata", func() {
	var view *chat.View

	BeforeEach(func() {
		view = chat.NewView()
		view.SetMarkdownRenderer(func(c string, _ int) string { return c })
	})

	Describe("SetAgentColor and SetModelID propagation", func() {
		It("stamps AgentColor on messages created by FlushPartialResponse", func() {
			view.SetAgentColor("205")
			view.SetModelID("claude-sonnet-4-20250514")
			view.SetStreaming(true, "hello from agent")
			view.FlushPartialResponse()

			msgs := view.Messages()
			Expect(msgs).To(HaveLen(1))
			Expect(msgs[0].AgentColor).To(Equal(lipgloss.Color("205")))
			// FlushPartialResponse intentionally OMITS ModelID: it
			// commits a mid-turn partial (before a tool call). The
			// model + duration footer must render ONLY on the final
			// assistant message of a turn — finaliseChunk (Done path)
			// is the sole site that stamps ModelID on the appended
			// message. Setting it here would render the footer
			// between the streamed text and the inline tool widget
			// (the "Reading… below the footer" symptom).
			Expect(msgs[0].ModelID).To(BeEmpty())
		})

		It("renders model footer in content when ModelID is set", func() {
			view.SetModelID("gpt-4o")
			view.AddMessage(chat.Message{
				Role:    "assistant",
				Content: "hello",
				ModelID: "gpt-4o",
			})
			content := view.RenderContent(80)
			Expect(content).To(ContainSubstring("▣"))
			Expect(content).To(ContainSubstring("gpt-4o"))
		})

		It("renders BlockTool for tool_result with ToolName", func() {
			view.AddMessage(chat.Message{
				Role:     "tool_result",
				Content:  "file contents",
				ToolName: "read",
			})
			content := view.RenderContent(80)
			Expect(content).To(ContainSubstring("read"))
			Expect(content).NotTo(ContainSubstring("📤"))
		})
	})
})
