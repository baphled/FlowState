package feedback_test

import (
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/feedback"
	"github.com/baphled/flowstate/internal/tui/uikit/primitives"
	"github.com/baphled/flowstate/internal/ui/themes"
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DetailModal", func() {
	var (
		modal *feedback.DetailModal
		theme themes.Theme
	)

	BeforeEach(func() {
		theme = themes.NewDefaultTheme()
		modal = feedback.NewDetailModal("Event Details", "This is the event content")
		modal.SetDimensions(100, 24)
	})

	Describe("NewDetailModal", func() {
		It("should create a visible modal", func() {
			Expect(modal.IsVisible()).To(BeTrue())
		})

		It("should render title and content", func() {
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
			Expect(view).To(ContainSubstring("Event Details"))
			Expect(view).To(ContainSubstring("event content"))
		})
	})

	Describe("Theme", func() {
		It("should accept custom theme", func() {
			modal = feedback.NewDetailModal("Test", "Content").
				WithTheme(theme)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should handle nil theme gracefully", func() {
			modal = feedback.NewDetailModal("Test", "Content").
				WithTheme(nil)
			// Should not panic, uses default theme
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("Footer", func() {
		It("should render default footer when no badges set", func() {
			view := modal.View()
			// Default footer should have close hint
			Expect(view).To(SatisfyAny(
				ContainSubstring("Esc"),
				ContainSubstring("Close"),
			))
		})

		It("should render custom footer badges", func() {
			modal = feedback.NewDetailModal("Title", "Content").
				WithTheme(theme).
				WithFooterBadges(
					primitives.HelpKeyBadge("e", "Edit", theme),
					primitives.HelpKeyBadge("d", "Delete", theme),
				)
			view := modal.View()
			Expect(view).To(ContainSubstring("Edit"))
			Expect(view).To(ContainSubstring("Delete"))
		})
	})

	Describe("Update - Close keys", func() {
		It("should close on Esc key", func() {
			_, cmd := modal.Update(tea.KeyMsg{Type: tea.KeyEsc})
			Expect(cmd).To(BeNil())
			Expect(modal.IsVisible()).To(BeFalse())
		})

		It("should close on Enter key", func() {
			modal.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(modal.IsVisible()).To(BeFalse())
		})

		It("should close on 'q' key", func() {
			modal.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
			Expect(modal.IsVisible()).To(BeFalse())
		})

		It("should close on Backspace key", func() {
			modal.Update(tea.KeyMsg{Type: tea.KeyBackspace})
			Expect(modal.IsVisible()).To(BeFalse())
		})
	})

	Describe("Update - Scroll keys", func() {
		BeforeEach(func() {
			// Create modal with long content that would be scrollable
			longContent := strings.Repeat("Line of content\n", 100)
			modal = feedback.NewDetailModal("Title", longContent)
			modal.SetDimensions(80, 24)
			// Initialize the viewport by rendering
			modal.View()
		})

		It("should handle scroll keys without closing", func() {
			modal.Show()
			modal.Update(tea.KeyMsg{Type: tea.KeyUp})
			Expect(modal.IsVisible()).To(BeTrue())

			modal.Show()
			modal.Update(tea.KeyMsg{Type: tea.KeyDown})
			Expect(modal.IsVisible()).To(BeTrue())
		})
	})

	Describe("Update - Window resize", func() {
		It("should handle window size message", func() {
			_, cmd := modal.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
			Expect(cmd).To(BeNil())
			Expect(modal.IsVisible()).To(BeTrue())
		})
	})

	Describe("Update - Hidden state", func() {
		It("should ignore input when not visible", func() {
			modal.Hide()
			modal.Update(tea.KeyMsg{Type: tea.KeyEsc})
			Expect(modal.IsVisible()).To(BeFalse())
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

		It("should handle large content with scrolling", func() {
			longContent := strings.Repeat("This is a line of content that should scroll.\n", 50)
			modal = feedback.NewDetailModal("Long Content", longContent)
			modal.SetDimensions(80, 24)
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("View", func() {
		It("should use solid background", func() {
			modal = feedback.NewDetailModal("Test", "Content").
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
			modal = feedback.NewDetailModal("Title", "Content").
				WithTheme(theme).
				WithFooterBadges(
					primitives.HelpKeyBadge("s", "Skills", theme),
					primitives.BackBadge(theme),
				)
			Expect(modal).NotTo(BeNil())
			Expect(modal.IsVisible()).To(BeTrue())
		})
	})

	Describe("SetContent", func() {
		It("should update the content", func() {
			modal.SetContent("New Content")
			view := modal.View()
			Expect(view).To(ContainSubstring("New Content"))
		})
	})

	Describe("SetTitle", func() {
		It("should update the title", func() {
			modal.SetTitle("New Title")
			view := modal.View()
			Expect(view).To(ContainSubstring("New Title"))
		})
	})

	Describe("Integration - Full Workflow", func() {
		It("should handle view and close workflow", func() {
			// Initial state
			Expect(modal.IsVisible()).To(BeTrue())

			// View should render
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
			Expect(view).To(ContainSubstring("Event Details"))

			// Close modal
			modal.Update(tea.KeyMsg{Type: tea.KeyEsc})

			// Modal should be hidden
			Expect(modal.IsVisible()).To(BeFalse())

			// View should be empty
			view = modal.View()
			Expect(view).To(BeEmpty())
		})
	})
})
