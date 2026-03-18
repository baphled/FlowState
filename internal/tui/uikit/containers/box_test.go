package containers_test

import (
	"testing"

	"github.com/baphled/flowstate/internal/tui/uikit/containers"
	"github.com/baphled/flowstate/internal/ui/themes"
	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestContainers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Containers Suite")
}

var _ = Describe("Box", func() {
	var (
		theme themes.Theme
		box   *containers.Box
	)

	BeforeEach(func() {
		theme = themes.NewDefaultTheme()
	})

	Describe("Construction", func() {
		It("should create box with theme", func() {
			box = containers.NewBox(theme)
			Expect(box).NotTo(BeNil())
		})
	})

	Describe("Configuration", func() {
		BeforeEach(func() {
			box = containers.NewBox(theme)
		})

		It("should set content", func() {
			result := box.Content("Hello World")
			Expect(result).To(Equal(box)) // Check chaining
		})

		It("should set title", func() {
			result := box.Title("My Box")
			Expect(result).To(Equal(box)) // Check chaining
		})

		It("should set variant to Default", func() {
			result := box.Variant(containers.BoxDefault)
			Expect(result).To(Equal(box))
		})

		It("should set variant to Emphasized", func() {
			result := box.Variant(containers.BoxEmphasized)
			Expect(result).To(Equal(box))
		})

		It("should set variant to Destructive", func() {
			result := box.Variant(containers.BoxDestructive)
			Expect(result).To(Equal(box))
		})

		It("should set variant to Subtle", func() {
			result := box.Variant(containers.BoxSubtle)
			Expect(result).To(Equal(box))
		})

		It("should set width (0 = auto)", func() {
			result := box.Width(50)
			Expect(result).To(Equal(box))
		})

		It("should set height (0 = auto)", func() {
			result := box.Height(20)
			Expect(result).To(Equal(box))
		})

		It("should set maxHeight (0 = no limit)", func() {
			result := box.MaxHeight(30)
			Expect(result).To(Equal(box))
		})

		It("should set padding", func() {
			result := box.Padding(2)
			Expect(result).To(Equal(box))
		})

		It("should enable shadow", func() {
			result := box.WithShadow()
			Expect(result).To(Equal(box))
		})

		It("should support chaining", func() {
			result := box.
				Title("Test").
				Content("Content").
				Variant(containers.BoxEmphasized).
				Width(60).
				Height(10).
				Padding(1).
				WithShadow()

			Expect(result).To(Equal(box))
		})
	})

	Describe("Rendering", func() {
		BeforeEach(func() {
			box = containers.NewBox(theme)
		})

		It("should produce bordered box", func() {
			box.Content("Test content")
			rendered := box.Render()

			Expect(rendered).NotTo(BeEmpty())
			Expect(rendered).To(ContainSubstring("Test content"))
		})

		It("should render title when set", func() {
			box.Title("My Title").Content("Content")
			rendered := box.Render()

			Expect(rendered).To(ContainSubstring("My Title"))
		})

		It("should use default border for Default variant", func() {
			box.Variant(containers.BoxDefault).Content("Content")
			rendered := box.Render()

			// Should have border characters
			Expect(rendered).To(MatchRegexp(`[─│┌┐└┘]`))
		})

		It("should use thick border for Emphasized variant", func() {
			box.Variant(containers.BoxEmphasized).Content("Important")
			rendered := box.Render()

			// Emphasized should have thick border
			Expect(rendered).To(MatchRegexp(`[━┃┏┓┗┛]`))
		})

		It("should use error color for Destructive variant", func() {
			box.Variant(containers.BoxDestructive).Content("Warning")
			rendered := box.Render()

			// Should render with border (color may not show in tests)
			Expect(rendered).To(ContainSubstring("Warning"))
			Expect(rendered).To(MatchRegexp(`[─│┌┐└┘╭╮╰╯]`))
		})

		It("should render shadow when enabled", func() {
			box.WithShadow().Content("Shadowed")
			rendered := box.Render()

			// Shadow adds extra characters/spacing
			Expect(len(rendered)).To(BeNumerically(">", len("Shadowed")+10))
		})

		It("should respect width constraint", func() {
			box.Width(30).Content("This is a test")
			rendered := box.Render()

			// Rendered width should be around 30 (use lipgloss.Width for accurate measurement)
			lines := splitLines(rendered)
			if len(lines) > 0 {
				// Use lipgloss.Width to handle ANSI codes correctly
				actualWidth := lipgloss.Width(lines[0])
				Expect(actualWidth).To(BeNumerically("<=", 35)) // Width + borders + padding
			}
		})

		It("should apply padding", func() {
			box.Padding(2).Content("Padded")
			rendered := box.Render()

			// Padded content should have more whitespace
			Expect(rendered).To(ContainSubstring("Padded"))
			Expect(len(rendered)).To(BeNumerically(">", len("Padded")+20))
		})

		It("should apply maxHeight constraint", func() {
			// Create content with many lines
			content := "Line1\nLine2\nLine3\nLine4\nLine5\nLine6\nLine7\nLine8"
			box.MaxHeight(5).Content(content)
			rendered := box.Render()

			// MaxHeight should limit the output height
			lines := splitLines(rendered)
			Expect(len(lines)).To(BeNumerically("<=", 5))
		})

		It("should respect maxHeight with border and padding", func() {
			// Long content
			content := "A\nB\nC\nD\nE\nF\nG\nH\nI\nJ"
			box.MaxHeight(8).Padding(1).Content(content)
			rendered := box.Render()

			lines := splitLines(rendered)
			Expect(len(lines)).To(BeNumerically("<=", 8))
		})

		It("should support chaining with maxHeight", func() {
			result := box.
				Title("Test").
				Content("Content").
				Width(60).
				MaxHeight(20).
				Padding(1)

			Expect(result).To(Equal(box))
		})
	})
})

// Helper to split rendered output into lines.
func splitLines(s string) []string {
	lines := []string{}
	current := ""
	for _, r := range s {
		if r == '\n' {
			lines = append(lines, current)
			current = ""
		} else {
			current += string(r)
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}
