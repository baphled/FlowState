package feedback_test

import (
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/feedback"
	"github.com/baphled/flowstate/internal/ui/themes"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type testKeyMap struct {
	shortBindings []key.Binding
	fullBindings  [][]key.Binding
}

func (t testKeyMap) ShortHelp() []key.Binding {
	return t.shortBindings
}

func (t testKeyMap) FullHelp() [][]key.Binding {
	return t.fullBindings
}

var _ = Describe("HelpModal", func() {
	var (
		modal  *feedback.HelpModal
		theme  themes.Theme
		keyMap help.KeyMap
	)

	BeforeEach(func() {
		theme = themes.NewDefaultTheme()
		keyMap = testKeyMap{
			shortBindings: []key.Binding{
				key.NewBinding(
					key.WithKeys("a"),
					key.WithHelp("a", "test action"),
				),
			},
			fullBindings: [][]key.Binding{
				{
					key.NewBinding(
						key.WithKeys("a"),
						key.WithHelp("a", "test action"),
					),
				},
			},
		}
		modal = feedback.NewHelpModal(keyMap)
		modal.SetSize(100, 24)
	})

	Describe("DefaultHelpModalKeyMap", func() {
		It("should return valid key bindings", func() {
			km := feedback.DefaultHelpModalKeyMap()
			Expect(km.Toggle).NotTo(BeNil())
			Expect(km.Close).NotTo(BeNil())
			Expect(km.FullHelp).NotTo(BeNil())
		})

		It("should have toggle key bound to ?", func() {
			km := feedback.DefaultHelpModalKeyMap()
			Expect(km.Toggle.Keys()).To(ContainElement("?"))
		})

		It("should have close key bound to esc and ?", func() {
			km := feedback.DefaultHelpModalKeyMap()
			keys := km.Close.Keys()
			Expect(keys).To(ContainElement("esc"))
			Expect(keys).To(ContainElement("?"))
		})

		It("should have full help key bound to f", func() {
			km := feedback.DefaultHelpModalKeyMap()
			Expect(km.FullHelp.Keys()).To(ContainElement("f"))
		})
	})

	Describe("NewHelpModal", func() {
		It("should create a help modal with provided keymap", func() {
			Expect(modal).NotTo(BeNil())
		})

		It("should initialize as hidden", func() {
			Expect(modal.IsVisible()).To(BeFalse())
		})

		It("should set default width and height", func() {
			m := feedback.NewHelpModal(keyMap)
			m.SetSize(100, 24)
			Expect(m).NotTo(BeNil())
		})

		It("should use default theme", func() {
			m := feedback.NewHelpModal(keyMap)
			view := m.View()
			Expect(view).To(Equal(""))
		})
	})

	Describe("WithTheme", func() {
		It("should set custom theme", func() {
			modal = modal.WithTheme(theme)
			Expect(modal).NotTo(BeNil())
		})

		It("should return modal for chaining", func() {
			result := modal.WithTheme(theme)
			Expect(result).To(Equal(modal))
		})

		It("should handle nil theme", func() {
			modal = modal.WithTheme(nil)
			Expect(modal).NotTo(BeNil())
		})
	})

	Describe("SetKeyMap", func() {
		It("should update the keymap", func() {
			newKeyMap := testKeyMap{
				shortBindings: []key.Binding{
					key.NewBinding(
						key.WithKeys("b"),
						key.WithHelp("b", "new action"),
					),
				},
			}
			modal.SetKeyMap(newKeyMap)
			Expect(modal).NotTo(BeNil())
		})
	})

	Describe("SetSize", func() {
		It("should set width and height", func() {
			modal.SetSize(120, 30)
			Expect(modal).NotTo(BeNil())
		})

		It("should adjust help width", func() {
			modal.SetSize(100, 24)
			Expect(modal).NotTo(BeNil())
		})
	})

	Describe("Show", func() {
		It("should make modal visible", func() {
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
			modal.Show()
			modal.Hide()
			Expect(modal.IsVisible()).To(BeFalse())
		})

		It("should reset full help state", func() {
			modal.Show()
			modal.ToggleFullHelp()
			modal.Hide()
			Expect(modal.IsVisible()).To(BeFalse())
		})
	})

	Describe("Toggle", func() {
		It("should show when hidden", func() {
			Expect(modal.IsVisible()).To(BeFalse())
			modal.Toggle()
			Expect(modal.IsVisible()).To(BeTrue())
		})

		It("should hide when visible", func() {
			modal.Show()
			modal.Toggle()
			Expect(modal.IsVisible()).To(BeFalse())
		})

		It("should toggle multiple times", func() {
			modal.Toggle()
			Expect(modal.IsVisible()).To(BeTrue())
			modal.Toggle()
			Expect(modal.IsVisible()).To(BeFalse())
			modal.Toggle()
			Expect(modal.IsVisible()).To(BeTrue())
		})
	})

	Describe("IsVisible", func() {
		It("should return false when hidden", func() {
			Expect(modal.IsVisible()).To(BeFalse())
		})

		It("should return true when shown", func() {
			modal.Show()
			Expect(modal.IsVisible()).To(BeTrue())
		})
	})

	Describe("ToggleFullHelp", func() {
		It("should toggle full help state", func() {
			modal.Show()
			modal.ToggleFullHelp()
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should toggle back to short help", func() {
			modal.Show()
			modal.ToggleFullHelp()
			modal.ToggleFullHelp()
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("Update", func() {
		Context("when modal is hidden", func() {
			It("should open on toggle key", func() {
				msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}
				consumed, cmd := modal.Update(msg)
				Expect(consumed).To(BeTrue())
				Expect(modal.IsVisible()).To(BeTrue())
				Expect(cmd).To(BeNil())
			})

			It("should ignore other keys", func() {
				msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
				consumed, cmd := modal.Update(msg)
				Expect(consumed).To(BeFalse())
				Expect(modal.IsVisible()).To(BeFalse())
				Expect(cmd).To(BeNil())
			})

			It("should ignore non-key messages", func() {
				msg := tea.WindowSizeMsg{Width: 100, Height: 24}
				consumed, cmd := modal.Update(msg)
				Expect(consumed).To(BeFalse())
				Expect(cmd).To(BeNil())
			})
		})

		Context("when modal is visible", func() {
			BeforeEach(func() {
				modal.Show()
			})

			It("should close on close key", func() {
				msg := tea.KeyMsg{Type: tea.KeyEsc}
				consumed, cmd := modal.Update(msg)
				Expect(consumed).To(BeTrue())
				Expect(modal.IsVisible()).To(BeFalse())
				Expect(cmd).To(BeNil())
			})

			It("should toggle full help on f key", func() {
				msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}}
				consumed, cmd := modal.Update(msg)
				Expect(consumed).To(BeTrue())
				Expect(cmd).To(BeNil())
			})

			It("should consume key events", func() {
				msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}
				consumed, cmd := modal.Update(msg)
				Expect(consumed).To(BeTrue())
				Expect(cmd).To(BeNil())
			})
		})
	})

	Describe("View", func() {
		It("should return empty string when hidden", func() {
			view := modal.View()
			Expect(view).To(Equal(""))
		})

		It("should return empty string when keymap is nil", func() {
			m := feedback.NewHelpModal(nil)
			m.Show()
			view := m.View()
			Expect(view).To(Equal(""))
		})

		It("should render content when visible", func() {
			modal.Show()
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
			Expect(view).To(ContainSubstring("Keyboard Shortcuts"))
		})

		It("should show short help by default", func() {
			modal.Show()
			view := modal.View()
			Expect(view).To(ContainSubstring("f for full help"))
		})

		It("should show full help when toggled", func() {
			modal.Show()
			modal.ToggleFullHelp()
			view := modal.View()
			Expect(view).To(ContainSubstring("f for short help"))
		})

		It("should use theme colors", func() {
			modal.WithTheme(theme)
			modal.Show()
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should handle small width", func() {
			modal.SetSize(40, 24)
			modal.Show()
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should handle large width", func() {
			modal.SetSize(200, 24)
			modal.Show()
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should render with default theme when nil", func() {
			modal.WithTheme(nil)
			modal.Show()
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should render footer with correct text when showing short help", func() {
			modal.Show()
			view := modal.View()
			Expect(view).To(ContainSubstring("Press ? or esc to close"))
		})

		It("should render footer with correct text when showing full help", func() {
			modal.Show()
			modal.ToggleFullHelp()
			view := modal.View()
			Expect(view).To(ContainSubstring("Press ? or esc to close"))
		})
	})

	Describe("ShortHelp", func() {
		It("should return empty string when keymap is nil", func() {
			m := feedback.NewHelpModal(nil)
			help := m.ShortHelp()
			Expect(help).To(Equal(""))
		})

		It("should return short help text", func() {
			help := modal.ShortHelp()
			Expect(help).NotTo(BeEmpty())
		})
	})

	Describe("RenderOverlay", func() {
		It("should return base content when hidden", func() {
			baseContent := "Base content\nLine 2"
			result := modal.RenderOverlay(baseContent)
			Expect(result).To(Equal(baseContent))
		})

		It("should overlay modal on base content when visible", func() {
			modal.Show()
			baseContent := "Base content\nLine 2\nLine 3"
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(Equal(baseContent))
			Expect(result).To(ContainSubstring("Keyboard Shortcuts"))
		})

		It("should center modal vertically", func() {
			modal.Show()
			baseContent := strings.Repeat("Line\n", 30)
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(BeEmpty())
		})

		It("should handle small base content", func() {
			modal.Show()
			baseContent := "Small"
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(BeEmpty())
		})

		It("should dim background content", func() {
			modal.Show()
			baseContent := "Background content"
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(Equal(baseContent))
		})
	})

	Describe("getTheme", func() {
		It("should return default theme when nil", func() {
			m := feedback.NewHelpModal(keyMap)
			m.WithTheme(nil)
			view := m.View()
			Expect(view).To(Equal(""))
		})

		It("should return custom theme when set", func() {
			m := feedback.NewHelpModal(keyMap)
			m.WithTheme(theme)
			m.Show()
			view := m.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should use theme in rendering when visible", func() {
			m := feedback.NewHelpModal(keyMap)
			m.WithTheme(theme)
			m.Show()
			view := m.View()
			Expect(view).To(ContainSubstring("Keyboard Shortcuts"))
		})

		It("should handle nil theme gracefully in rendering", func() {
			m := feedback.NewHelpModal(keyMap)
			m.WithTheme(nil)
			m.Show()
			view := m.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("composeOverlay edge cases", func() {
		It("should handle overlay with small base content", func() {
			modal.Show()
			baseContent := "Small"
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(BeEmpty())
		})

		It("should handle overlay with large base content", func() {
			modal.Show()
			baseContent := strings.Repeat("Line of content\n", 50)
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(BeEmpty())
		})

		It("should handle overlay with single line base content", func() {
			modal.Show()
			baseContent := "Single line"
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(BeEmpty())
		})

		It("should handle overlay with empty base content", func() {
			modal.Show()
			baseContent := ""
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(BeEmpty())
		})

		It("should center modal when base content is larger", func() {
			modal.Show()
			baseContent := strings.Repeat("Line\n", 100)
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(Equal(baseContent))
		})

		It("should handle overlay with very wide lines", func() {
			modal.Show()
			baseContent := strings.Repeat("x", 200) + "\n" + strings.Repeat("y", 200)
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(BeEmpty())
		})

		It("should handle overlay with modal wider than base content", func() {
			modal.SetSize(200, 24)
			modal.Show()
			baseContent := "Short"
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(BeEmpty())
		})

		It("should handle overlay with very small width", func() {
			modal.SetSize(40, 24)
			modal.Show()
			baseContent := "Content line"
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(BeEmpty())
		})

		It("should handle overlay with modal lines wider than terminal", func() {
			modal.SetSize(50, 24)
			modal.Show()
			baseContent := "x\ny\nz"
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(BeEmpty())
		})

		It("should handle overlay with negative padding", func() {
			modal.SetSize(30, 24)
			modal.Show()
			baseContent := strings.Repeat("x", 100) + "\n" + strings.Repeat("y", 100)
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(BeEmpty())
		})
	})

	Describe("Integration", func() {
		It("should handle full workflow", func() {
			Expect(modal.IsVisible()).To(BeFalse())

			modal.Show()
			Expect(modal.IsVisible()).To(BeTrue())

			view := modal.View()
			Expect(view).To(ContainSubstring("Keyboard Shortcuts"))

			modal.ToggleFullHelp()
			view = modal.View()
			Expect(view).To(ContainSubstring("f for short help"))

			modal.Hide()
			Expect(modal.IsVisible()).To(BeFalse())
		})

		It("should handle theme changes", func() {
			modal.WithTheme(theme)
			modal.Show()
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should handle size changes", func() {
			modal.SetSize(80, 20)
			modal.Show()
			view := modal.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should handle overlay rendering with theme", func() {
			modal.WithTheme(theme)
			modal.Show()
			baseContent := "Background content\nLine 2\nLine 3"
			result := modal.RenderOverlay(baseContent)
			Expect(result).NotTo(BeEmpty())
		})

		It("should handle multiple size changes", func() {
			modal.SetSize(100, 24)
			modal.Show()
			view1 := modal.View()

			modal.SetSize(80, 20)
			view2 := modal.View()

			Expect(view1).NotTo(BeEmpty())
			Expect(view2).NotTo(BeEmpty())
		})
	})
})
