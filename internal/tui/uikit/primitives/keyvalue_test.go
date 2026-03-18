package primitives_test

import (
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/primitives"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("KeyValue", func() {
	var th theme.Theme

	BeforeEach(func() {
		th = theme.Default()
	})

	Describe("NewKeyValue", func() {
		It("should create KeyValue with theme", func() {
			kv := primitives.NewKeyValue(th)
			Expect(kv).NotTo(BeNil())
		})

		It("should accept nil theme", func() {
			kv := primitives.NewKeyValue(nil)
			Expect(kv).NotTo(BeNil())
		})
	})

	Describe("Configuration", func() {
		It("should set label width", func() {
			kv := primitives.NewKeyValue(th)
			result := kv.LabelWidth(20)
			Expect(result).To(Equal(kv)) // Check chaining
		})

		It("should set separator", func() {
			kv := primitives.NewKeyValue(th)
			result := kv.Separator("\n---\n")
			Expect(result).To(Equal(kv))
		})

		It("should add key-value pairs", func() {
			kv := primitives.NewKeyValue(th)
			result := kv.Add("Name:", "John")
			Expect(result).To(Equal(kv))
		})

		It("should add muted pairs", func() {
			kv := primitives.NewKeyValue(th)
			result := kv.AddMuted("Updated:", "2025-01-20")
			Expect(result).To(Equal(kv))
		})

		It("should add blank lines", func() {
			kv := primitives.NewKeyValue(th)
			result := kv.AddBlank()
			Expect(result).To(Equal(kv))
		})

		It("should clear all pairs", func() {
			kv := primitives.NewKeyValue(th).Add("Key:", "Value")
			result := kv.Clear()
			Expect(result).To(Equal(kv))
			Expect(kv.Render()).To(BeEmpty())
		})

		It("should support chaining", func() {
			kv := primitives.NewKeyValue(th).
				LabelWidth(15).
				Add("Name:", "John").
				Add("Email:", "john@example.com").
				AddBlank().
				AddMuted("Created:", "2025-01-01")

			Expect(kv).NotTo(BeNil())
		})
	})

	Describe("Rendering", func() {
		It("should render empty string for no pairs", func() {
			kv := primitives.NewKeyValue(th)
			rendered := kv.Render()
			Expect(rendered).To(BeEmpty())
		})

		It("should render single pair", func() {
			kv := primitives.NewKeyValue(th).Add("Name:", "John")
			rendered := kv.Render()
			Expect(rendered).To(ContainSubstring("Name:"))
			Expect(rendered).To(ContainSubstring("John"))
		})

		It("should render multiple pairs", func() {
			kv := primitives.NewKeyValue(th).
				Add("Name:", "John").
				Add("Email:", "john@example.com")
			rendered := kv.Render()
			Expect(rendered).To(ContainSubstring("Name:"))
			Expect(rendered).To(ContainSubstring("John"))
			Expect(rendered).To(ContainSubstring("Email:"))
			Expect(rendered).To(ContainSubstring("john@example.com"))
		})

		It("should render muted pairs differently", func() {
			kv := primitives.NewKeyValue(th).
				Add("Name:", "John").
				AddMuted("Updated:", "2025-01-20")
			rendered := kv.Render()
			Expect(rendered).To(ContainSubstring("Name:"))
			Expect(rendered).To(ContainSubstring("Updated:"))
		})

		It("should render blank lines for spacing", func() {
			kv := primitives.NewKeyValue(th).
				Add("Name:", "John").
				AddBlank().
				Add("Email:", "john@example.com")
			rendered := kv.Render()

			// Should have blank line between pairs
			lines := splitKeyValueLines(rendered)
			Expect(len(lines)).To(BeNumerically(">=", 3))
		})

		It("should respect label width", func() {
			kv := primitives.NewKeyValue(th).
				LabelWidth(20).
				Add("Name:", "John")
			rendered := kv.Render()
			Expect(rendered).To(ContainSubstring("Name:"))
			Expect(rendered).To(ContainSubstring("John"))
		})

		It("should handle nil theme gracefully", func() {
			kv := primitives.NewKeyValue(nil).Add("Key:", "Value")
			rendered := kv.Render()
			Expect(rendered).To(ContainSubstring("Key:"))
			Expect(rendered).To(ContainSubstring("Value"))
		})
	})
})

// Helper to split rendered output into lines.
func splitKeyValueLines(s string) []string {
	return strings.Split(s, "\n")
}
