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
				// The collapsed BlockTool renders the title line
				// "<icon> <name>: <input>"; the result content is NOT
				// part of the collapsed form (default), so we anchor
				// the post-text marker on "bash" (visible as part of
				// the title) rather than the content "ls output".
				view.AddMessage(chat.Message{Role: "tool_result", Content: "ls output", ToolName: "bash", ToolInput: "ls"})

				content := view.RenderContent(80)

				textPos := strings.Index(content, "text before tool call")
				toolBlockPos := strings.Index(content, "bash")

				Expect(textPos).To(BeNumerically(">=", 0), "response text should appear")
				Expect(toolBlockPos).To(BeNumerically(">=", 0), "tool_result title should appear in place of tool_call")
				Expect(textPos).To(BeNumerically("<", toolBlockPos), "response text before tool block")
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

// Regression: long single-stream responses (planner reviewer summaries
// that arrive as 3000+ chunks over several minutes) appeared truncated
// on screen even though the engine committed the full content to the
// session JSON. The renderPartialResponseThrottled cache lags behind
// v.response by up to 100ms; the question this spec answers is whether
// finaliseChunk + RenderContent reliably emit the *complete* committed
// content at end-of-turn regardless of throttle state.
//
// If this test passes, the truncation symptom is in the viewport
// SetContent path (Bubble Tea) or somewhere downstream of view.go;
// if it fails, RenderContent is the culprit.
var _ = Describe("View burst-stream finalisation", func() {
	It("renders the full committed content after a long burst-stream Done", func() {
		v := chat.NewView()
		v.SetDimensions(120, 40)
		// Inject an identity render function so the test does not depend
		// on glamour's exact output formatting; only the presence of the
		// content matters for the truncation regression.
		v.SetMarkdownRenderer(func(s string, _ int) string { return s })

		// Build ~12KB of recognisable content split into 200 chunks —
		// large enough to push past the throttle's 100ms cool-down many
		// times during the burst, mirroring the real-world stalled-
		// session profile (3000+ chunks in 5min, 11823-char committed).
		const chunks = 200
		var fullExpected strings.Builder
		for n := 0; n < chunks; n++ {
			chunk := chunkBody(n)
			fullExpected.WriteString(chunk)
			v.HandleChunk(chunk, false, "", "", "")
		}
		// Final Done with no content (matches the real-world shape:
		// the last few content chunks arrive on Done=false followed by
		// an empty Done=true sentinel).
		v.HandleChunk("", true, "", "", "")

		rendered := v.RenderContent(120)

		// Sentinel checks at the start, middle, and end of the burst —
		// if the throttle drops the final committed render or
		// renderMessage truncates, at least one of these will fail.
		Expect(rendered).To(ContainSubstring(chunkBody(0)),
			"first chunk's marker missing — start of the response is truncated")
		Expect(rendered).To(ContainSubstring(chunkBody(chunks/2)),
			"mid-burst chunk's marker missing — middle of the response is truncated")
		Expect(rendered).To(ContainSubstring(chunkBody(chunks-1)),
			"last content chunk's marker missing — tail of the response is truncated")
		Expect(rendered).To(ContainSubstring(fullExpected.String()),
			"the rendered output must contain the complete concatenation of all chunks")
	})

	It("renders correctly when Done arrives WITH a final content payload", func() {
		v := chat.NewView()
		v.SetDimensions(120, 40)
		v.SetMarkdownRenderer(func(s string, _ int) string { return s })

		// Real provider streams sometimes pack the last content into
		// the Done chunk itself rather than emitting a separate
		// Done-with-empty-Content sentinel. Both shapes must surface
		// the full content end-to-end.
		v.HandleChunk("first part. ", false, "", "", "")
		v.HandleChunk("second part. ", false, "", "", "")
		v.HandleChunk("third part. ", true, "", "", "") // Done with content

		rendered := v.RenderContent(120)

		Expect(rendered).To(ContainSubstring("first part."))
		Expect(rendered).To(ContainSubstring("second part."))
		Expect(rendered).To(ContainSubstring("third part."),
			"Done-with-content payload must appear in the final render")
	})
})

// chunkBody returns a deterministic, recognisable chunk body for the
// burst test. Including the chunk index in the body lets the assertions
// pinpoint exactly which chunks (start/middle/end) survived the throttle.
func chunkBody(n int) string {
	return "[chunk-" + paddedIdx(n) + "-payload-with-enough-length-to-matter] "
}

func paddedIdx(n int) string {
	s := ""
	switch {
	case n < 10:
		s = "00"
	case n < 100:
		s = "0"
	}
	switch {
	case n < 10:
		s += string(rune('0' + n))
	case n < 100:
		s += string(rune('0'+n/10)) + string(rune('0'+n%10))
	default:
		s += string(rune('0'+n/100)) + string(rune('0'+(n/10)%10)) + string(rune('0'+n%10))
	}
	return s
}
