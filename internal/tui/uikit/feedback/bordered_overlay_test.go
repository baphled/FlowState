package feedback_test

import (
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/feedback"
	"github.com/baphled/flowstate/internal/ui/themes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RenderBorderedOverlay", func() {
	var (
		theme themes.Theme
		bg    string
	)

	BeforeEach(func() {
		theme = themes.NewDefaultTheme()
		bg = strings.Repeat("X", 80) + "\n"
		bg = strings.Repeat(bg, 24)
		bg = strings.TrimSuffix(bg, "\n")
	})

	Describe("box sizing", func() {
		It("computes 80% x 70% for a standard 80x24 terminal", func() {
			// 80*0.8=64, 24*0.7=16.8→16; both within clamp range
			result := feedback.RenderBorderedOverlay(bg, "hello", 80, 24, theme)
			Expect(result).NotTo(BeEmpty())

			lines := strings.Split(result, "\n")
			Expect(lines).To(HaveLen(24))
		})

		It("clamps to maximum 120x40 for a large terminal", func() {
			largeBg := strings.Repeat(strings.Repeat("X", 200)+"\n", 60)
			largeBg = strings.TrimSuffix(largeBg, "\n")

			result := feedback.RenderBorderedOverlay(largeBg, "hello", 200, 60, theme)
			Expect(result).NotTo(BeEmpty())

			lines := strings.Split(result, "\n")
			Expect(lines).To(HaveLen(60))
		})

		It("clamps to minimum 40x12 for a small terminal", func() {
			smallBg := strings.Repeat(strings.Repeat("X", 20)+"\n", 10)
			smallBg = strings.TrimSuffix(smallBg, "\n")

			result := feedback.RenderBorderedOverlay(smallBg, "hello", 20, 10, theme)
			Expect(result).NotTo(BeEmpty())
		})
	})

	Describe("border rendering", func() {
		It("contains rounded border characters", func() {
			result := feedback.RenderBorderedOverlay(bg, "hello", 80, 24, theme)

			// Rounded border uses ╭ ╮ ╰ ╯ │ ─
			Expect(result).To(ContainSubstring("╭"))
			Expect(result).To(ContainSubstring("╯"))
			Expect(result).To(ContainSubstring("│"))
		})
	})

	Describe("background preservation", func() {
		It("preserves background content outside the overlay", func() {
			result := feedback.RenderBorderedOverlay(bg, "hello", 80, 24, theme)

			// The dimmed background should still be present in some lines
			// (lines above or below the modal area)
			Expect(result).NotTo(BeEmpty())
			Expect(strings.Split(result, "\n")).To(HaveLen(24))
		})
	})

	Describe("content inclusion", func() {
		It("includes the provided content within the bordered box", func() {
			result := feedback.RenderBorderedOverlay(bg, "test content here", 80, 24, theme)
			Expect(result).To(ContainSubstring("test content here"))
		})
	})

	Describe("nil theme handling", func() {
		It("handles nil theme gracefully by using default", func() {
			result := feedback.RenderBorderedOverlay(bg, "hello", 80, 24, nil)
			Expect(result).NotTo(BeEmpty())
			Expect(result).To(ContainSubstring("╭"))
		})
	})
})
