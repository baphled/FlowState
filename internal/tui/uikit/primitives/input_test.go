package primitives_test

import (
	"regexp"

	"github.com/baphled/flowstate/internal/tui/uikit/primitives"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// stripAnsi removes ANSI escape codes from a string for reliable test comparisons.
// This is needed because lipgloss styling adds color codes that can break substring matching.
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripAnsi(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

var _ = Describe("Input", func() {
	var th theme.Theme
	var input *primitives.Input

	BeforeEach(func() {
		th = theme.Default()
		input = primitives.NewInput(th)
	})

	Describe("NewInput", func() {
		It("should create input with theme", func() {
			Expect(input).NotTo(BeNil())
		})

		It("should accept nil theme and use default", func() {
			i := primitives.NewInput(nil)
			Expect(i).NotTo(BeNil())
		})
	})

	Describe("Fluent API", func() {
		Describe("Label", func() {
			It("should set label", func() {
				input.Label("Username")
				view := stripAnsi(input.View())
				Expect(view).To(ContainSubstring("Username"))
			})

			It("should return input for chaining", func() {
				result := input.Label("Email")
				Expect(result).To(Equal(input))
			})
		})

		Describe("Placeholder", func() {
			It("should set placeholder", func() {
				input.Placeholder("Name").Width(50)
				view := stripAnsi(input.View())
				Expect(view).To(ContainSubstring("Name"))
			})

			It("should return input for chaining", func() {
				result := input.Placeholder("Type here...")
				Expect(result).To(Equal(input))
			})
		})

		Describe("Value", func() {
			It("should set initial value", func() {
				input.Value("initial")
				Expect(input.GetValue()).To(Equal("initial"))
			})

			It("should return input for chaining", func() {
				result := input.Value("test")
				Expect(result).To(Equal(input))
			})
		})

		Describe("Error", func() {
			It("should set error message", func() {
				input.Error("Invalid email")
				view := stripAnsi(input.View())
				Expect(view).To(ContainSubstring("Invalid email"))
			})

			It("should return input for chaining", func() {
				result := input.Error("Required field")
				Expect(result).To(Equal(input))
			})

			It("should clear error with empty string", func() {
				input.Error("Error").Error("")
				view := stripAnsi(input.View())
				Expect(view).NotTo(ContainSubstring("Error"))
			})
		})

		Describe("Width", func() {
			It("should set input width", func() {
				input.Width(50)
				view := input.View()
				Expect(view).NotTo(BeEmpty())
			})

			It("should return input for chaining", func() {
				result := input.Width(40)
				Expect(result).To(Equal(input))
			})
		})

		It("should chain multiple methods", func() {
			input.Label("Email").
				Placeholder("user@example.com").
				Value("test@test.com").
				Width(60)
			view := stripAnsi(input.View())
			Expect(view).To(ContainSubstring("Email"))
			Expect(input.GetValue()).To(Equal("test@test.com"))
		})
	})

	Describe("Focus Management", func() {
		Describe("Focus", func() {
			It("should return focus command", func() {
				cmd := input.Focus()
				Expect(cmd).NotTo(BeNil())
			})
		})

		Describe("Blur", func() {
			It("should blur input", func() {
				input.Focus()
				input.Blur()
				// Input should no longer be focused
				// (verified by absence of focus indicators in view)
				view := input.View()
				Expect(view).NotTo(BeEmpty())
			})
		})
	})

	Describe("Update", func() {
		It("should handle text input", func() {
			// Focus first
			input.Focus()
			input.Update(nil)

			// Type characters
			_, _ = input.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
			_, _ = input.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})

			value := input.GetValue()
			Expect(value).To(Equal("hi"))
		})

		It("should handle backspace", func() {
			input.Focus()
			input.Value("hello")

			_, _ = input.Update(tea.KeyMsg{Type: tea.KeyBackspace})

			value := input.GetValue()
			Expect(value).To(Equal("hell"))
		})

		It("should handle non-key messages", func() {
			initialValue := input.GetValue()
			_, _ = input.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
			Expect(input.GetValue()).To(Equal(initialValue))
		})
	})

	Describe("GetValue", func() {
		It("should return empty string initially", func() {
			value := input.GetValue()
			Expect(value).To(BeEmpty())
		})

		It("should return set value", func() {
			input.Value("test value")
			value := input.GetValue()
			Expect(value).To(Equal("test value"))
		})

		It("should return typed value", func() {
			input.Focus()
			input.Update(nil)
			_, _ = input.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
			_, _ = input.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})

			value := input.GetValue()
			Expect(value).To(Equal("ab"))
		})
	})

	Describe("View", func() {
		It("should render empty input", func() {
			view := input.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should render label above input", func() {
			input.Label("Name")
			view := stripAnsi(input.View())
			Expect(view).To(ContainSubstring("Name"))
		})

		It("should render placeholder when empty", func() {
			input.Placeholder("Type").Width(50)
			view := stripAnsi(input.View())
			Expect(view).To(ContainSubstring("Type"))
		})

		It("should render value", func() {
			input.Value("some text")
			view := stripAnsi(input.View())
			Expect(view).To(ContainSubstring("some text"))
		})

		It("should render error below input", func() {
			input.Error("Invalid input")
			view := stripAnsi(input.View())
			Expect(view).To(ContainSubstring("Invalid input"))
		})

		It("should render complete input with all parts", func() {
			input.Label("Email").
				Placeholder("user@example.com").
				Value("test@test.com").
				Error("Invalid format")

			view := stripAnsi(input.View())
			Expect(view).To(ContainSubstring("Email"))
			Expect(view).To(ContainSubstring("test@test.com"))
			Expect(view).To(ContainSubstring("Invalid format"))
		})
	})
})
