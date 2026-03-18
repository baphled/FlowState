package primitives_test

import (
	"github.com/baphled/flowstate/internal/tui/uikit/primitives"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ProgressBar", func() {
	var th theme.Theme

	BeforeEach(func() {
		th = theme.Default()
	})

	Describe("NewProgressBar", func() {
		It("should create progress bar with value", func() {
			bar := primitives.NewProgressBar(0.5, th)
			Expect(bar).NotTo(BeNil())
		})

		It("should accept nil theme and use default", func() {
			bar := primitives.NewProgressBar(0.5, nil)
			Expect(bar).NotTo(BeNil())
		})

		It("should clamp value to 0.0 minimum", func() {
			bar := primitives.NewProgressBar(-0.5, th)
			rendered := bar.Render()
			Expect(rendered).NotTo(BeEmpty())
		})

		It("should clamp value to 1.0 maximum", func() {
			bar := primitives.NewProgressBar(1.5, th)
			rendered := bar.Render()
			Expect(rendered).NotTo(BeEmpty())
		})
	})

	Describe("Fluent API", func() {
		Describe("Width", func() {
			It("should set custom width", func() {
				bar := primitives.NewProgressBar(0.5, th).Width(10)
				rendered := bar.Render()
				Expect(rendered).NotTo(BeEmpty())
			})

			It("should return bar for chaining", func() {
				bar := primitives.NewProgressBar(0.5, th)
				result := bar.Width(20)
				Expect(result).To(Equal(bar))
			})
		})

		Describe("ShowPercentage", func() {
			It("should show percentage when enabled", func() {
				bar := primitives.NewProgressBar(0.75, th).ShowPercentage(true)
				rendered := bar.Render()
				Expect(rendered).To(ContainSubstring("75%"))
			})

			It("should not show percentage when disabled", func() {
				bar := primitives.NewProgressBar(0.75, th).ShowPercentage(false)
				rendered := bar.Render()
				Expect(rendered).NotTo(ContainSubstring("%"))
			})

			It("should return bar for chaining", func() {
				bar := primitives.NewProgressBar(0.5, th)
				result := bar.ShowPercentage(true)
				Expect(result).To(Equal(bar))
			})
		})

		Describe("Label", func() {
			It("should add label prefix", func() {
				bar := primitives.NewProgressBar(0.8, th).Label("Confidence:")
				rendered := bar.Render()
				Expect(rendered).To(ContainSubstring("Confidence:"))
			})

			It("should return bar for chaining", func() {
				bar := primitives.NewProgressBar(0.5, th)
				result := bar.Label("Test")
				Expect(result).To(Equal(bar))
			})
		})

		Describe("FilledChar and EmptyChar", func() {
			It("should use custom filled character", func() {
				bar := primitives.NewProgressBar(1.0, th).FilledChar("=").Width(5)
				rendered := bar.Render()
				Expect(rendered).To(ContainSubstring("====="))
			})

			It("should use custom empty character", func() {
				bar := primitives.NewProgressBar(0.0, th).EmptyChar("-").Width(5)
				rendered := bar.Render()
				Expect(rendered).To(ContainSubstring("-----"))
			})
		})
	})

	Describe("Render", func() {
		It("should render filled bar for 100%", func() {
			bar := primitives.NewProgressBar(1.0, th).Width(10)
			rendered := bar.Render()
			Expect(rendered).To(ContainSubstring("██████████"))
		})

		It("should render empty bar for 0%", func() {
			bar := primitives.NewProgressBar(0.0, th).Width(10)
			rendered := bar.Render()
			Expect(rendered).To(ContainSubstring("░░░░░░░░░░"))
		})

		It("should render partial bar for 50%", func() {
			bar := primitives.NewProgressBar(0.5, th).Width(10)
			rendered := bar.Render()
			Expect(rendered).To(ContainSubstring("█████░░░░░"))
		})

		It("should render with brackets by default", func() {
			bar := primitives.NewProgressBar(0.5, th).Width(10)
			rendered := bar.Render()
			Expect(rendered).To(ContainSubstring("["))
			Expect(rendered).To(ContainSubstring("]"))
		})

		It("should render label, bar, and percentage together", func() {
			bar := primitives.NewProgressBar(0.8, th).
				Label("Progress:").
				Width(10).
				ShowPercentage(true)
			rendered := bar.Render()
			Expect(rendered).To(ContainSubstring("Progress:"))
			Expect(rendered).To(ContainSubstring("["))
			Expect(rendered).To(ContainSubstring("]"))
			Expect(rendered).To(ContainSubstring("80%"))
		})
	})

	Describe("Convenience Constructors", func() {
		Describe("ConfidenceBar", func() {
			It("should create a confidence bar with label and percentage", func() {
				bar := primitives.ConfidenceBar(0.85, th)
				rendered := bar.Render()
				Expect(rendered).To(ContainSubstring("85%"))
			})

			It("should work with nil theme", func() {
				bar := primitives.ConfidenceBar(0.5, nil)
				rendered := bar.Render()
				Expect(rendered).To(ContainSubstring("50%"))
			})
		})

		Describe("CompactBar", func() {
			It("should create a compact bar without label or percentage", func() {
				bar := primitives.CompactBar(0.6, 10, th)
				rendered := bar.Render()
				Expect(rendered).NotTo(ContainSubstring("%"))
				Expect(rendered).To(ContainSubstring("["))
			})
		})
	})
})
