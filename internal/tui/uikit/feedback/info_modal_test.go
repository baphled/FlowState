package feedback_test

import (
	"github.com/baphled/flowstate/internal/tui/uikit/feedback"
	"github.com/baphled/flowstate/internal/ui/themes"
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("InfoModal", func() {
	var (
		modal *feedback.InfoModal
		theme themes.Theme
	)

	BeforeEach(func() {
		theme = themes.NewDefaultTheme()
		modal = feedback.NewInfoModal("Test Title", "Test message")
	})

	Describe("NewInfoModal", func() {
		It("should create an info modal with info variant", func() {
			Expect(modal).NotTo(BeNil())
			Expect(modal.GetVariant()).To(Equal(feedback.InfoModalInfo))
		})

		It("should set title and message", func() {
			Expect(modal.GetTitle()).To(Equal("Test Title"))
			Expect(modal.GetMessage()).To(Equal("Test message"))
		})

		It("should be visible by default", func() {
			Expect(modal.IsVisible()).To(BeTrue())
		})

		It("should use default theme", func() {
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should set default dimensions", func() {
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("NewWarningInfoModal", func() {
		It("should create a warning modal with warning variant", func() {
			warningModal := feedback.NewWarningInfoModal("Warning", "Warning message")
			Expect(warningModal.GetVariant()).To(Equal(feedback.InfoModalWarning))
		})

		It("should set title and message", func() {
			warningModal := feedback.NewWarningInfoModal("Warning Title", "Warning content")
			Expect(warningModal.GetTitle()).To(Equal("Warning Title"))
			Expect(warningModal.GetMessage()).To(Equal("Warning content"))
		})

		It("should be visible by default", func() {
			warningModal := feedback.NewWarningInfoModal("Warning", "Message")
			Expect(warningModal.IsVisible()).To(BeTrue())
		})
	})

	Describe("NewSuccessInfoModal", func() {
		It("should create a success modal with success variant", func() {
			successModal := feedback.NewSuccessInfoModal("Success", "Success message")
			Expect(successModal.GetVariant()).To(Equal(feedback.InfoModalSuccess))
		})

		It("should set title and message", func() {
			successModal := feedback.NewSuccessInfoModal("Success Title", "Success content")
			Expect(successModal.GetTitle()).To(Equal("Success Title"))
			Expect(successModal.GetMessage()).To(Equal("Success content"))
		})

		It("should be visible by default", func() {
			successModal := feedback.NewSuccessInfoModal("Success", "Message")
			Expect(successModal.IsVisible()).To(BeTrue())
		})
	})

	Describe("Init", func() {
		It("should return nil command", func() {
			cmd := modal.Init()
			Expect(cmd).To(BeNil())
		})
	})

	Describe("Update", func() {
		Context("when modal is visible", func() {
			It("should dismiss on Enter key", func() {
				msg := tea.KeyMsg{Type: tea.KeyEnter}
				consumed := modal.Update(msg)
				Expect(consumed).To(BeTrue())
				Expect(modal.IsVisible()).To(BeFalse())
			})

			It("should dismiss on Space key", func() {
				msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
				consumed := modal.Update(msg)
				Expect(consumed).To(BeTrue())
				Expect(modal.IsVisible()).To(BeFalse())
			})

			It("should dismiss on Esc key", func() {
				msg := tea.KeyMsg{Type: tea.KeyEsc}
				consumed := modal.Update(msg)
				Expect(consumed).To(BeTrue())
				Expect(modal.IsVisible()).To(BeFalse())
			})

			It("should ignore other keys", func() {
				msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
				consumed := modal.Update(msg)
				Expect(consumed).To(BeFalse())
				Expect(modal.IsVisible()).To(BeTrue())
			})

			It("should handle window size messages", func() {
				msg := tea.WindowSizeMsg{Width: 120, Height: 30}
				consumed := modal.Update(msg)
				Expect(consumed).To(BeFalse())
				Expect(modal.IsVisible()).To(BeTrue())
			})
		})

		Context("when modal is hidden", func() {
			BeforeEach(func() {
				modal.Hide()
			})

			It("should not process key messages", func() {
				msg := tea.KeyMsg{Type: tea.KeyEnter}
				consumed := modal.Update(msg)
				Expect(consumed).To(BeFalse())
				Expect(modal.IsVisible()).To(BeFalse())
			})

			It("should not process window size messages", func() {
				msg := tea.WindowSizeMsg{Width: 120, Height: 30}
				consumed := modal.Update(msg)
				Expect(consumed).To(BeFalse())
				Expect(modal.IsVisible()).To(BeFalse())
			})
		})
	})

	Describe("View", func() {
		It("should return empty string when hidden", func() {
			modal.Hide()
			view := modal.View()
			Expect(view).To(Equal(""))
		})

		It("should render content when visible", func() {
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
			Expect(view).To(ContainSubstring("Test Title"))
			Expect(view).To(ContainSubstring("Test message"))
		})

		It("should render with border", func() {
			view := modal.View()
			Expect(view).To(ContainSubstring("╭"))
		})

		It("should render footer with close hint", func() {
			view := modal.View()
			Expect(view).To(SatisfyAny(
				ContainSubstring("Enter"),
				ContainSubstring("Esc"),
				ContainSubstring("Close"),
			))
		})

		It("should use theme colors", func() {
			modal.WithTheme(theme)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should handle long messages", func() {
			longMessage := "This is a very long message that should be wrapped properly in the modal. " +
				"It contains multiple sentences and should display correctly without breaking the layout."
			longModal := feedback.NewInfoModal("Title", longMessage)
			view := longModal.View()
			Expect(view).NotTo(BeEmpty())
			Expect(view).To(ContainSubstring("Title"))
		})

		It("should handle empty message", func() {
			emptyModal := feedback.NewInfoModal("Title", "")
			view := emptyModal.View()
			Expect(view).NotTo(BeEmpty())
			Expect(view).To(ContainSubstring("Title"))
		})
	})

	Describe("IsVisible", func() {
		It("should return true when visible", func() {
			Expect(modal.IsVisible()).To(BeTrue())
		})

		It("should return false when hidden", func() {
			modal.Hide()
			Expect(modal.IsVisible()).To(BeFalse())
		})
	})

	Describe("Show", func() {
		It("should make modal visible", func() {
			modal.Hide()
			modal.Show()
			Expect(modal.IsVisible()).To(BeTrue())
		})

		It("should allow multiple show calls", func() {
			modal.Show()
			modal.Show()
			Expect(modal.IsVisible()).To(BeTrue())
		})
	})

	Describe("Hide", func() {
		It("should make modal invisible", func() {
			modal.Hide()
			Expect(modal.IsVisible()).To(BeFalse())
		})

		It("should allow multiple hide calls", func() {
			modal.Hide()
			modal.Hide()
			Expect(modal.IsVisible()).To(BeFalse())
		})
	})

	Describe("WithTheme", func() {
		It("should set custom theme", func() {
			result := modal.WithTheme(theme)
			Expect(result).To(Equal(modal))
		})

		It("should return modal for chaining", func() {
			result := modal.WithTheme(theme).WithTheme(nil)
			Expect(result).To(Equal(modal))
		})

		It("should handle nil theme", func() {
			modal.WithTheme(nil)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should use custom theme in rendering", func() {
			modal.WithTheme(theme)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("SetDimensions", func() {
		It("should update width and height", func() {
			modal.SetDimensions(120, 30)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should handle small dimensions", func() {
			modal.SetDimensions(40, 10)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should handle large dimensions", func() {
			modal.SetDimensions(200, 50)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("GetTitle", func() {
		It("should return the modal title", func() {
			Expect(modal.GetTitle()).To(Equal("Test Title"))
		})

		It("should return empty string for empty title", func() {
			emptyModal := feedback.NewInfoModal("", "Message")
			Expect(emptyModal.GetTitle()).To(Equal(""))
		})
	})

	Describe("GetMessage", func() {
		It("should return the modal message", func() {
			Expect(modal.GetMessage()).To(Equal("Test message"))
		})

		It("should return empty string for empty message", func() {
			emptyModal := feedback.NewInfoModal("Title", "")
			Expect(emptyModal.GetMessage()).To(Equal(""))
		})
	})

	Describe("GetVariant", func() {
		It("should return info variant for info modal", func() {
			Expect(modal.GetVariant()).To(Equal(feedback.InfoModalInfo))
		})

		It("should return warning variant for warning modal", func() {
			warningModal := feedback.NewWarningInfoModal("Warning", "Message")
			Expect(warningModal.GetVariant()).To(Equal(feedback.InfoModalWarning))
		})

		It("should return success variant for success modal", func() {
			successModal := feedback.NewSuccessInfoModal("Success", "Message")
			Expect(successModal.GetVariant()).To(Equal(feedback.InfoModalSuccess))
		})
	})

	Describe("Variant-specific rendering", func() {
		It("should render info modal with info color", func() {
			infoModal := feedback.NewInfoModal("Info", "Message")
			infoModal.WithTheme(theme)
			view := infoModal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should render warning modal with warning color", func() {
			warningModal := feedback.NewWarningInfoModal("Warning", "Message")
			warningModal.WithTheme(theme)
			view := warningModal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should render success modal with success color", func() {
			successModal := feedback.NewSuccessInfoModal("Success", "Message")
			successModal.WithTheme(theme)
			view := successModal.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("Integration", func() {
		It("should handle full workflow", func() {
			Expect(modal.IsVisible()).To(BeTrue())

			view := modal.View()
			Expect(view).To(ContainSubstring("Test Title"))

			modal.SetDimensions(120, 30)
			view = modal.View()
			Expect(view).NotTo(BeEmpty())

			modal.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(modal.IsVisible()).To(BeFalse())

			view = modal.View()
			Expect(view).To(Equal(""))
		})

		It("should handle theme and dimension changes", func() {
			modal.WithTheme(theme)
			modal.SetDimensions(100, 25)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
			Expect(view).To(ContainSubstring("Test Title"))
		})

		It("should handle show/hide cycles", func() {
			modal.Hide()
			Expect(modal.IsVisible()).To(BeFalse())

			modal.Show()
			Expect(modal.IsVisible()).To(BeTrue())

			modal.Hide()
			Expect(modal.IsVisible()).To(BeFalse())
		})
	})
})
