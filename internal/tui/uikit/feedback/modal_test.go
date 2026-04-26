package feedback_test

import (
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/tui/uikit/feedback"
	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// findBorderLines locates the top and bottom border line indices.
func findBorderLines(lines []string) (top, bottom int) {
	top, bottom = -1, -1
	for i, line := range lines {
		if strings.Contains(line, "╭") && strings.Contains(line, "╮") {
			top = i
		}
		if strings.Contains(line, "╰") && strings.Contains(line, "╯") {
			bottom = i
		}
	}
	return top, bottom
}

var _ = Describe("Modal (legacy)", func() {
	Describe("NewErrorModal", func() {
		It("constructs an error modal with the expected defaults", func() {
			modal := feedback.NewErrorModal("Error Title", "Error message")

			Expect(modal.Type).To(Equal(feedback.ModalError), "Type must be ModalError")
			Expect(modal.Title).To(Equal("Error Title"), "Title must round-trip")
			Expect(modal.Message).To(Equal("Error message"), "Message must round-trip")
			Expect(modal.Bell).To(BeTrue(), "Error modals must ring the bell")
			Expect(modal.Cancellable).To(BeTrue(), "Error modals must be cancellable")
			Expect(modal.FadeInDuration).To(Equal(150*time.Millisecond), "FadeInDuration default must be 150ms")
		})
	})

	Describe("NewLoadingModal", func() {
		It("constructs a loading modal with a spinner and the supplied message", func() {
			modal := feedback.NewLoadingModal("Loading...", true)

			Expect(modal.Type).To(Equal(feedback.ModalLoading), "Type must be ModalLoading")
			Expect(modal.Title).To(Equal("Loading"), "Title defaults to 'Loading'")
			Expect(modal.Message).To(Equal("Loading..."), "Message must round-trip")
			Expect(modal.Cancellable).To(BeTrue(), "Loading modals must be cancellable when requested")
			Expect(feedback.SpinnerForTest(modal)).NotTo(BeNil(), "Spinner must be initialised")
		})

		It("supports a wider message without aggressive line wrapping", func() {
			// The loading modal's max width is 100 — a 90-char message should
			// fit on a single line at terminal width 120.
			longMessage := strings.Repeat("X", 90)
			modal := feedback.NewLoadingModal(longMessage, false)

			output := modal.Render(120, 40)
			Expect(output).NotTo(BeEmpty(), "wide loading modal must render")

			lines := strings.Split(output, "\n")
			messageFound := false
			for _, line := range lines {
				if strings.Count(line, "X") >= 85 {
					messageFound = true
					break
				}
			}
			Expect(messageFound).To(BeTrue(), "long message must render without excessive wrapping (max width should be 100)")
		})

		It("respects narrow terminal widths to avoid cutoff", func() {
			longMessage := strings.Repeat("Y", 90)
			modal := feedback.NewLoadingModal(longMessage, false)

			output := modal.Render(80, 40)
			Expect(output).NotTo(BeEmpty(), "narrow terminal must still render output")

			lines := strings.Split(output, "\n")
			for i, line := range lines {
				lineWidth := lipgloss.Width(line)
				Expect(lineWidth).To(BeNumerically("<=", 80),
					"line %d must not exceed terminal width (got visual width %d)", i, lineWidth)
			}
		})
	})

	Describe("NewProgressModal", func() {
		It("constructs a progress modal with the supplied progress value", func() {
			modal := feedback.NewProgressModal("Installing", "Installing packages...", 0.5)
			Expect(modal.Type).To(Equal(feedback.ModalProgress), "Type must be ModalProgress")
			Expect(modal.Progress).To(Equal(0.5), "Progress must round-trip")
		})

		It("renders extreme progress values 0%, 50%, 100%", func() {
			cases := []struct {
				progress float64
				want     string
			}{
				{0.0, "0%"},
				{1.0, "100%"},
				{0.5, "50%"},
			}
			for _, tc := range cases {
				modal := feedback.NewProgressModal("Step", "Working...", tc.progress)
				output := modal.Render(80, 40)
				Expect(output).To(ContainSubstring(tc.want),
					"progress %.2f must render the %s label", tc.progress, tc.want)
			}
		})
	})

	Describe("NewSuccessModal", func() {
		It("constructs a success modal with a 3 second auto-dismiss", func() {
			modal := feedback.NewSuccessModal("Operation completed successfully")
			Expect(modal.Type).To(Equal(feedback.ModalSuccess), "Type must be ModalSuccess")
			Expect(modal.AutoDismiss).To(Equal(3*time.Second), "AutoDismiss default must be 3s")
		})

		It("renders without panic and exposes auto-dismiss timing", func() {
			modal := feedback.NewSuccessModal("Operation completed")
			Expect(modal.Type).To(Equal(feedback.ModalSuccess), "Success modal type must persist")
			output := modal.Render(80, 40)
			Expect(output).NotTo(BeEmpty(), "success modal must render output")
		})
	})

	Describe("NewWarningModal", func() {
		It("constructs a warning modal that rings the bell and is cancellable", func() {
			modal := feedback.NewWarningModal("Warning Title", "Warning message")
			Expect(modal.Type).To(Equal(feedback.ModalWarning), "Type must be ModalWarning")
			Expect(modal.Bell).To(BeTrue(), "Warning modals must ring the bell")
			Expect(modal.Cancellable).To(BeTrue(), "Warning modals must be cancellable")
		})
	})

	Describe("SetMessageRotator", func() {
		It("stores the rotator on the modal", func() {
			modal := feedback.NewLoadingModal("Loading...", false)
			messages := []string{"Analyzing...", "Processing...", "Finishing..."}
			rotator := feedback.NewLoadingMessageRotator(messages)

			modal.SetMessageRotator(rotator)
			Expect(feedback.MessageRotatorForTest(modal)).To(BeIdenticalTo(rotator),
				"messageRotator field must point at the supplied rotator")
		})
	})

	Describe("Render", func() {
		It("renders the error modal with title, message, icon and dismiss hint", func() {
			modal := feedback.NewErrorModal("Error", "Something went wrong")
			output := modal.Render(80, 24)

			Expect(output).NotTo(BeEmpty(), "error modal must render")
			Expect(output).To(ContainSubstring("Error"), "output must contain title")
			Expect(output).To(ContainSubstring("Something went wrong"), "output must contain message")
			Expect(output).To(ContainSubstring("⚠️"), "output must contain error icon")
			Expect(output).To(ContainSubstring("Press Esc to dismiss"), "output must contain dismiss hint")
		})

		It("renders the loading modal with loading icon and cancel hint", func() {
			modal := feedback.NewLoadingModal("Processing...", true)
			output := modal.Render(80, 24)

			Expect(output).NotTo(BeEmpty(), "loading modal must render")
			Expect(output).To(ContainSubstring("⏳"), "output must contain loading icon")
			Expect(output).To(ContainSubstring("Press Esc to cancel"), "output must contain cancel hint")
		})

		It("renders the progress modal with percentage, bar and icon", func() {
			modal := feedback.NewProgressModal("Installing", "Installing packages...", 0.65)
			output := modal.Render(80, 24)

			Expect(output).NotTo(BeEmpty(), "progress modal must render")
			Expect(output).To(ContainSubstring("65%"), "output must contain progress percentage")
			Expect(output).To(ContainSubstring("█"), "output must contain filled progress bar block")
			Expect(output).To(ContainSubstring("░"), "output must contain empty progress bar block")
			Expect(output).To(ContainSubstring("📊"), "output must contain progress icon")
		})

		It("renders the success modal with success icon and auto-dismiss hint", func() {
			modal := feedback.NewSuccessModal("Operation completed")
			output := modal.Render(80, 24)

			Expect(output).NotTo(BeEmpty(), "success modal must render")
			Expect(output).To(ContainSubstring("✅"), "output must contain success icon")
			Expect(output).To(ContainSubstring("Auto-dismiss"), "output must contain auto-dismiss hint")
		})

		It("renders the warning modal with warning icon", func() {
			modal := feedback.NewWarningModal("Warning", "This action is risky")
			output := modal.Render(80, 24)

			Expect(output).NotTo(BeEmpty(), "warning modal must render")
			Expect(output).To(ContainSubstring("⚠️"), "output must contain warning icon")
		})

		It("includes action buttons when Actions are set", func() {
			modal := feedback.NewErrorModal("Confirm", "Are you sure?")
			modal.Actions = []string{"Yes", "No"}

			output := modal.Render(80, 24)
			Expect(output).To(ContainSubstring("Yes"), "output must contain Yes button")
			Expect(output).To(ContainSubstring("No"), "output must contain No button")
		})
	})

	Describe("UpdateProgress", func() {
		It("updates progress and clamps out-of-range values", func() {
			modal := feedback.NewProgressModal("Test", "Test", 0.5)

			modal.UpdateProgress(0.75)
			Expect(modal.Progress).To(Equal(0.75), "in-range value must round-trip")

			modal.UpdateProgress(-0.5)
			Expect(modal.Progress).To(Equal(0.0), "negative values must clamp to 0.0")

			modal.UpdateProgress(1.5)
			Expect(modal.Progress).To(Equal(1.0), "values above 1.0 must clamp to 1.0")
		})
	})

	Describe("AdvanceSpinner", func() {
		It("cycles spinner frames without panicking", func() {
			modal := feedback.NewLoadingModal("Loading...", false)
			spinner := feedback.SpinnerForTest(modal)

			modal.AdvanceSpinner()

			initialFrame := spinner.GetFrame()
			for range 20 {
				modal.AdvanceSpinner()
			}

			currentFrame := spinner.GetFrame()
			Expect(currentFrame).NotTo(BeEmpty(), "spinner frame must be non-empty after advancing")
			Expect(initialFrame).NotTo(BeEmpty(), "initial spinner frame must be non-empty")
		})
	})

	Describe("RotateMessage", func() {
		It("advances and wraps through the configured messages", func() {
			modal := feedback.NewLoadingModal("Initial message", false)
			rotator := feedback.NewLoadingMessageRotator([]string{"Msg1", "Msg2", "Msg3"})
			modal.SetMessageRotator(rotator)

			Expect(modal.RotateMessage()).To(Equal("Msg2"), "first rotation must advance to Msg2")
			Expect(modal.RotateMessage()).To(Equal("Msg3"), "second rotation must advance to Msg3")
			Expect(modal.RotateMessage()).To(Equal("Msg1"), "third rotation must wrap back to Msg1")
		})
	})

	Describe("calculateOpacity", func() {
		It("stays in [0,1] immediately after construction and reaches 1.0 once fade is complete", func() {
			modal := feedback.NewErrorModal("Test", "Test")

			opacity := feedback.CalculateOpacityForTest(modal)
			Expect(opacity).To(BeNumerically(">=", 0.0), "opacity must be >= 0")
			Expect(opacity).To(BeNumerically("<=", 1.0), "opacity must be <= 1")

			feedback.SetFadeStartTimeForTest(modal, time.Now().Add(-200*time.Millisecond))
			opacity = feedback.CalculateOpacityForTest(modal)
			Expect(opacity).To(Equal(1.0), "opacity must be 1.0 once fade duration has elapsed")
		})
	})

	Describe("wrapText", func() {
		It("wraps long lines into multiple lines respecting (approximate) width", func() {
			text := "This is a very long line that should be wrapped to fit within the specified width constraint"
			wrapped := feedback.WrapTextForTest(text, 20)

			lines := strings.Split(wrapped, "\n")
			Expect(len(lines)).To(BeNumerically(">", 1), "long text must wrap to multiple lines")

			for _, line := range lines {
				Expect(len(line)).To(BeNumerically("<=", 30),
					"each wrapped line must be reasonably close to the requested width: %q", line)
			}
		})

		It("leaves an empty input unchanged", func() {
			Expect(feedback.WrapTextForTest("", 20)).To(Equal(""), "empty input must remain empty")
		})

		It("leaves a short input unchanged", func() {
			Expect(feedback.WrapTextForTest("Short", 20)).To(Equal("Short"),
				"input shorter than width must round-trip unchanged")
		})
	})

	Describe("Edge cases", func() {
		It("renders gracefully when modal content is larger than the terminal", func() {
			longMessage := strings.Repeat("This is a very long error message that exceeds terminal width. ", 10)
			modal := feedback.NewErrorModal("Error", longMessage)

			output := modal.Render(40, 10)
			Expect(output).NotTo(BeEmpty(), "modal larger than terminal must still render output")
		})

		It("renders very long error messages without panicking", func() {
			longMessage := strings.Repeat("Error detail. ", 100)
			modal := feedback.NewErrorModal("Error", longMessage)

			output := modal.Render(80, 40)
			Expect(output).NotTo(BeEmpty(), "very long error messages must wrap or truncate gracefully")
		})

		It("renders without panic during the fade-in window", func() {
			modal := feedback.NewErrorModal("Test", "Message")

			output := modal.Render(80, 40)
			Expect(output).NotTo(BeEmpty(), "must render output during fade-in")

			time.Sleep(50 * time.Millisecond)
			output = modal.Render(80, 40)
			Expect(output).NotTo(BeEmpty(), "must render output after some fade-in has elapsed")
		})

		It("handles rapid progress updates without panicking", func() {
			modal := feedback.NewProgressModal("Processing", "Processing items...", 0.0)

			for i := 0.0; i <= 1.0; i += 0.1 {
				modal.UpdateProgress(i)
				output := modal.Render(80, 40)
				Expect(output).NotTo(BeEmpty(),
					"rapid progress update %.1f must still render", i)
			}
		})

		It("renders with empty title and/or message", func() {
			cases := []struct {
				title, message string
			}{
				{"", "Message"},
				{"Title", ""},
				{"", ""},
			}
			for _, tc := range cases {
				modal := feedback.NewErrorModal(tc.title, tc.message)
				output := modal.Render(80, 40)
				Expect(output).NotTo(BeEmpty(),
					"modal with title=%q message=%q must still render", tc.title, tc.message)
			}
		})

		It("renders many long action buttons in a narrow terminal without panic", func() {
			modal := feedback.NewErrorModal("Error", "Test error message")
			modal.Actions = []string{
				"Very Long Action Button 1",
				"Very Long Action Button 2",
				"Very Long Action Button 3",
				"Very Long Action Button 4",
				"Very Long Action Button 5",
			}

			output := modal.Render(50, 20)
			Expect(output).NotTo(BeEmpty(), "many action buttons must render gracefully")
		})

		It("renders on a very small terminal", func() {
			modal := feedback.NewErrorModal("Error", "An error occurred")
			output := modal.Render(30, 10)
			Expect(output).NotTo(BeEmpty(), "small terminal must still produce output")
		})

		It("renders on a very large terminal", func() {
			modal := feedback.NewLoadingModal("Loading...", false)
			output := modal.Render(200, 60)
			Expect(output).NotTo(BeEmpty(), "large terminal must still produce output")
		})
	})

	Describe("SimpleSpinner", func() {
		It("advances through frames and cycles back without panicking", func() {
			spinner := feedback.NewSimpleSpinner()

			frame := spinner.GetFrame()
			Expect(frame).NotTo(BeEmpty(), "initial spinner frame must be non-empty")

			initialFrame := spinner.GetFrame()
			spinner.Advance()
			nextFrame := spinner.GetFrame()
			Expect(nextFrame).NotTo(Equal(initialFrame), "frame must change after a single advance")

			for range 20 {
				spinner.Advance()
			}
			frame = spinner.GetFrame()
			Expect(frame).NotTo(BeEmpty(), "frame must remain valid after many advances")
		})
	})

	Describe("LoadingMessageRotator", func() {
		It("rotates through configured messages and wraps", func() {
			rotator := feedback.NewLoadingMessageRotator([]string{"Loading...", "Processing...", "Finalizing..."})

			Expect(rotator.GetCurrent()).To(Equal("Loading..."), "first message must be returned initially")
			Expect(rotator.Rotate()).To(Equal("Processing..."), "first Rotate must move to second message")

			rotator.Rotate() // move to "Finalizing..."
			Expect(rotator.Rotate()).To(Equal("Loading..."), "Rotate past last message must wrap to first")
		})

		It("falls back to a default message when given an empty slice", func() {
			rotator := feedback.NewLoadingMessageRotator([]string{})
			Expect(rotator.GetCurrent()).To(Equal("Loading..."),
				"empty rotator must fall back to default 'Loading...' message")
		})
	})

	Describe("OverlayModal", func() {
		It("constructs with title, content and default width", func() {
			modal := feedback.NewOverlayModal("Test Title", "Test Content")

			Expect(modal.Title).To(Equal("Test Title"), "title must round-trip")
			Expect(modal.Content).To(Equal("Test Content"), "content must round-trip")
			Expect(modal.Width).To(Equal(feedback.DefaultOverlayWidth),
				"width must default to DefaultOverlayWidth")
		})

		It("clamps SetWidth to the configured min and max", func() {
			modal := feedback.NewOverlayModal("Test", "Content")

			modal.SetWidth(80)
			Expect(modal.Width).To(Equal(80), "in-range widths must round-trip")

			modal.SetWidth(10)
			Expect(modal.Width).To(Equal(feedback.MinOverlayWidth),
				"width below min must clamp to MinOverlayWidth")

			modal.SetWidth(200)
			Expect(modal.Width).To(Equal(feedback.MaxOverlayWidth),
				"width above max must clamp to MaxOverlayWidth")
		})

		It("stores footer text via SetFooter", func() {
			modal := feedback.NewOverlayModal("Test", "Content")
			modal.SetFooter("Press Esc to close")
			Expect(modal.Footer).To(Equal("Press Esc to close"), "Footer must round-trip")
		})
	})

	Describe("DimContent", func() {
		It("returns dimmed content that still contains the original text", func() {
			dimmed := feedback.DimContent("Test content")
			Expect(dimmed).NotTo(BeEmpty(), "dimmed content must be non-empty")
			Expect(dimmed).To(ContainSubstring("Test content"),
				"dimmed output must contain the original text")
		})

		It("returns empty when given empty input", func() {
			Expect(feedback.DimContent("")).To(Equal(""),
				"empty input must produce empty dimmed output")
		})
	})

	Describe("RenderOverlay", func() {
		It("renders the modal content over the supplied background", func() {
			background := strings.Repeat("Background content\n", 20)
			modalContent := "Modal content"

			output := feedback.RenderOverlay(background, modalContent, 80, 24, nil)
			Expect(output).NotTo(BeEmpty(), "overlay output must be non-empty")
			Expect(output).To(ContainSubstring("Modal content"),
				"overlay output must contain modal content")
		})
	})

	Describe("RenderOverlayWithDefaultTheme", func() {
		It("renders successfully with the default theme", func() {
			background := strings.Repeat("Background\n", 10)
			output := feedback.RenderOverlayWithDefaultTheme(background, "Test modal", 80, 24)
			Expect(output).NotTo(BeEmpty(), "default-theme overlay output must be non-empty")
		})
	})

	Describe("Init", func() {
		It("returns a tick command for loading modals so the spinner animates", func() {
			modal := feedback.NewLoadingModal("Loading...", false)
			Expect(modal.Init()).NotTo(BeNil(),
				"loading modal Init must return a tick command")
		})

		It("returns nil for non-loading modals", func() {
			modal := feedback.NewErrorModal("Error", "Something went wrong")
			Expect(modal.Init()).To(BeNil(),
				"error modals must not return a tick command from Init")
		})
	})

	Describe("Update with ModalSpinnerTickMsg", func() {
		It("advances the spinner and returns another tick command", func() {
			modal := feedback.NewLoadingModal("Loading...", false)
			spinner := feedback.SpinnerForTest(modal)
			initialFrame := spinner.GetFrame()

			cmd := modal.Update(feedback.ModalSpinnerTickMsg{})

			Expect(spinner.GetFrame()).NotTo(Equal(initialFrame),
				"spinner must advance after ModalSpinnerTickMsg")
			Expect(cmd).NotTo(BeNil(),
				"Update must return another tick command to keep animation going")
		})

		It("rotates the configured loading message", func() {
			modal := feedback.NewLoadingModal("Initial", false)
			rotator := feedback.NewLoadingMessageRotator([]string{"Msg1", "Msg2", "Msg3"})
			modal.SetMessageRotator(rotator)

			modal.Update(feedback.ModalSpinnerTickMsg{})

			Expect(rotator.GetCurrent()).To(Equal("Msg2"),
				"first tick must rotate the current message to Msg2")
		})
	})

	Describe("Loading modal box integrity", func() {
		// Diagnostic test verifying borders are intact and content is fully
		// visible in a typical terminal.
		It("renders complete top, bottom and side borders along with the message", func() {
			message := "Detecting burst patterns and analyzing data..."
			modal := feedback.NewLoadingModal(message, true)

			output := modal.Render(120, 40)
			lines := strings.Split(output, "\n")

			topBorderLine, bottomBorderLine := findBorderLines(lines)

			Expect(topBorderLine).NotTo(Equal(-1),
				"top border line must be present in modal output:\n%s", output)
			topLine := lines[topBorderLine]
			Expect(topLine).To(ContainSubstring("╭"), "top border must contain '╭'")
			Expect(topLine).To(ContainSubstring("╮"), "top border must contain '╮'")

			Expect(bottomBorderLine).NotTo(Equal(-1),
				"bottom border line must be present in modal output:\n%s", output)
			bottomLine := lines[bottomBorderLine]
			Expect(bottomLine).To(ContainSubstring("╰"), "bottom border must contain '╰'")
			Expect(bottomLine).To(ContainSubstring("╯"), "bottom border must contain '╯'")

			if topBorderLine >= 0 && bottomBorderLine > topBorderLine {
				for i := topBorderLine + 1; i < bottomBorderLine; i++ {
					line := lines[i]
					pipeCount := strings.Count(line, "│")
					Expect(pipeCount).To(BeNumerically(">=", 2),
						"line %d must have at least two side borders, got %d: %q",
						i, pipeCount, line)
				}
			}

			Expect(output).To(ContainSubstring("Detecting"),
				"modal must contain 'Detecting' from message; output:\n%s", output)
			Expect(output).To(ContainSubstring("burst"),
				"modal must contain 'burst' from message; output:\n%s", output)

			maxWidth := 0
			for _, line := range lines {
				if w := lipgloss.Width(line); w > maxWidth {
					maxWidth = w
				}
			}
			GinkgoWriter.Printf("Modal rendered: %d lines, max width %d\n", len(lines), maxWidth)
		})
	})
})
