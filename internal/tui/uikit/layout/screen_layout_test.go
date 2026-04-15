package layout_test

import (
	"regexp"
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/ui/terminal"
	"github.com/baphled/flowstate/internal/ui/themes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// stripAnsi removes ANSI escape codes from a string for reliable test comparisons.
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripAnsi(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// MockLogo implements LogoRenderer for testing.
type MockLogo struct {
	width   int
	content string
}

func NewMockLogo(content string) *MockLogo {
	return &MockLogo{content: content}
}

func (m *MockLogo) ViewStatic() string {
	return m.content
}

func (m *MockLogo) SetWidth(width int) {
	m.width = width
}

var _ = Describe("ScreenLayout Pinned Layout", func() {
	var (
		termInfo *terminal.Info
		theme    themes.Theme
	)

	BeforeEach(func() {
		termInfo = &terminal.Info{
			Width:   80,
			Height:  24,
			IsValid: true,
		}
		theme = themes.NewDefaultTheme()
	})

	Describe("Logo at Top (Top Pinned)", func() {
		It("should render logo at line 0 when LogoSpacing is 0", func() {
			logo := NewMockLogo("LOGO")
			view := layout.NewScreenLayout(termInfo).
				WithLogo(logo, 0).
				WithTheme(theme).
				WithContent("Content").
				WithHelp("Help text")

			rendered := view.Render()
			lines := strings.Split(stripAnsi(rendered), "\n")

			Expect(strings.TrimSpace(lines[0])).To(ContainSubstring("LOGO"))
		})

		It("should respect LogoSpacing parameter for blank lines before logo", func() {
			logo := NewMockLogo("LOGO")
			view := layout.NewScreenLayout(termInfo).
				WithLogo(logo, 5).
				WithTheme(theme).
				WithContent("Content")

			rendered := view.Render()
			lines := strings.Split(stripAnsi(rendered), "\n")

			for i := range 5 {
				Expect(strings.TrimSpace(lines[i])).To(BeEmpty(), "line %d should be blank (spacing=5)", i)
			}
			Expect(strings.TrimSpace(lines[5])).To(ContainSubstring("LOGO"))
		})
	})

	Describe("Footer at Bottom (Bottom Pinned)", func() {
		It("should render footer on the last lines of the terminal", func() {
			view := layout.NewScreenLayout(termInfo).
				WithTheme(theme).
				WithContent("Content").
				WithHelp("Press q to quit")

			rendered := view.Render()
			lines := strings.Split(stripAnsi(rendered), "\n")

			// Terminal height is 24, so footer should be near line 23 (0-indexed)
			// Find the last non-empty line
			var lastNonEmptyLine string
			for i := len(lines) - 1; i >= 0; i-- {
				if strings.TrimSpace(lines[i]) != "" {
					lastNonEmptyLine = lines[i]
					break
				}
			}

			Expect(lastNonEmptyLine).To(ContainSubstring("Press q to quit"))
		})

		It("should render footer at bottom even with logo", func() {
			logo := NewMockLogo("LOGO\nLINE 2")
			view := layout.NewScreenLayout(termInfo).
				WithLogo(logo, 0).
				WithTheme(theme).
				WithContent("Content").
				WithHelp("Help text")

			rendered := view.Render()
			lines := strings.Split(stripAnsi(rendered), "\n")

			// Find last non-empty line
			var lastNonEmptyLine string
			for i := len(lines) - 1; i >= 0; i-- {
				if strings.TrimSpace(lines[i]) != "" {
					lastNonEmptyLine = lines[i]
					break
				}
			}

			Expect(lastNonEmptyLine).To(ContainSubstring("Help text"))
		})
	})

	Describe("Content Placement", func() {
		It("should render content immediately after header section", func() {
			logo := NewMockLogo("LOGO")
			view := layout.NewScreenLayout(termInfo).
				WithLogo(logo, 0).
				WithBreadcrumbs("Home", "Settings").
				WithTheme(theme).
				WithContent("Main Content").
				WithHelp("Help")

			rendered := view.Render()
			lines := strings.Split(stripAnsi(rendered), "\n")

			// Find logo, breadcrumbs, then content should follow
			logoFound := false
			breadcrumbsFound := false
			contentIndex := -1

			for i, line := range lines {
				stripped := strings.TrimSpace(line)
				if !logoFound && strings.Contains(stripped, "LOGO") {
					logoFound = true
				} else if logoFound && !breadcrumbsFound && strings.Contains(stripped, "Settings") {
					breadcrumbsFound = true
				} else if breadcrumbsFound && strings.Contains(stripped, "Main Content") {
					contentIndex = i
					break
				}
			}

			Expect(logoFound).To(BeTrue(), "Logo should be found")
			Expect(breadcrumbsFound).To(BeTrue(), "Breadcrumbs should be found")
			Expect(contentIndex).To(BeNumerically(">", 0), "Content should be found after header")
		})
	})

	Describe("Spacer Fills Gap", func() {
		It("should fill vertical space between content and footer", func() {
			view := layout.NewScreenLayout(termInfo).
				WithTheme(theme).
				WithContent("Content").
				WithHelp("Help")

			rendered := view.Render()
			lines := strings.Split(rendered, "\n")

			// Total lines should equal terminal height
			Expect(lines).To(HaveLen(termInfo.Height))
		})

		It("should calculate spacer correctly with logo and footer", func() {
			logo := NewMockLogo("LOGO\nLINE2\nLINE3") // 3 lines
			view := layout.NewScreenLayout(termInfo).
				WithLogo(logo, 0).
				WithTheme(theme).
				WithContent("Content"). // 1 line
				WithHelp("Help text")   // ~2 lines (separator + help)

			rendered := view.Render()
			lines := strings.Split(rendered, "\n")

			// Should have exactly termInfo.Height lines
			Expect(lines).To(HaveLen(termInfo.Height))
		})
	})

	Describe("Overflow Handling (Graceful Degradation)", func() {
		It("should handle content taller than terminal without negative spacer", func() {
			// Create content that exceeds terminal height
			tallContent := strings.Repeat("Line\n", 30) // 30 lines in a 24-line terminal

			view := layout.NewScreenLayout(termInfo).
				WithTheme(theme).
				WithContent(tallContent).
				WithHelp("Help")

			// Should not panic
			Expect(func() {
				_ = view.Render()
			}).NotTo(Panic())
		})

		It("should constrain overflowing content to available height via viewport", func() {
			tallContent := strings.Repeat("Line\n", 30) // 30 lines in a 24-line terminal

			view := layout.NewScreenLayout(termInfo).
				WithTheme(theme).
				WithContent(tallContent).
				WithHelp("Help")

			rendered := view.Render()
			lines := strings.Split(rendered, "\n")

			// Total output must still be exactly terminal height
			Expect(lines).To(HaveLen(termInfo.Height),
				"output should be exactly terminal height even with overflowing content")

			// Footer must still be at the bottom
			strippedLines := strings.Split(stripAnsi(rendered), "\n")
			var lastNonEmptyLine string
			for i := len(strippedLines) - 1; i >= 0; i-- {
				if strings.TrimSpace(strippedLines[i]) != "" {
					lastNonEmptyLine = strippedLines[i]
					break
				}
			}
			Expect(lastNonEmptyLine).To(ContainSubstring("Help"),
				"footer should remain pinned at bottom even when content overflows")
		})
	})

	Describe("No Logo Mode", func() {
		It("should start with breadcrumbs at line 0 when no logo", func() {
			view := layout.NewScreenLayout(termInfo).
				WithBreadcrumbs("Home", "Settings").
				WithTheme(theme).
				WithContent("Content").
				WithHelp("Help")

			rendered := view.Render()
			lines := strings.Split(stripAnsi(rendered), "\n")

			// First non-empty line should contain breadcrumbs
			var firstNonEmpty string
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					firstNonEmpty = line
					break
				}
			}

			Expect(firstNonEmpty).To(ContainSubstring("Settings"))
		})
	})

	Describe("No Footer Mode", func() {
		It("should render without footer when help text not provided", func() {
			view := layout.NewScreenLayout(termInfo).
				WithTheme(theme).
				WithContent("Content")
			// No WithHelp() call

			rendered := view.Render()

			// Should still render successfully
			Expect(rendered).NotTo(BeEmpty())
		})
	})

	Describe("Vertical Alignment", func() {
		It("should use Top alignment instead of Center", func() {
			logo := NewMockLogo("LOGO")
			view := layout.NewScreenLayout(termInfo).
				WithLogo(logo, 0).
				WithTheme(theme).
				WithContent("Content").
				WithHelp("Help")

			rendered := view.Render()
			lines := strings.Split(stripAnsi(rendered), "\n")

			// Logo should be in the top portion (first 5 lines)
			logoInTop := false
			for i := 0; i < 5 && i < len(lines); i++ {
				if strings.Contains(lines[i], "LOGO") {
					logoInTop = true
					break
				}
			}

			Expect(logoInTop).To(BeTrue(), "Logo should be in top portion of screen")

			// Help should be in the bottom portion (last 5 lines)
			helpInBottom := false
			startIdx := len(lines) - 5
			if startIdx < 0 {
				startIdx = 0
			}
			for i := startIdx; i < len(lines); i++ {
				if strings.Contains(lines[i], "Help") {
					helpInBottom = true
					break
				}
			}

			Expect(helpInBottom).To(BeTrue(), "Help should be in bottom portion of screen")
		})
	})

	Describe("Left Alignment", func() {
		It("should left-align content horizontally", func() {
			view := layout.NewScreenLayout(termInfo).
				WithTheme(theme).
				WithContent("X").
				WithHelp("Help")

			rendered := view.Render()
			lines := strings.Split(stripAnsi(rendered), "\n")

			for _, line := range lines {
				if strings.Contains(line, "X") {
					trimmed := strings.TrimLeft(line, " ")
					leadingSpaces := len(line) - len(trimmed)
					Expect(leadingSpaces).To(Equal(0), "Content should be left-aligned (no leading spaces)")
					break
				}
			}
		})
	})

	Describe("Total Height Matches Terminal", func() {
		It("should render exactly terminal height lines", func() {
			view := layout.NewScreenLayout(termInfo).
				WithTheme(theme).
				WithContent("Content").
				WithHelp("Help")

			rendered := view.Render()
			lines := strings.Split(rendered, "\n")

			Expect(lines).To(HaveLen(termInfo.Height))
		})

		It("should maintain terminal height with different sizes", func() {
			smallTerm := &terminal.Info{Width: 40, Height: 10, IsValid: true}
			view := layout.NewScreenLayout(smallTerm).
				WithTheme(theme).
				WithContent("Content").
				WithHelp("Help")

			rendered := view.Render()
			lines := strings.Split(rendered, "\n")

			Expect(lines).To(HaveLen(10))
		})
	})

	Describe("WithSecondaryContent", func() {
		It("should return the same ScreenLayout pointer for chaining", func() {
			view := layout.NewScreenLayout(termInfo)

			result := view.WithSecondaryContent("secondary pane")

			Expect(result).To(BeIdenticalTo(view), "builder must preserve pointer identity for chainability")
		})

		It("should store the supplied secondary content", func() {
			view := layout.NewScreenLayout(termInfo).
				WithSecondaryContent("secondary pane")

			Expect(layout.SecondaryContent(view)).To(Equal("secondary pane"))
		})

		It("should default to empty when WithSecondaryContent is never called", func() {
			view := layout.NewScreenLayout(termInfo)

			Expect(layout.SecondaryContent(view)).To(BeEmpty())
		})

		It("should overwrite the stored value when called twice", func() {
			view := layout.NewScreenLayout(termInfo).
				WithSecondaryContent("first").
				WithSecondaryContent("second")

			Expect(layout.SecondaryContent(view)).To(Equal("second"))
		})
	})

	Describe("Dual-Pane Rendering", func() {
		// renderDualPane builds a layout of the supplied width and returns
		// the content-band lines (between header and footer). Width of each
		// line is used to verify the 70/30 split — every content line is
		// padded to terminalWidth so lipgloss composition is visible.
		renderDualPane := func(width int, primary, secondary string) []string {
			info := &terminal.Info{Width: width, Height: 24, IsValid: true}
			view := layout.NewScreenLayout(info).
				WithTheme(theme).
				WithContent(primary).
				WithSecondaryContent(secondary)
			rendered := view.Render()
			return strings.Split(stripAnsi(rendered), "\n")
		}

		// findContentLine locates the first rendered line that contains the
		// primary pane body. That's the line where we measure the split.
		findContentLine := func(lines []string, needle string) string {
			for _, line := range lines {
				if strings.Contains(line, needle) {
					return line
				}
			}
			return ""
		}

		DescribeTable("renders dual-pane with 70/30 split and single separator",
			func(width, expectedPrimary, expectedSecondary int) {
				lines := renderDualPane(width, "PRIMARY", "SECONDARY")

				line := findContentLine(lines, "PRIMARY")
				Expect(line).NotTo(BeEmpty(), "primary content line must exist")
				Expect(line).To(ContainSubstring("SECONDARY"), "secondary content must share the same horizontal band")
				Expect([]rune(line)).To(HaveLen(width), "content line must span full terminal width")

				// Separator column is expected at position expectedPrimary
				// (0-indexed). That single rune should be the box-drawing
				// vertical bar.
				runes := []rune(line)
				Expect(string(runes[expectedPrimary])).To(Equal("│"),
					"separator '│' must sit at column %d (width=%d)", expectedPrimary, width)

				// Exactly one separator column on the content line.
				Expect(strings.Count(line, "│")).To(Equal(1),
					"content line must contain exactly one '│' separator")

				// Invariants from ADR.
				Expect(expectedPrimary+1+expectedSecondary).To(Equal(width),
					"primary + separator + secondary must equal width")
				Expect(expectedPrimary).To(BeNumerically(">=", expectedSecondary),
					"primary pane must be at least as wide as secondary")
			},
			Entry("80 cols → 55 / 1 / 24", 80, 55, 24),
			Entry("81 cols → 56 / 1 / 24 (odd)", 81, 56, 24),
			Entry("82 cols → 56 / 1 / 25", 82, 56, 25),
			Entry("100 cols → 69 / 1 / 30", 100, 69, 30),
			Entry("101 cols → 70 / 1 / 30 (odd)", 101, 70, 30),
			Entry("120 cols → 83 / 1 / 36", 120, 83, 36),
			Entry("121 cols → 84 / 1 / 36 (odd)", 121, 84, 36),
		)

		It("falls back to single-pane when width is below the 80-col threshold", func() {
			lines := renderDualPane(79, "PRIMARY", "SECONDARY")

			line := findContentLine(lines, "PRIMARY")
			Expect(line).NotTo(BeEmpty(), "primary content line must exist")
			Expect(line).NotTo(ContainSubstring("│"),
				"no separator should be rendered below the dual-pane threshold")
			Expect(line).NotTo(ContainSubstring("SECONDARY"),
				"secondary content must be suppressed below the dual-pane threshold")
		})

		It("renders single-pane when secondary content is empty", func() {
			info := &terminal.Info{Width: 120, Height: 24, IsValid: true}
			view := layout.NewScreenLayout(info).
				WithTheme(theme).
				WithContent("PRIMARY ONLY")

			rendered := view.Render()
			stripped := stripAnsi(rendered)
			lines := strings.Split(stripped, "\n")

			var contentLine string
			for _, line := range lines {
				if strings.Contains(line, "PRIMARY ONLY") {
					contentLine = line
					break
				}
			}

			Expect(contentLine).NotTo(BeEmpty())
			Expect(contentLine).NotTo(ContainSubstring("│"),
				"single-pane render must not include the '│' separator")
		})

		// Accessibility B1: the dual-pane separator must NOT be styled with
		// the default theme's BorderColor (#3d4454 → ~1.68:1 contrast on the
		// default #1a1f2e background, below WCAG 1.4.11's 3:1 floor). We
		// assert the raw (un-stripped) output does not embed an ANSI escape
		// setting the foreground to #3d4454 on any line containing '│'.
		It("does not style the separator with the low-contrast BorderColor", func() {
			info := &terminal.Info{Width: 120, Height: 24, IsValid: true}
			rendered := layout.NewScreenLayout(info).
				WithTheme(theme).
				WithContent("PRIMARY").
				WithSecondaryContent("SECONDARY").
				Render()

			// Lipgloss encodes truecolor foreground as
			//   ESC[38;2;R;G;Bm
			// For #3d4454 that's 61;68;84.
			lowContrastFg := "\x1b[38;2;61;68;84m"
			Expect(rendered).NotTo(ContainSubstring(lowContrastFg),
				"separator must not render with theme BorderColor (#3d4454) — fails WCAG 1.4.11 Non-Text Contrast")

			// Sanity: the separator glyph must still be present in the output.
			stripped := stripAnsi(rendered)
			Expect(stripped).To(ContainSubstring("│"),
				"separator glyph must still render after dropping the low-contrast override")
		})
	})

	Describe("Available Content Height Calculation", func() {
		It("should calculate available content height correctly", func() {
			logo := NewMockLogo("LOGO\nLINE2\nLINE3") // 3 lines
			view := layout.NewScreenLayout(termInfo).
				WithLogo(logo, 0).
				WithBreadcrumbs("Home", "Settings").
				WithTheme(theme).
				WithHelp("Help text")

			// Expected calculation:
			// Terminal height: 24
			// Header: 2 (blank) + 3 (logo) + 1 (blank) + 1 (breadcrumbs) + 1 (blank) = 8 lines
			// Footer: 1 (blank) + 1 (help) = 2 lines
			// Available content: 24 - 8 - 2 = 14 lines
			availableHeight := view.GetAvailableContentHeight()

			Expect(availableHeight).To(BeNumerically(">=", 10), "Should have at least 10 lines for content")
			Expect(availableHeight).To(BeNumerically("<=", 18), "Should not exceed reasonable content height")
		})

		It("should return full height when no header or footer", func() {
			view := layout.NewScreenLayout(termInfo).
				WithTheme(theme)
			// No logo, no help = minimal header/footer

			availableHeight := view.GetAvailableContentHeight()

			// Should be close to terminal height (24) minus minimal margins
			Expect(availableHeight).To(BeNumerically(">", 20), "Should use most of terminal for content")
		})

		It("should handle small terminals gracefully", func() {
			smallTerm := &terminal.Info{Width: 40, Height: 10, IsValid: true}
			logo := NewMockLogo("LOGO")
			view := layout.NewScreenLayout(smallTerm).
				WithLogo(logo, 0).
				WithTheme(theme).
				WithHelp("Help")

			availableHeight := view.GetAvailableContentHeight()

			// Even in small terminal, should return positive height
			Expect(availableHeight).To(BeNumerically(">", 0), "Should always return positive content height")
			Expect(availableHeight).To(BeNumerically("<", 10), "Should be less than terminal height")
		})
	})
})
