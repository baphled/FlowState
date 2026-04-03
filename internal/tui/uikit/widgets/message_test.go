package widgets_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("MessageWidget", func() {
	var th theme.Theme

	BeforeEach(func() {
		th = theme.Default()
	})

	Describe("NewMessageWidget", func() {
		It("creates a widget with the given role and content", func() {
			w := widgets.NewMessageWidget("user", "hello", th)
			Expect(w).NotTo(BeNil())
		})
	})

	Describe("Render", func() {
		Context("user messages", func() {
			It("includes the You label", func() {
				w := widgets.NewMessageWidget("user", "hello world", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("You"))
			})

			It("includes the message content", func() {
				w := widgets.NewMessageWidget("user", "test message", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("test message"))
			})
		})

		Context("assistant messages", func() {
			It("includes the Assistant label", func() {
				w := widgets.NewMessageWidget("assistant", "hi there", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("Assistant"))
			})

			It("includes the message content", func() {
				w := widgets.NewMessageWidget("assistant", "response text", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("response text"))
			})
		})

		Context("system messages", func() {
			It("renders as dimmed annotation for slash command output", func() {
				w := widgets.NewMessageWidget("system", "Available commands:\n  /help", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("Available commands"))
				Expect(output).NotTo(ContainSubstring("You"))
				Expect(output).NotTo(ContainSubstring("Assistant"))
			})
		})

		Context("tool_call messages", func() {
			It("includes the tool-specific icon prefix", func() {
				w := widgets.NewMessageWidget("tool_call", "bash", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("$"))
			})

			It("includes the tool name as content", func() {
				w := widgets.NewMessageWidget("tool_call", "read_file", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("read_file"))
			})

			It("does not include You or Assistant labels", func() {
				w := widgets.NewMessageWidget("tool_call", "bash", th)
				output := w.Render(80)
				Expect(output).NotTo(ContainSubstring("You"))
				Expect(output).NotTo(ContainSubstring("Assistant"))
			})
		})

		Context("tool_result messages", func() {
			It("includes the package emoji prefix", func() {
				w := widgets.NewMessageWidget("tool_result", "output data", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("📤"))
			})

			It("includes the full content without truncation", func() {
				w := widgets.NewMessageWidget("tool_result", "this is the complete output", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("this is the complete output"))
			})

			It("renders in muted grey color", func() {
				w := widgets.NewMessageWidget("tool_result", "output", th)
				output := w.Render(80)
				Expect(output).NotTo(BeEmpty())
			})

			It("does not include You or Assistant labels", func() {
				w := widgets.NewMessageWidget("tool_result", "output", th)
				output := w.Render(80)
				Expect(output).NotTo(ContainSubstring("You"))
				Expect(output).NotTo(ContainSubstring("Assistant"))
			})
		})

		Context("skill_load messages", func() {
			It("includes the books emoji prefix", func() {
				w := widgets.NewMessageWidget("skill_load", "loading skill", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("📚"))
			})

			It("includes the full content without truncation", func() {
				w := widgets.NewMessageWidget("skill_load", "skill_name loaded successfully", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("skill_name loaded successfully"))
			})

			It("does not include You or Assistant labels", func() {
				w := widgets.NewMessageWidget("skill_load", "skill loaded", th)
				output := w.Render(80)
				Expect(output).NotTo(ContainSubstring("You"))
				Expect(output).NotTo(ContainSubstring("Assistant"))
			})
		})

		Context("todo_update messages", func() {
			It("includes the content in the output", func() {
				w := widgets.NewMessageWidget("todo_update", "- [ ] Write tests\n- [x] Review PR", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("Write tests"))
			})

			It("does not include You or Assistant labels", func() {
				w := widgets.NewMessageWidget("todo_update", "- [ ] Task one", th)
				output := w.Render(80)
				Expect(output).NotTo(ContainSubstring("You"))
				Expect(output).NotTo(ContainSubstring("Assistant"))
			})
		})

		Context("tool_error messages", func() {
			It("includes the cross mark emoji prefix", func() {
				w := widgets.NewMessageWidget("tool_error", "error: something failed", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("❌"))
			})

			It("includes the full error content without truncation", func() {
				w := widgets.NewMessageWidget("tool_error", "error: connection timeout", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("error: connection timeout"))
			})

			It("renders in red color", func() {
				w := widgets.NewMessageWidget("tool_error", "error: failed", th)
				output := w.Render(80)
				Expect(output).NotTo(BeEmpty())
			})

			It("does not include You or Assistant labels", func() {
				w := widgets.NewMessageWidget("tool_error", "error: failed", th)
				output := w.Render(80)
				Expect(output).NotTo(ContainSubstring("You"))
				Expect(output).NotTo(ContainSubstring("Assistant"))
			})
		})

		Context("with nil theme", func() {
			It("still renders without panic", func() {
				w := widgets.NewMessageWidget("user", "no theme", nil)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("no theme"))
			})
		})

		Context("with custom markdown renderer", func() {
			It("uses the renderer for assistant messages", func() {
				w := widgets.NewMessageWidget("assistant", "markdown content", th)
				w.SetMarkdownRenderer(func(content string, _ int) string {
					return "[rendered]" + content
				})
				output := w.Render(80)
				Expect(output).To(ContainSubstring("[rendered]markdown content"))
			})

			It("does not use the renderer for user messages", func() {
				w := widgets.NewMessageWidget("user", "plain text", th)
				w.SetMarkdownRenderer(func(_ string, _ int) string {
					return "[should-not-appear]"
				})
				output := w.Render(80)
				Expect(output).NotTo(ContainSubstring("[should-not-appear]"))
				Expect(output).To(ContainSubstring("plain text"))
			})
		})

		Context("tool_result with ToolName set", func() {
			It("uses BlockTool for tool_result when ToolName is set", func() {
				w := widgets.NewMessageWidget("tool_result", "output data", th)
				w.SetToolName("bash")
				output := w.Render(80)
				Expect(output).To(ContainSubstring("bash"))
				Expect(output).NotTo(ContainSubstring("📤"))
			})

			It("falls back to emoji prefix when ToolName is empty", func() {
				w := widgets.NewMessageWidget("tool_result", "output data", th)
				output := w.Render(80)
				Expect(output).To(ContainSubstring("📤"))
			})
		})

		Context("tool_result with ToolName and ToolInput set", func() {
			It("renders tool_result with correct icon and input via BlockTool", func() {
				w := widgets.NewMessageWidget("tool_result", "output text", th)
				w.SetToolName("bash")
				w.SetToolInput("ls -la")
				result := w.Render(80)
				Expect(result).To(ContainSubstring("$"))
				Expect(result).To(ContainSubstring("ls -la"))
			})
		})

		Context("tool_call with tool-specific icon", func() {
			It("renders tool_call with tool-specific icon not wrench emoji", func() {
				w := widgets.NewMessageWidget("tool_call", "bash: ls -la", th)
				result := w.Render(80)
				Expect(result).To(ContainSubstring("$"))
				Expect(result).NotTo(ContainSubstring("🔧"))
				Expect(result).To(ContainSubstring("bash: ls -la"))
			})

			It("renders tool_call with read icon for read tool", func() {
				w := widgets.NewMessageWidget("tool_call", "read: /path/to/file", th)
				result := w.Render(80)
				Expect(result).To(ContainSubstring("→"))
				Expect(result).NotTo(ContainSubstring("🔧"))
			})

			It("renders tool_call with default icon for unknown tool", func() {
				w := widgets.NewMessageWidget("tool_call", "unknown_tool", th)
				result := w.Render(80)
				Expect(result).NotTo(ContainSubstring("🔧"))
				Expect(result).To(ContainSubstring("unknown_tool"))
			})
		})

		Context("assistant messages with AgentColor", func() {
			It("uses theme colour for assistant label when AgentColor is zero", func() {
				w := widgets.NewMessageWidget("assistant", "hi", th)
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := w.Render(80)
				Expect(output).To(ContainSubstring("Assistant"))
			})

			It("uses AgentColor for assistant label when set", func() {
				w := widgets.NewMessageWidget("assistant", "hi", th)
				w.SetAgentColor(lipgloss.Color("#ff0000"))
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := w.Render(80)
				Expect(output).To(ContainSubstring("Assistant"))
			})
		})

		Context("assistant messages with ModelID", func() {
			It("renders model ID footer on assistant message when ModelID is set", func() {
				w := widgets.NewMessageWidget("assistant", "response", th)
				w.SetModelID("claude-sonnet-4-20250514")
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := w.Render(80)
				Expect(output).To(ContainSubstring("▣"))
				Expect(output).To(ContainSubstring("claude-sonnet-4-20250514"))
			})

			It("does not render footer when ModelID is empty", func() {
				w := widgets.NewMessageWidget("assistant", "response", th)
				w.SetMarkdownRenderer(func(c string, _ int) string { return c })
				output := w.Render(80)
				Expect(output).NotTo(ContainSubstring("▣"))
			})
		})

		Context("width constraint", func() {
			It("does not exceed width with PaddingLeft after markdown wrapping", func() {
				longContent := "This is a very long paragraph that should be wrapped by the markdown renderer at a specific width. " +
					"We need content that will produce lines close to the requested width when wrapped. " +
					"The renderer should wrap at width-2, then PaddingLeft(2) adds 2 chars, keeping total within width."

				w := widgets.NewMessageWidget("assistant", longContent, th)
				w.SetMarkdownRenderer(func(content string, width int) string {
					lines := strings.Split(content, "\n")
					var wrapped []string
					for _, line := range lines {
						if len(line) > width {
							for len(line) > width {
								wrapped = append(wrapped, line[:width])
								line = line[width:]
							}
							if line != "" {
								wrapped = append(wrapped, line)
							}
						} else {
							wrapped = append(wrapped, line)
						}
					}
					return strings.Join(wrapped, "\n")
				})

				output := w.Render(80)
				stripped := stripANSI(output)
				lines := strings.Split(stripped, "\n")

				for i, line := range lines {
					Expect(len(line)).To(BeNumerically("<=", 80),
						"line %d exceeds 80 chars: length=%d content=%q", i+1, len(line), line)
				}
			})
		})
	})
})

// stripANSI removes ANSI escape codes from a string.
func stripANSI(s string) string {
	var result strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}
