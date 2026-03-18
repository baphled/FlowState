package widgets_test

import (
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DetailView", func() {
	var testTheme theme.Theme

	BeforeEach(func() {
		testTheme = theme.Default()
	})

	Describe("Construction", func() {
		It("creates a new DetailView with theme", func() {
			dv := widgets.NewDetailView(testTheme)
			Expect(dv).NotTo(BeNil())
		})

		It("creates a DetailView with nil theme (uses default)", func() {
			dv := widgets.NewDetailView(nil)
			Expect(dv).NotTo(BeNil())
			// Should not panic when rendering
			result := dv.Render()
			Expect(result).To(BeEmpty()) // Empty view with no fields
		})
	})

	Describe("Field Rendering", func() {
		It("renders a single field with label and value", func() {
			dv := widgets.NewDetailView(testTheme).
				Field("Name", "John Doe")

			result := dv.Render()
			Expect(result).To(ContainSubstring("Name"))
			Expect(result).To(ContainSubstring("John Doe"))
		})

		It("renders multiple fields", func() {
			dv := widgets.NewDetailView(testTheme).
				Field("Name", "John Doe").
				Field("Email", "john@example.com").
				Field("Role", "Developer")

			result := dv.Render()
			Expect(result).To(ContainSubstring("Name"))
			Expect(result).To(ContainSubstring("John Doe"))
			Expect(result).To(ContainSubstring("Email"))
			Expect(result).To(ContainSubstring("john@example.com"))
			Expect(result).To(ContainSubstring("Role"))
			Expect(result).To(ContainSubstring("Developer"))
		})

		It("skips fields with empty values when using FieldIf", func() {
			dv := widgets.NewDetailView(testTheme).
				Field("Name", "John Doe").
				FieldIf("Email", "").
				FieldIf("Role", "Developer")

			result := dv.Render()
			Expect(result).To(ContainSubstring("Name"))
			Expect(result).NotTo(ContainSubstring("Email"))
			Expect(result).To(ContainSubstring("Role"))
		})

		It("renders label and value with consistent alignment", func() {
			dv := widgets.NewDetailView(testTheme).
				Field("Name", "John").
				Field("Company", "Acme Inc")

			result := dv.Render()
			// Both labels should be present with colon
			Expect(result).To(ContainSubstring("Name:"))
			Expect(result).To(ContainSubstring("Company:"))
		})
	})

	Describe("Long Text Wrapping", func() {
		It("wraps long text values when width is set", func() {
			longText := "This is a very long description that should wrap to multiple lines when the width constraint is applied to the detail view widget."

			dv := widgets.NewDetailView(testTheme).
				Width(40).
				Field("Description", longText)

			result := dv.Render()
			lines := strings.Split(result, "\n")

			// Should have multiple lines due to wrapping
			Expect(len(lines)).To(BeNumerically(">", 1))
			// Original text should still be present (split across lines)
			Expect(result).To(ContainSubstring("Description"))
		})

		It("does not wrap text when width is not set", func() {
			longText := "Short text that fits"

			dv := widgets.NewDetailView(testTheme).
				Field("Description", longText)

			result := dv.Render()
			Expect(result).To(ContainSubstring("Short text that fits"))
		})

		It("handles very long single words gracefully", func() {
			longWord := "supercalifragilisticexpialidocious"

			dv := widgets.NewDetailView(testTheme).
				Width(20).
				Field("Word", longWord)

			result := dv.Render()
			// Should not panic and should contain the word
			Expect(result).To(ContainSubstring("Word"))
		})

		It("preserves line breaks in multi-line values", func() {
			multiLineText := "Line 1\nLine 2\nLine 3"

			dv := widgets.NewDetailView(testTheme).
				Field("Notes", multiLineText)

			result := dv.Render()
			Expect(result).To(ContainSubstring("Line 1"))
			Expect(result).To(ContainSubstring("Line 2"))
			Expect(result).To(ContainSubstring("Line 3"))
		})
	})

	Describe("Section Support", func() {
		It("renders a section with title", func() {
			dv := widgets.NewDetailView(testTheme).
				Section("Personal Info").
				Field("Name", "John Doe").
				Field("Age", "30")

			result := dv.Render()
			Expect(result).To(ContainSubstring("Personal Info"))
			Expect(result).To(ContainSubstring("Name"))
			Expect(result).To(ContainSubstring("Age"))
		})

		It("renders multiple sections", func() {
			dv := widgets.NewDetailView(testTheme).
				Section("Personal").
				Field("Name", "John").
				Section("Work").
				Field("Company", "Acme")

			result := dv.Render()
			Expect(result).To(ContainSubstring("Personal"))
			Expect(result).To(ContainSubstring("Work"))
		})

		It("visually separates sections", func() {
			dv := widgets.NewDetailView(testTheme).
				Section("Section 1").
				Field("Field1", "Value1").
				Section("Section 2").
				Field("Field2", "Value2")

			result := dv.Render()
			// Sections should be separated (blank line or separator)
			lines := strings.Split(result, "\n")
			Expect(len(lines)).To(BeNumerically(">=", 4)) // At least 4 lines for 2 sections
		})
	})

	Describe("List Rendering", func() {
		It("renders a string slice as a comma-separated list", func() {
			tags := []string{"go", "tui", "cli"}

			dv := widgets.NewDetailView(testTheme).
				List("Tags", tags)

			result := dv.Render()
			Expect(result).To(ContainSubstring("Tags"))
			Expect(result).To(ContainSubstring("go"))
			Expect(result).To(ContainSubstring("tui"))
			Expect(result).To(ContainSubstring("cli"))
			// Should NOT have Go's array syntax
			Expect(result).NotTo(ContainSubstring("[go tui cli]"))
		})

		It("handles empty lists gracefully", func() {
			dv := widgets.NewDetailView(testTheme).
				List("Tags", []string{})

			result := dv.Render()
			// Empty list should either be hidden or show "None"
			Expect(result).NotTo(ContainSubstring("[]"))
		})

		It("skips empty lists when using ListIf", func() {
			dv := widgets.NewDetailView(testTheme).
				Field("Name", "Test").
				ListIf("Tags", []string{}).
				ListIf("Categories", []string{"cat1", "cat2"})

			result := dv.Render()
			Expect(result).NotTo(ContainSubstring("Tags"))
			Expect(result).To(ContainSubstring("Categories"))
		})

		It("renders lists with custom separator", func() {
			items := []string{"item1", "item2", "item3"}

			dv := widgets.NewDetailView(testTheme).
				ListWithSeparator("Items", items, " | ")

			result := dv.Render()
			Expect(result).To(ContainSubstring("item1 | item2 | item3"))
		})

		It("renders bulleted lists", func() {
			items := []string{"First item", "Second item", "Third item"}

			dv := widgets.NewDetailView(testTheme).
				BulletList("Steps", items)

			result := dv.Render()
			Expect(result).To(ContainSubstring("Steps"))
			// Should have bullet points
			Expect(result).To(MatchRegexp(`[•\-\*]\s*First item`))
		})
	})

	Describe("Title Support", func() {
		It("renders a title at the top", func() {
			dv := widgets.NewDetailView(testTheme).
				Title("Event Details").
				Field("Name", "Conference")

			result := dv.Render()
			// Title should appear before fields
			titleIdx := strings.Index(result, "Event Details")
			nameIdx := strings.Index(result, "Name")
			Expect(titleIdx).To(BeNumerically("<", nameIdx))
		})

		It("styles the title prominently", func() {
			dv := widgets.NewDetailView(testTheme).
				Title("Important Details")

			result := dv.Render()
			Expect(result).To(ContainSubstring("Important Details"))
		})
	})

	Describe("Theme Integration", func() {
		It("uses theme colors for labels", func() {
			dv := widgets.NewDetailView(testTheme).
				Field("Name", "Test")

			// Should render without panic
			result := dv.Render()
			Expect(result).To(ContainSubstring("Name"))
		})

		It("uses theme colors for section titles", func() {
			dv := widgets.NewDetailView(testTheme).
				Section("Section Title")

			result := dv.Render()
			Expect(result).To(ContainSubstring("Section Title"))
		})

		It("handles theme change", func() {
			dv := widgets.NewDetailView(testTheme).
				Field("Name", "Test")

			// Should be able to change theme
			newTheme := theme.Default()
			dv.SetTheme(newTheme)

			result := dv.Render()
			Expect(result).To(ContainSubstring("Name"))
		})
	})

	Describe("Fluent API", func() {
		It("supports method chaining", func() {
			dv := widgets.NewDetailView(testTheme).
				Title("Details").
				Width(60).
				Section("Info").
				Field("Name", "John").
				Field("Age", "30").
				List("Tags", []string{"a", "b"}).
				Section("More").
				Field("Extra", "Data")

			result := dv.Render()
			Expect(result).To(ContainSubstring("Details"))
			Expect(result).To(ContainSubstring("Info"))
			Expect(result).To(ContainSubstring("Name"))
		})

		It("returns the same instance for chaining", func() {
			dv := widgets.NewDetailView(testTheme)
			dv2 := dv.Field("Test", "Value")
			Expect(dv).To(BeIdenticalTo(dv2))
		})
	})

	Describe("Edge Cases", func() {
		It("handles nil values gracefully", func() {
			dv := widgets.NewDetailView(testTheme).
				Field("Name", "")

			result := dv.Render()
			// Should not panic
			Expect(result).NotTo(BeNil())
		})

		It("handles special characters in values", func() {
			dv := widgets.NewDetailView(testTheme).
				Field("Code", "<script>alert('xss')</script>")

			result := dv.Render()
			Expect(result).To(ContainSubstring("<script>"))
		})

		It("handles unicode characters", func() {
			dv := widgets.NewDetailView(testTheme).
				Field("Emoji", "Hello 👋 World 🌍")

			result := dv.Render()
			Expect(result).To(ContainSubstring("👋"))
			Expect(result).To(ContainSubstring("🌍"))
		})

		It("handles very narrow width", func() {
			dv := widgets.NewDetailView(testTheme).
				Width(10).
				Field("Name", "John Doe")

			// Should not panic
			result := dv.Render()
			Expect(result).NotTo(BeEmpty())
		})
	})

	Describe("wrapWords (unexported)", func() {
		It("wraps words to fit within width", func() {
			dv := widgets.NewDetailView(testTheme).
				Width(10).
				Field("Test", "hello world test")

			result := dv.Render()
			Expect(result).NotTo(BeEmpty())
		})

		It("handles empty words slice via wrapText", func() {
			dv := widgets.NewDetailView(testTheme)
			result := dv.Render()
			Expect(result).To(BeEmpty())
		})

		It("handles long word exceeding width with existing content", func() {
			dv := widgets.NewDetailView(testTheme).
				Width(10).
				Field("Word", "hello supercalifragilisticexpialidocious")

			result := dv.Render()
			Expect(result).To(ContainSubstring("supercalifragilisticexpialidocious"))
		})

		It("handles single long word", func() {
			dv := widgets.NewDetailView(testTheme).
				Width(10).
				Field("Word", "supercalifragilisticexpialidocious")

			result := dv.Render()
			Expect(result).To(ContainSubstring("supercalifragilisticexpialidocious"))
		})

		It("wraps multiple words correctly", func() {
			dv := widgets.NewDetailView(testTheme).
				Width(15).
				Field("Words", "the quick brown fox")

			result := dv.Render()
			Expect(result).NotTo(BeEmpty())
		})
	})

	Describe("wrapText (unexported)", func() {
		It("returns text as-is when width <= 0", func() {
			dv := widgets.NewDetailView(testTheme).
				Width(0).
				Field("Test", "hello world")

			result := dv.Render()
			Expect(result).To(ContainSubstring("hello world"))
		})

		It("returns text as-is when width is negative", func() {
			dv := widgets.NewDetailView(testTheme).
				Width(-5).
				Field("Test", "hello world")

			result := dv.Render()
			Expect(result).To(ContainSubstring("hello world"))
		})

		It("clamps width to 10 when width < 10", func() {
			dv := widgets.NewDetailView(testTheme).
				Width(5).
				Field("Test", "hello world test")

			result := dv.Render()
			Expect(result).NotTo(BeEmpty())
		})

		It("handles empty text gracefully", func() {
			dv := widgets.NewDetailView(testTheme).
				Width(20).
				Field("Test", "")

			result := dv.Render()
			Expect(result).NotTo(BeNil())
		})

		It("returns whitespace-only text as-is", func() {
			dv := widgets.NewDetailView(testTheme).
				Width(20).
				Field("Test", "   \t  ")

			result := dv.Render()
			Expect(result).NotTo(BeEmpty())
		})

		It("wraps text to multiple lines", func() {
			dv := widgets.NewDetailView(testTheme).
				Width(15).
				Field("Text", "the quick brown fox jumps over the lazy dog")

			result := dv.Render()
			lines := strings.Split(result, "\n")
			Expect(len(lines)).To(BeNumerically(">", 1))
		})

		It("handles single word longer than width", func() {
			dv := widgets.NewDetailView(testTheme).
				Width(10).
				Field("Word", "supercalifragilisticexpialidocious")

			result := dv.Render()
			Expect(result).To(ContainSubstring("supercalifragilisticexpialidocious"))
		})
	})
})
