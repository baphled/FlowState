package feedback_test

import (
	"github.com/baphled/flowstate/internal/tui/uikit/feedback"
	"github.com/baphled/flowstate/internal/ui/themes"
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ConfirmModal", func() {
	var (
		modal *feedback.ConfirmModal
		theme themes.Theme
	)

	BeforeEach(func() {
		theme = themes.NewDefaultTheme()
		modal = feedback.NewConfirmModal("Delete Item", "Are you sure you want to delete this item?")
		modal.SetDimensions(100, 24)
	})

	Describe("NewConfirmModal", func() {
		It("should create a visible modal", func() {
			Expect(modal.IsVisible()).To(BeTrue())
		})

		It("should not be confirmed initially", func() {
			Expect(modal.WasConfirmed()).To(BeFalse())
		})

		It("should render title and message", func() {
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
			Expect(view).To(ContainSubstring("Delete Item"))
			Expect(view).To(ContainSubstring("Are you sure"))
		})
	})

	Describe("Variants", func() {
		It("should use destructive variant styling", func() {
			modal = feedback.NewConfirmModal("Delete Event", "This cannot be undone").
				WithVariant(feedback.ConfirmDestructive)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
			Expect(view).To(ContainSubstring("Delete Event"))
		})

		It("should use warning variant styling", func() {
			modal = feedback.NewConfirmModal("Warning", "This action may have side effects").
				WithVariant(feedback.ConfirmWarning)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
			Expect(view).To(ContainSubstring("Warning"))
		})

		It("should use default variant styling", func() {
			modal = feedback.NewConfirmModal("Confirm", "Continue?").
				WithVariant(feedback.ConfirmDefault)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
			Expect(view).To(ContainSubstring("Confirm"))
		})
	})

	Describe("Theme", func() {
		It("should accept custom theme", func() {
			modal = feedback.NewConfirmModal("Test", "Message").
				WithTheme(theme)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should handle nil theme gracefully", func() {
			modal = feedback.NewConfirmModal("Test", "Message").
				WithTheme(nil)
			// Should not panic, uses default theme
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("Update - Confirmation", func() {
		It("should confirm on 'y' key", func() {
			cmd, confirmed := modal.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
			Expect(cmd).To(BeNil())
			Expect(confirmed).To(BeTrue())
			Expect(modal.IsVisible()).To(BeFalse())
			Expect(modal.WasConfirmed()).To(BeTrue())
		})

		It("should confirm on 'Y' key", func() {
			cmd, confirmed := modal.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
			Expect(cmd).To(BeNil())
			Expect(confirmed).To(BeTrue())
			Expect(modal.WasConfirmed()).To(BeTrue())
		})

		It("should confirm on Enter key", func() {
			cmd, confirmed := modal.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).To(BeNil())
			Expect(confirmed).To(BeTrue())
			Expect(modal.WasConfirmed()).To(BeTrue())
		})
	})

	Describe("Update - Cancellation", func() {
		It("should cancel on 'n' key", func() {
			cmd, confirmed := modal.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
			Expect(cmd).To(BeNil())
			Expect(confirmed).To(BeFalse())
			Expect(modal.IsVisible()).To(BeFalse())
			Expect(modal.WasConfirmed()).To(BeFalse())
		})

		It("should cancel on 'N' key", func() {
			cmd, confirmed := modal.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
			Expect(cmd).To(BeNil())
			Expect(confirmed).To(BeFalse())
			Expect(modal.WasConfirmed()).To(BeFalse())
		})

		It("should cancel on Esc key", func() {
			cmd, confirmed := modal.Update(tea.KeyMsg{Type: tea.KeyEsc})
			Expect(cmd).To(BeNil())
			Expect(confirmed).To(BeFalse())
			Expect(modal.WasConfirmed()).To(BeFalse())
		})
	})

	Describe("Update - Other keys", func() {
		It("should ignore other keys", func() {
			_, confirmed := modal.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
			Expect(confirmed).To(BeFalse())
			Expect(modal.IsVisible()).To(BeTrue())
		})

		It("should handle window size message", func() {
			cmd, confirmed := modal.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
			Expect(cmd).To(BeNil())
			Expect(confirmed).To(BeFalse())
			Expect(modal.IsVisible()).To(BeTrue())
		})

		It("should ignore input when not visible", func() {
			modal.Hide()
			_, confirmed := modal.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
			Expect(confirmed).To(BeFalse())
			Expect(modal.WasConfirmed()).To(BeFalse())
		})
	})

	Describe("Visibility", func() {
		It("should show modal", func() {
			modal.Hide()
			Expect(modal.IsVisible()).To(BeFalse())
			modal.Show()
			Expect(modal.IsVisible()).To(BeTrue())
		})

		It("should hide modal", func() {
			Expect(modal.IsVisible()).To(BeTrue())
			modal.Hide()
			Expect(modal.IsVisible()).To(BeFalse())
		})

		It("should reset confirmed state on Show", func() {
			modal.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
			Expect(modal.WasConfirmed()).To(BeTrue())
			modal.Show()
			Expect(modal.WasConfirmed()).To(BeFalse())
		})

		It("should return empty string when not visible", func() {
			modal.Hide()
			view := modal.View()
			Expect(view).To(BeEmpty())
		})
	})

	Describe("Dimensions", func() {
		It("should update dimensions", func() {
			modal.SetDimensions(80, 24)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should handle very small dimensions", func() {
			modal.SetDimensions(20, 10)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("View", func() {
		It("should render help footer with keyboard shortcuts", func() {
			view := modal.View()
			Expect(view).To(ContainSubstring("y/Enter"))
			Expect(view).To(ContainSubstring("n/Esc"))
		})

		It("should use solid background", func() {
			modal = feedback.NewConfirmModal("Test", "Message").
				WithTheme(theme)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("Init", func() {
		It("should return nil command", func() {
			cmd := modal.Init()
			Expect(cmd).To(BeNil())
		})
	})

	Describe("Fluent API", func() {
		It("should support method chaining", func() {
			modal = feedback.NewConfirmModal("Title", "Message").
				WithVariant(feedback.ConfirmDestructive).
				WithTheme(theme)
			Expect(modal).NotTo(BeNil())
			Expect(modal.IsVisible()).To(BeTrue())
		})
	})

	Describe("Integration - Full Workflow", func() {
		It("should handle confirm workflow", func() {
			// Initial state
			Expect(modal.IsVisible()).To(BeTrue())
			Expect(modal.WasConfirmed()).To(BeFalse())

			// View should render
			view := modal.View()
			Expect(view).NotTo(BeEmpty())

			// Confirm action
			cmd, confirmed := modal.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
			Expect(cmd).To(BeNil())
			Expect(confirmed).To(BeTrue())

			// Modal should be hidden and confirmed
			Expect(modal.IsVisible()).To(BeFalse())
			Expect(modal.WasConfirmed()).To(BeTrue())

			// View should be empty
			view = modal.View()
			Expect(view).To(BeEmpty())
		})

		It("should handle cancel workflow", func() {
			// Initial state
			Expect(modal.IsVisible()).To(BeTrue())

			// View should render
			view := modal.View()
			Expect(view).NotTo(BeEmpty())

			// Cancel action
			cmd, confirmed := modal.Update(tea.KeyMsg{Type: tea.KeyEsc})
			Expect(cmd).To(BeNil())
			Expect(confirmed).To(BeFalse())

			// Modal should be hidden and not confirmed
			Expect(modal.IsVisible()).To(BeFalse())
			Expect(modal.WasConfirmed()).To(BeFalse())
		})
	})
})
