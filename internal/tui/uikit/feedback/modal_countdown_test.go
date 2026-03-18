package feedback

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Modal Countdown", func() {
	Describe("Initialization", func() {
		It("should return countdown tick command for success modal with auto-dismiss", func() {
			modal := NewSuccessModal("Operation successful!")

			cmd := modal.Init()

			Expect(cmd).ToNot(BeNil(), "Expected Init() to return a countdown tick command")
		})

		It("should return nil for success modal without auto-dismiss", func() {
			modal := NewSuccessModal("Operation successful!")
			modal.AutoDismiss = 0

			cmd := modal.Init()

			Expect(cmd).To(BeNil(), "Expected Init() to return nil without auto-dismiss")
		})

		It("should return nil for non-success modals", func() {
			modal := NewErrorModal("Error", "Something went wrong")

			cmd := modal.Init()

			Expect(cmd).To(BeNil(), "Expected Init() to return nil for error modal")
		})
	})

	Describe("Countdown Tick Updates", func() {
		It("should return next tick command when time remains", func() {
			modal := NewSuccessModal("Operation successful!")

			cmd := modal.Update(ModalCountdownTickMsg{})

			Expect(cmd).ToNot(BeNil(), "Expected Update() to return next countdown tick")
		})

		It("should return auto-dismiss message when countdown expires", func() {
			modal := NewSuccessModal("Operation successful!")
			modal.countdownRemaining = 1

			cmd := modal.Update(ModalCountdownTickMsg{})

			Expect(cmd).ToNot(BeNil(), "Expected Update() to return a command")

			msg := cmd()
			Expect(msg).To(BeAssignableToTypeOf(ModalAutoDismissMsg{}),
				"Expected ModalAutoDismissMsg when countdown expires")
		})

		It("should ignore countdown tick for non-success modals", func() {
			modal := NewErrorModal("Error", "Something went wrong")

			cmd := modal.Update(ModalCountdownTickMsg{})

			Expect(cmd).To(BeNil(), "Expected Update() to ignore CountdownTickMsg for error modal")
		})
	})

	Describe("Countdown Display Rendering", func() {
		It("should show countdown text with time remaining", func() {
			modal := NewSuccessModal("Operation successful!")

			output := modal.Render(80, 40)

			Expect(output).To(ContainSubstring("Auto-dismiss in"),
				"Expected countdown display to show 'Auto-dismiss in' text")
			Expect(output).To(MatchRegexp(`\d+s`),
				"Expected countdown display to show seconds")
		})

		It("should not show countdown text when time expires", func() {
			modal := NewSuccessModal("Operation successful!")
			modal.countdownRemaining = 0

			output := modal.Render(80, 40)

			countdownOccurrences := strings.Count(output, "Auto-dismiss in")
			Expect(countdownOccurrences).To(Equal(0),
				"Expected countdown display to be hidden when time expires")
		})

		It("should show decreasing countdown as time passes", func() {
			modal := NewSuccessModal("Operation successful!")

			initialOutput := modal.Render(80, 40)
			Expect(initialOutput).To(ContainSubstring("Auto-dismiss in 3s"),
				"Expected initial countdown to show 3s")

			modal.countdownRemaining = 2

			laterOutput := modal.Render(80, 40)
			Expect(laterOutput).To(ContainSubstring("Auto-dismiss in 2s"),
				"Expected countdown to show 2s after countdown decrements")
		})
	})
})
