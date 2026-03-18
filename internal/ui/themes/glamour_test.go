package themes_test

import (
	"github.com/baphled/flowstate/internal/ui/themes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Glamour Theme Integration", func() {
	var theme themes.Theme

	BeforeEach(func() {
		theme = themes.NewDefaultTheme()
	})

	Describe("NewGlamourStyle", func() {
		It("should create a glamour style name based on theme", func() {
			styleName := themes.NewGlamourStyleName(theme)
			Expect(styleName).NotTo(BeEmpty())
		})

		It("should return dark style for dark themes", func() {
			styleName := themes.NewGlamourStyleName(theme)
			// Default theme is dark
			Expect(styleName).To(Equal("dark"))
		})

		It("should return light style for light themes", func() {
			// Create a light theme
			lightPalette := &themes.ColorPalette{
				Background: "#ffffff",
				Foreground: "#000000",
			}
			lightTheme := themes.NewBaseTheme("light", "Light Theme", "Test", false, lightPalette)
			styleName := themes.NewGlamourStyleName(lightTheme)
			Expect(styleName).To(Equal("light"))
		})

		It("should handle nil theme", func() {
			styleName := themes.NewGlamourStyleName(nil)
			Expect(styleName).To(Equal("dark"))
		})
	})

	Describe("RenderMarkdown", func() {
		It("should render markdown content", func() {
			markdown := "# Hello World\n\nThis is a test."
			result, err := themes.RenderMarkdown(theme, markdown, 80)
			Expect(err).NotTo(HaveOccurred())
			// Content is rendered with ANSI styling, so just check it's not empty
			// and contains the text (possibly with ANSI codes interspersed)
			Expect(result).NotTo(BeEmpty())
			Expect(result).To(MatchRegexp("Hello.*World"))
		})

		It("should render bullet lists", func() {
			markdown := "- Item 1\n- Item 2\n- Item 3"
			result, err := themes.RenderMarkdown(theme, markdown, 80)
			Expect(err).NotTo(HaveOccurred())
			// Check content is present (may have ANSI codes)
			Expect(result).To(MatchRegexp("Item.*1"))
			Expect(result).To(MatchRegexp("Item.*2"))
		})

		It("should render code blocks", func() {
			markdown := "```go\nfunc main() {}\n```"
			result, err := themes.RenderMarkdown(theme, markdown, 80)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(ContainSubstring("func"))
		})

		It("should handle empty content", func() {
			result, err := themes.RenderMarkdown(theme, "", 80)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeEmpty())
		})

		It("should handle nil theme", func() {
			result, err := themes.RenderMarkdown(nil, "# Test", 80)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(ContainSubstring("Test"))
		})

		It("should respect width parameter", func() {
			longText := "This is a very long line that should wrap according to the width parameter specified."
			result, err := themes.RenderMarkdown(theme, longText, 40)
			Expect(err).NotTo(HaveOccurred())
			// The result should contain the text (wrapped)
			Expect(result).To(ContainSubstring("long"))
		})

		It("should render headings at different levels", func() {
			markdown := "# H1\n## H2\n### H3"
			result, err := themes.RenderMarkdown(theme, markdown, 80)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(ContainSubstring("H1"))
			Expect(result).To(ContainSubstring("H2"))
			Expect(result).To(ContainSubstring("H3"))
		})

		It("should render bold and italic text", func() {
			markdown := "**bold** and *italic* text"
			result, err := themes.RenderMarkdown(theme, markdown, 80)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(ContainSubstring("bold"))
			Expect(result).To(ContainSubstring("italic"))
		})

		It("should render links", func() {
			markdown := "[Link](https://example.com)"
			result, err := themes.RenderMarkdown(theme, markdown, 80)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(ContainSubstring("Link"))
		})

		It("should render tables", func() {
			markdown := "| Col1 | Col2 |\n|------|------|\n| A    | B    |"
			result, err := themes.RenderMarkdown(theme, markdown, 80)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeEmpty())
		})

		It("should handle very small width", func() {
			result, err := themes.RenderMarkdown(theme, "# Test", 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeEmpty())
		})

		It("should handle very large width", func() {
			result, err := themes.RenderMarkdown(theme, "# Test", 200)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeEmpty())
		})
	})

	Describe("RenderCVPreview", func() {
		It("should render CV content as formatted markdown", func() {
			cvContent := `# John Doe
## Senior Software Engineer

### Experience
- Led development of microservices architecture
- Mentored junior developers

### Skills
- Go, Python, JavaScript
- Docker, Kubernetes
`
			result, err := themes.RenderCVPreview(theme, cvContent, 80)
			Expect(err).NotTo(HaveOccurred())
			// Content is rendered with ANSI styling
			Expect(result).To(MatchRegexp("John.*Doe"))
			Expect(result).To(MatchRegexp("Experience"))
			Expect(result).To(MatchRegexp("Skills"))
		})

		It("should handle empty CV content", func() {
			result, err := themes.RenderCVPreview(theme, "", 80)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeEmpty())
		})
	})

	Describe("MarkdownRenderer", func() {
		var renderer *themes.MarkdownRenderer

		BeforeEach(func() {
			var err error
			renderer, err = themes.NewMarkdownRenderer(theme, 80)
			Expect(err).NotTo(HaveOccurred())
			Expect(renderer).NotTo(BeNil())
		})

		Describe("NewMarkdownRenderer", func() {
			It("should create a renderer with dark theme", func() {
				r, err := themes.NewMarkdownRenderer(theme, 80)
				Expect(err).NotTo(HaveOccurred())
				Expect(r).NotTo(BeNil())
			})

			It("should create a renderer with light theme", func() {
				lightPalette := &themes.ColorPalette{
					Background: "#ffffff",
					Foreground: "#000000",
				}
				lightTheme := themes.NewBaseTheme("light", "Light Theme", "Test", false, lightPalette)
				r, err := themes.NewMarkdownRenderer(lightTheme, 80)
				Expect(err).NotTo(HaveOccurred())
				Expect(r).NotTo(BeNil())
			})

			It("should create a renderer with nil theme", func() {
				r, err := themes.NewMarkdownRenderer(nil, 80)
				Expect(err).NotTo(HaveOccurred())
				Expect(r).NotTo(BeNil())
			})

			It("should create a renderer with different widths", func() {
				r, err := themes.NewMarkdownRenderer(theme, 40)
				Expect(err).NotTo(HaveOccurred())
				Expect(r).NotTo(BeNil())
			})
		})

		Describe("Render", func() {
			It("should render markdown content", func() {
				result, err := renderer.Render("# Test\n\nContent here")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(ContainSubstring("Test"))
			})

			It("should handle empty content", func() {
				result, err := renderer.Render("")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeEmpty())
			})

			It("should render bullet lists", func() {
				result, err := renderer.Render("- Item 1\n- Item 2")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(ContainSubstring("Item"))
			})

			It("should render code blocks", func() {
				result, err := renderer.Render("```go\nfunc test() {}\n```")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(ContainSubstring("func"))
			})
		})

		Describe("SetWidth", func() {
			It("should update renderer width", func() {
				err := renderer.SetWidth(40)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should render with new width after SetWidth", func() {
				err := renderer.SetWidth(40)
				Expect(err).NotTo(HaveOccurred())

				result, err := renderer.Render("This is a very long line that should wrap")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeEmpty())
			})

			It("should handle multiple SetWidth calls", func() {
				err := renderer.SetWidth(50)
				Expect(err).NotTo(HaveOccurred())

				err = renderer.SetWidth(100)
				Expect(err).NotTo(HaveOccurred())

				result, err := renderer.Render("# Test")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeEmpty())
			})
		})

		Describe("SetTheme", func() {
			It("should update renderer theme to light", func() {
				lightPalette := &themes.ColorPalette{
					Background: "#ffffff",
					Foreground: "#000000",
				}
				lightTheme := themes.NewBaseTheme("light", "Light Theme", "Test", false, lightPalette)
				err := renderer.SetTheme(lightTheme)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should update renderer theme to nil", func() {
				err := renderer.SetTheme(nil)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should render with new theme after SetTheme", func() {
				lightPalette := &themes.ColorPalette{
					Background: "#ffffff",
					Foreground: "#000000",
				}
				lightTheme := themes.NewBaseTheme("light", "Light Theme", "Test", false, lightPalette)
				err := renderer.SetTheme(lightTheme)
				Expect(err).NotTo(HaveOccurred())

				result, err := renderer.Render("# Test")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeEmpty())
			})

			It("should handle multiple SetTheme calls", func() {
				lightPalette := &themes.ColorPalette{
					Background: "#ffffff",
					Foreground: "#000000",
				}
				lightTheme := themes.NewBaseTheme("light", "Light Theme", "Test", false, lightPalette)

				err := renderer.SetTheme(lightTheme)
				Expect(err).NotTo(HaveOccurred())

				err = renderer.SetTheme(theme)
				Expect(err).NotTo(HaveOccurred())

				result, err := renderer.Render("# Test")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeEmpty())
			})
		})
	})
})
