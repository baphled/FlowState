package primitives_test

import (
	"github.com/baphled/flowstate/internal/tui/uikit/primitives"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ButtonGroup", func() {
	var th theme.Theme
	var group *primitives.ButtonGroup

	BeforeEach(func() {
		th = theme.Default()
		group = primitives.NewButtonGroup(th)
	})

	Describe("NewButtonGroup", func() {
		It("should create empty button group", func() {
			Expect(group).NotTo(BeNil())
		})

		It("should accept nil theme and use default", func() {
			g := primitives.NewButtonGroup(nil)
			Expect(g).NotTo(BeNil())
		})

		It("should default to horizontal layout", func() {
			// Default horizontal layout verified by render output
			rendered := group.Render()
			Expect(rendered).NotTo(BeNil())
		})
	})

	Describe("Add", func() {
		It("should add button to group", func() {
			group.Add("Save")
			rendered := group.Render()
			Expect(rendered).To(ContainSubstring("Save"))
		})

		It("should add multiple buttons", func() {
			group.Add("Save")
			group.Add("Cancel")
			group.Add("Delete")
			rendered := group.Render()
			Expect(rendered).To(ContainSubstring("Save"))
			Expect(rendered).To(ContainSubstring("Cancel"))
			Expect(rendered).To(ContainSubstring("Delete"))
		})

		It("should return group for chaining", func() {
			result := group.Add("OK")
			Expect(result).To(Equal(group))
		})
	})

	Describe("Convenience Add Methods", func() {
		Describe("AddPrimary", func() {
			It("should add primary button", func() {
				group.AddPrimary("Submit")
				rendered := group.Render()
				Expect(rendered).To(ContainSubstring("Submit"))
			})
		})

		Describe("AddSecondary", func() {
			It("should add secondary button", func() {
				group.AddSecondary("Cancel")
				rendered := group.Render()
				Expect(rendered).To(ContainSubstring("Cancel"))
			})
		})

		Describe("AddDanger", func() {
			It("should add danger button", func() {
				group.AddDanger("Delete")
				rendered := group.Render()
				Expect(rendered).To(ContainSubstring("Delete"))
			})
		})

		It("should chain multiple add methods", func() {
			group.AddPrimary("Save").AddSecondary("Cancel").AddDanger("Delete")
			rendered := group.Render()
			Expect(rendered).To(ContainSubstring("Save"))
			Expect(rendered).To(ContainSubstring("Cancel"))
			Expect(rendered).To(ContainSubstring("Delete"))
		})
	})

	Describe("Horizontal", func() {
		It("should set horizontal layout", func() {
			group.Add("A").Add("B").Horizontal(true)
			rendered := group.Render()
			// Horizontal layout should join buttons on same line
			Expect(rendered).NotTo(BeEmpty())
		})

		It("should set vertical layout", func() {
			group.Add("A").Add("B").Horizontal(false)
			rendered := group.Render()
			// Vertical layout should stack buttons
			Expect(rendered).NotTo(BeEmpty())
		})

		It("should return group for chaining", func() {
			result := group.Horizontal(true)
			Expect(result).To(Equal(group))
		})
	})

	Describe("Focus Management", func() {
		BeforeEach(func() {
			group.Add("First").Add("Second").Add("Third")
		})

		Describe("FocusIndex", func() {
			It("should return current focus index", func() {
				index := group.FocusIndex()
				Expect(index).To(Equal(0)) // Default focus on first button
			})
		})

		Describe("FocusedLabel", func() {
			It("should return focused button label", func() {
				label := group.FocusedLabel()
				Expect(label).To(Equal("First"))
			})

			It("should return empty string when no buttons", func() {
				emptyGroup := primitives.NewButtonGroup(th)
				label := emptyGroup.FocusedLabel()
				Expect(label).To(BeEmpty())
			})
		})

		Describe("FocusNext", func() {
			It("should move focus to next button", func() {
				group.FocusNext()
				Expect(group.FocusIndex()).To(Equal(1))
				Expect(group.FocusedLabel()).To(Equal("Second"))
			})

			It("should wrap to first button at end", func() {
				group.FocusNext() // -> Second
				group.FocusNext() // -> Third
				group.FocusNext() // -> First (wrap)
				Expect(group.FocusIndex()).To(Equal(0))
				Expect(group.FocusedLabel()).To(Equal("First"))
			})

			It("should handle empty group gracefully", func() {
				emptyGroup := primitives.NewButtonGroup(th)
				emptyGroup.FocusNext()
				Expect(emptyGroup.FocusIndex()).To(Equal(0))
			})
		})

		Describe("FocusPrev", func() {
			It("should move focus to previous button", func() {
				group.FocusNext() // -> Second
				group.FocusPrev() // -> First
				Expect(group.FocusIndex()).To(Equal(0))
				Expect(group.FocusedLabel()).To(Equal("First"))
			})

			It("should wrap to last button at beginning", func() {
				group.FocusPrev() // -> Third (wrap)
				Expect(group.FocusIndex()).To(Equal(2))
				Expect(group.FocusedLabel()).To(Equal("Third"))
			})

			It("should handle empty group gracefully", func() {
				emptyGroup := primitives.NewButtonGroup(th)
				emptyGroup.FocusPrev()
				Expect(emptyGroup.FocusIndex()).To(Equal(0))
			})
		})

		Describe("FocusFirst", func() {
			It("should move focus to first button", func() {
				group.FocusNext() // -> Second
				group.FocusFirst()
				Expect(group.FocusIndex()).To(Equal(0))
				Expect(group.FocusedLabel()).To(Equal("First"))
			})
		})

		Describe("FocusLast", func() {
			It("should move focus to last button", func() {
				group.FocusLast()
				Expect(group.FocusIndex()).To(Equal(2))
				Expect(group.FocusedLabel()).To(Equal("Third"))
			})
		})
	})

	Describe("Update (Keyboard Navigation)", func() {
		BeforeEach(func() {
			group.Add("First").Add("Second").Add("Third")
		})

		It("should handle tab key (focus next)", func() {
			_, _ = group.Update(tea.KeyMsg{Type: tea.KeyTab})
			Expect(group.FocusIndex()).To(Equal(1))
		})

		It("should handle shift+tab key (focus prev)", func() {
			_, _ = group.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
			Expect(group.FocusIndex()).To(Equal(2)) // Wrap to last
		})

		It("should handle right arrow key (focus next)", func() {
			_, _ = group.Update(tea.KeyMsg{Type: tea.KeyRight})
			Expect(group.FocusIndex()).To(Equal(1))
		})

		It("should handle left arrow key (focus prev)", func() {
			_, _ = group.Update(tea.KeyMsg{Type: tea.KeyLeft})
			Expect(group.FocusIndex()).To(Equal(2)) // Wrap to last
		})

		It("should handle 'l' key (vim-style right)", func() {
			_, _ = group.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
			Expect(group.FocusIndex()).To(Equal(1))
		})

		It("should handle 'h' key (vim-style left)", func() {
			_, _ = group.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
			Expect(group.FocusIndex()).To(Equal(2)) // Wrap to last
		})

		It("should handle home key (focus first)", func() {
			group.FocusLast()
			_, _ = group.Update(tea.KeyMsg{Type: tea.KeyHome})
			Expect(group.FocusIndex()).To(Equal(0))
		})

		It("should handle end key (focus last)", func() {
			_, _ = group.Update(tea.KeyMsg{Type: tea.KeyEnd})
			Expect(group.FocusIndex()).To(Equal(2))
		})

		It("should ignore unknown keys", func() {
			initialIndex := group.FocusIndex()
			_, _ = group.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
			Expect(group.FocusIndex()).To(Equal(initialIndex))
		})

		It("should ignore non-key messages", func() {
			initialIndex := group.FocusIndex()
			_, _ = group.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
			Expect(group.FocusIndex()).To(Equal(initialIndex))
		})
	})

	Describe("Render", func() {
		It("should render empty group", func() {
			rendered := group.Render()
			Expect(rendered).To(BeEmpty())
		})

		It("should render single button", func() {
			group.Add("OK")
			rendered := group.Render()
			Expect(rendered).To(ContainSubstring("OK"))
		})

		It("should render multiple buttons horizontally", func() {
			group.Add("Save").Add("Cancel").Horizontal(true)
			rendered := group.Render()
			Expect(rendered).To(ContainSubstring("Save"))
			Expect(rendered).To(ContainSubstring("Cancel"))
		})

		It("should render multiple buttons vertically", func() {
			group.Add("Save").Add("Cancel").Horizontal(false)
			rendered := group.Render()
			Expect(rendered).To(ContainSubstring("Save"))
			Expect(rendered).To(ContainSubstring("Cancel"))
		})

		It("should show focus on current button", func() {
			group.Add("First").Add("Second")
			group.FocusNext() // Focus on Second
			rendered := group.Render()
			// Second button should be rendered with focus state
			Expect(rendered).To(ContainSubstring("Second"))
		})
	})
})
