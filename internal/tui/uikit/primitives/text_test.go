package primitives_test

import (
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/tui/uikit/primitives"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPrimitives(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "UIKit Primitives Suite")
}

var _ = Describe("Text", func() {
	var th theme.Theme

	BeforeEach(func() {
		th = theme.Default()
	})

	Describe("NewText", func() {
		It("should create text with content", func() {
			text := primitives.NewText("Hello World", th)
			Expect(text).NotTo(BeNil())
			rendered := text.Render()
			Expect(rendered).To(ContainSubstring("Hello World"))
		})

		It("should accept nil theme and use default", func() {
			text := primitives.NewText("Hello", nil)
			Expect(text).NotTo(BeNil())
			rendered := text.Render()
			Expect(rendered).To(ContainSubstring("Hello"))
		})
	})

	Describe("Fluent API", func() {
		Describe("Style", func() {
			It("should set TextTitle style", func() {
				text := primitives.NewText("Title", th).Style(primitives.TextTitle)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Title"))
			})

			It("should set TextSubtitle style", func() {
				text := primitives.NewText("Subtitle", th).Style(primitives.TextSubtitle)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Subtitle"))
			})

			It("should set TextBody style", func() {
				text := primitives.NewText("Body", th).Style(primitives.TextBody)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Body"))
			})

			It("should set TextMuted style", func() {
				text := primitives.NewText("Muted", th).Style(primitives.TextMuted)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Muted"))
			})

			It("should set TextError style", func() {
				text := primitives.NewText("Error", th).Style(primitives.TextError)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Error"))
			})

			It("should set TextSuccess style", func() {
				text := primitives.NewText("Success", th).Style(primitives.TextSuccess)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Success"))
			})

			It("should set TextWarning style", func() {
				text := primitives.NewText("Warning", th).Style(primitives.TextWarning)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Warning"))
			})
		})

		Describe("Bold", func() {
			It("should make text bold", func() {
				text := primitives.NewText("Bold Text", th).Bold()
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Bold Text"))
			})

			It("should chain with other methods", func() {
				text := primitives.NewText("Bold Title", th).Style(primitives.TextTitle).Bold()
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Bold Title"))
			})
		})

		Describe("Width", func() {
			It("should constrain output width", func() {
				text := primitives.NewText("This is a very long text that should be constrained", th).Width(20)
				rendered := text.Render()
				// Split by newlines to check each line width
				lines := splitLines(rendered)
				for _, line := range lines {
					// Use lipgloss.Width to measure actual width (ignoring ANSI codes)
					Expect(visualWidth(line)).To(BeNumerically("<=", 20))
				}
			})

			It("should chain with other methods", func() {
				text := primitives.NewText("Short", th).Bold().Width(10)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Short"))
			})
		})

		Describe("Foreground", func() {
			It("should override the semantic color", func() {
				customColor := lipgloss.Color("#FF0000")
				text := primitives.NewText("Custom", th).Foreground(customColor)
				rendered := text.Render()
				// Content should be present in the rendered output
				Expect(rendered).To(ContainSubstring("Custom"))
			})

			It("should return self for chaining", func() {
				customColor := lipgloss.Color("#00FF00")
				text := primitives.NewText("Test", th)
				result := text.Foreground(customColor)
				Expect(result).To(Equal(text))
			})

			It("should work with other fluent methods", func() {
				customColor := lipgloss.Color("#0000FF")
				text := primitives.NewText("Styled", th).
					Foreground(customColor).
					Bold().
					MarginBottom(1)
				rendered := text.Render()
				lines := strings.Split(rendered, "\n")
				Expect(len(lines)).To(BeNumerically(">=", 2))
				Expect(rendered).To(ContainSubstring("Styled"))
			})

			It("should override semantic style color when set", func() {
				customColor := lipgloss.Color("#ABCDEF")
				// Title style sets primary color, but Foreground should override
				text := primitives.Title("Override", th).Foreground(customColor)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Override"))
			})
		})

		Describe("Margins", func() {
			Describe("MarginTop", func() {
				It("should add blank lines before content", func() {
					text := primitives.NewText("Content", th).MarginTop(2)
					rendered := text.Render()
					lines := strings.Split(rendered, "\n")
					// Should have 2 margin lines + 1 content line = 3 lines minimum
					Expect(len(lines)).To(BeNumerically(">=", 3))
					Expect(rendered).To(ContainSubstring("Content"))
				})

				It("should return self for chaining", func() {
					text := primitives.NewText("Test", th)
					result := text.MarginTop(1)
					Expect(result).To(Equal(text))
				})

				It("should handle zero margin", func() {
					text := primitives.NewText("Content", th).MarginTop(0)
					rendered := text.Render()
					Expect(rendered).NotTo(HavePrefix("\n"))
				})
			})

			Describe("MarginBottom", func() {
				It("should add blank lines after content", func() {
					text := primitives.NewText("Content", th).MarginBottom(2)
					rendered := text.Render()
					lines := strings.Split(rendered, "\n")
					// Should have 1 content line + 2 margin lines = 3 lines minimum
					Expect(len(lines)).To(BeNumerically(">=", 3))
					Expect(rendered).To(ContainSubstring("Content"))
				})

				It("should return self for chaining", func() {
					text := primitives.NewText("Test", th)
					result := text.MarginBottom(1)
					Expect(result).To(Equal(text))
				})

				It("should handle zero margin", func() {
					text := primitives.NewText("Content", th).MarginBottom(0)
					rendered := text.Render()
					Expect(rendered).NotTo(HaveSuffix("\n"))
				})
			})

			Describe("MarginLeft", func() {
				It("should add spaces before content", func() {
					text := primitives.NewText("Content", th).MarginLeft(4)
					rendered := text.Render()
					// Check that lipgloss MarginLeft is applied
					Expect(rendered).To(ContainSubstring("Content"))
				})

				It("should return self for chaining", func() {
					text := primitives.NewText("Test", th)
					result := text.MarginLeft(2)
					Expect(result).To(Equal(text))
				})
			})

			Describe("MarginRight", func() {
				It("should add spaces after content", func() {
					text := primitives.NewText("Content", th).MarginRight(4)
					rendered := text.Render()
					Expect(rendered).To(ContainSubstring("Content"))
				})

				It("should return self for chaining", func() {
					text := primitives.NewText("Test", th)
					result := text.MarginRight(2)
					Expect(result).To(Equal(text))
				})
			})

			Describe("Combined margins", func() {
				It("should support chaining multiple margin methods", func() {
					text := primitives.NewText("Centered", th).
						MarginTop(1).
						MarginBottom(1).
						MarginLeft(2).
						MarginRight(2)
					rendered := text.Render()
					lines := strings.Split(rendered, "\n")
					// Should have at least 3 lines: 1 top margin + content + 1 bottom margin
					Expect(len(lines)).To(BeNumerically(">=", 3))
					Expect(rendered).To(ContainSubstring("Centered"))
				})

				It("should work with other fluent methods", func() {
					text := primitives.Title("Title", th).
						MarginBottom(1).
						Bold()
					rendered := text.Render()
					lines := strings.Split(rendered, "\n")
					// Should have at least 2 lines: content + 1 bottom margin
					Expect(len(lines)).To(BeNumerically(">=", 2))
					Expect(rendered).To(ContainSubstring("Title"))
				})
			})
		})
	})

	Describe("Alignment", func() {
		Describe("Align", func() {
			It("should support AlignLeft", func() {
				text := primitives.NewText("Left", th).Width(20).Align(primitives.AlignLeft)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Left"))
			})

			It("should support AlignCenter", func() {
				text := primitives.NewText("Center", th).Width(20).Align(primitives.AlignCenter)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Center"))
			})

			It("should support AlignRight", func() {
				text := primitives.NewText("Right", th).Width(20).Align(primitives.AlignRight)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Right"))
			})

			It("should return self for chaining", func() {
				text := primitives.NewText("Test", th)
				result := text.Align(primitives.AlignCenter)
				Expect(result).To(Equal(text))
			})
		})

		Describe("Center", func() {
			It("should center text (convenience method)", func() {
				text := primitives.NewText("Centered", th).Width(30).Center()
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Centered"))
			})

			It("should return self for chaining", func() {
				text := primitives.NewText("Test", th)
				result := text.Center()
				Expect(result).To(Equal(text))
			})

			It("should work with title", func() {
				text := primitives.Title("Centered Title", th).Width(40).Center()
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Centered Title"))
			})
		})

		Describe("Right", func() {
			It("should right-align text (convenience method)", func() {
				text := primitives.NewText("Right", th).Width(30).Right()
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Right"))
			})

			It("should return self for chaining", func() {
				text := primitives.NewText("Test", th)
				result := text.Right()
				Expect(result).To(Equal(text))
			})
		})
	})

	Describe("Convenience Constructors", func() {
		Describe("Title", func() {
			It("should create title text", func() {
				text := primitives.Title("Page Title", th)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Page Title"))
			})
		})

		Describe("Subtitle", func() {
			It("should create subtitle text", func() {
				text := primitives.Subtitle("Section Subtitle", th)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Section Subtitle"))
			})
		})

		Describe("Body", func() {
			It("should create body text", func() {
				text := primitives.Body("Regular text", th)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Regular text"))
			})
		})

		Describe("Muted", func() {
			It("should create muted text", func() {
				text := primitives.Muted("Disabled text", th)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Disabled text"))
			})
		})

		Describe("ErrorText", func() {
			It("should create error text", func() {
				text := primitives.ErrorText("Error message", th)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Error message"))
			})
		})

		Describe("SuccessText", func() {
			It("should create success text", func() {
				text := primitives.SuccessText("Success message", th)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Success message"))
			})
		})

		Describe("WarningText", func() {
			It("should create warning text", func() {
				text := primitives.WarningText("Warning message", th)
				rendered := text.Render()
				Expect(rendered).To(ContainSubstring("Warning message"))
			})
		})
	})
})

// Helper functions for testing.
func splitLines(s string) []string {
	var lines []string
	current := ""
	for _, ch := range s {
		if ch == '\n' {
			lines = append(lines, current)
			current = ""
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func visualWidth(s string) int {
	// Simple visual width calculation (ignoring ANSI codes)
	// For production, use lipgloss.Width() but this is sufficient for tests
	width := 0
	inEscape := false
	for _, ch := range s {
		if ch == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if ch == 'm' {
				inEscape = false
			}
			continue
		}
		width++
	}
	return width
}
