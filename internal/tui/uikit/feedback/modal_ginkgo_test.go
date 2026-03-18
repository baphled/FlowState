package feedback_test

import (
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/feedback"
	"github.com/baphled/flowstate/internal/ui/themes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Modal", func() {
	Describe("WithTheme", func() {
		It("should set theme on Modal", func() {
			modal := feedback.NewErrorModal("Error", "Message")
			customTheme := themes.NewDefaultTheme()

			result := modal.WithTheme(customTheme)
			Expect(result).To(Equal(modal))
		})

		It("should handle nil theme on Modal", func() {
			modal := feedback.NewErrorModal("Error", "Message")
			result := modal.WithTheme(nil)
			Expect(result).To(Equal(modal))
		})
	})

	Describe("buildModalMessage", func() {
		It("should include spinner frame for loading modal", func() {
			modal := feedback.NewLoadingModal("Loading", false)
			output := modal.Render(80, 24)
			Expect(output).NotTo(BeEmpty())
			Expect(output).To(ContainSubstring("Loading"))
		})

		It("should include rotated message when rotator is set", func() {
			modal := feedback.NewLoadingModal("Default", false)
			rotator := feedback.NewLoadingMessageRotator([]string{"Step 1", "Step 2"})
			modal.SetMessageRotator(rotator)

			output := modal.Render(80, 24)
			Expect(output).NotTo(BeEmpty())
		})

		It("should combine spinner and rotated message", func() {
			modal := feedback.NewLoadingModal("Default", false)
			rotator := feedback.NewLoadingMessageRotator([]string{"Rotating"})
			modal.SetMessageRotator(rotator)

			output := modal.Render(80, 24)
			Expect(output).NotTo(BeEmpty())
			Expect(output).To(ContainSubstring("Rotating"))
		})
	})

	Describe("RotateMessage", func() {
		It("should cycle through messages", func() {
			rotator := feedback.NewLoadingMessageRotator([]string{"A", "B", "C"})

			Expect(rotator.GetCurrent()).To(Equal("A"))
			rotator.Rotate()
			Expect(rotator.GetCurrent()).To(Equal("B"))
			rotator.Rotate()
			Expect(rotator.GetCurrent()).To(Equal("C"))
			rotator.Rotate()
			Expect(rotator.GetCurrent()).To(Equal("A"))
		})

		It("should handle single message", func() {
			rotator := feedback.NewLoadingMessageRotator([]string{"Only"})
			rotator.Rotate()
			Expect(rotator.GetCurrent()).To(Equal("Only"))
		})
	})

	Describe("AdvanceSpinner", func() {
		It("should advance spinner without panic", func() {
			modal := feedback.NewLoadingModal("Loading", false)

			modal.AdvanceSpinner()

			output := modal.Render(80, 24)
			Expect(output).NotTo(BeEmpty())
		})
	})
})

var _ = Describe("OverlayModal", func() {
	Describe("WithTheme", func() {
		It("should set theme on OverlayModal", func() {
			modal := feedback.NewOverlayModal("Title", "Content")
			customTheme := themes.NewDefaultTheme()

			result := modal.WithTheme(customTheme)
			Expect(result).To(Equal(modal))
		})

		It("should handle nil theme on OverlayModal", func() {
			modal := feedback.NewOverlayModal("Title", "Content")
			result := modal.WithTheme(nil)
			Expect(result).To(Equal(modal))
		})
	})

	Describe("RenderCentered", func() {
		It("should render centered modal", func() {
			modal := feedback.NewOverlayModal("Test Title", "Test Content")
			background := strings.Repeat("Background\n", 30)

			output := modal.RenderCentered(background, 100, 40)
			Expect(output).NotTo(BeEmpty())
			Expect(output).To(ContainSubstring("Test Title"))
			Expect(output).To(ContainSubstring("Test Content"))
		})

		It("should render with custom theme", func() {
			modal := feedback.NewOverlayModal("Title", "Content")
			customTheme := themes.NewDefaultTheme()
			modal.WithTheme(customTheme)

			background := strings.Repeat("BG\n", 20)
			output := modal.RenderCentered(background, 80, 30)
			Expect(output).NotTo(BeEmpty())
		})

		It("should handle small terminal", func() {
			modal := feedback.NewOverlayModal("Title", "Content")
			background := "BG\n"

			output := modal.RenderCentered(background, 40, 10)
			Expect(output).NotTo(BeEmpty())
		})

		It("should handle large terminal", func() {
			modal := feedback.NewOverlayModal("Title", "Content")
			background := strings.Repeat("Background\n", 50)

			output := modal.RenderCentered(background, 200, 100)
			Expect(output).NotTo(BeEmpty())
		})
	})

	Describe("buildContent", func() {
		It("should include footer in rendered output", func() {
			modal := feedback.NewOverlayModal("Title", "Content")
			modal.SetFooter("Press Esc to close")

			background := strings.Repeat("BG\n", 20)
			output := modal.RenderCentered(background, 80, 30)
			Expect(output).To(ContainSubstring("Press Esc to close"))
		})

		It("should handle empty title", func() {
			modal := feedback.NewOverlayModal("", "Content only")

			background := strings.Repeat("BG\n", 20)
			output := modal.RenderCentered(background, 80, 30)
			Expect(output).NotTo(BeEmpty())
			Expect(output).To(ContainSubstring("Content only"))
		})

		It("should handle empty content", func() {
			modal := feedback.NewOverlayModal("Title", "")

			background := strings.Repeat("BG\n", 20)
			output := modal.RenderCentered(background, 80, 30)
			Expect(output).NotTo(BeEmpty())
		})

		It("should handle long title", func() {
			longTitle := strings.Repeat("X", 100)
			modal := feedback.NewOverlayModal(longTitle, "Content")

			background := strings.Repeat("BG\n", 20)
			output := modal.RenderCentered(background, 80, 30)
			Expect(output).NotTo(BeEmpty())
		})

		It("should handle long content", func() {
			longContent := strings.Repeat("Content line\n", 50)
			modal := feedback.NewOverlayModal("Title", longContent)

			background := strings.Repeat("BG\n", 20)
			output := modal.RenderCentered(background, 80, 30)
			Expect(output).NotTo(BeEmpty())
		})
	})
})
