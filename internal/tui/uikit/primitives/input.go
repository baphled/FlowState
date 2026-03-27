package primitives

import (
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Input is a theme-aware text input component wrapping bubbles textinput.
// It provides a fluent API for configuration and themed rendering.
//
// Example:
//
//	input := primitives.NewInput(theme).
//	    Label("Email").
//	    Placeholder("user@example.com").
//	    Width(50)
//	input.Focus()
//	value := input.GetValue()
type Input struct {
	theme.Aware
	textInput   textinput.Model
	label       string
	errorMsg    string
	width       int
	labelStyle  lipgloss.Style
	errorStyle  lipgloss.Style
	borderStyle lipgloss.Style
}

// NewInput creates a new input component with the given theme.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Input ready for use.
//
// Side effects:
//   - None.
func NewInput(th theme.Theme) *Input {
	ti := textinput.New()
	ti.CharLimit = 256

	i := &Input{
		textInput: ti,
		label:     "",
		errorMsg:  "",
		width:     40,
	}

	if th != nil {
		i.SetTheme(th)
	}

	i.applyTheming()
	return i
}

// Label sets the label displayed above the input.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Input ready for use.
//
// Side effects:
//   - None.
func (i *Input) Label(label string) *Input {
	i.label = label
	return i
}

// Placeholder sets the placeholder text shown when input is empty.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Input ready for use.
//
// Side effects:
//   - None.
func (i *Input) Placeholder(placeholder string) *Input {
	i.textInput.Placeholder = placeholder
	return i
}

// Value sets the initial value of the input.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Input ready for use.
//
// Side effects:
//   - None.
func (i *Input) Value(value string) *Input {
	i.textInput.SetValue(value)
	return i
}

// Error sets an error message to display below the input.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Input ready for use.
//
// Side effects:
//   - None.
func (i *Input) Error(msg string) *Input {
	i.errorMsg = msg
	return i
}

// Width sets the width of the input field.
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A fully initialized Input ready for use.
//
// Side effects:
//   - None.
func (i *Input) Width(w int) *Input {
	i.width = w
	i.textInput.Width = w
	return i
}

// Focus returns a command to focus the input.
//
// Returns:
//   - A tea.Cmd value.
//
// Side effects:
//   - None.
func (i *Input) Focus() tea.Cmd {
	return i.textInput.Focus()
}

// Blur removes focus from the input.
//
// Side effects:
//   - None.
func (i *Input) Blur() {
	i.textInput.Blur()
}

// GetValue returns the current value of the input.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (i *Input) GetValue() string {
	return i.textInput.Value()
}

// Update handles input events and updates the internal state.
// Implements the tea.Model interface for Bubble Tea integration.
//
// Expected:
//   - msg must be a valid tea.Msg.
//
// Returns:
//   - Updated Input model and command.
//
// Side effects:
//   - Updates internal text input state.
func (i *Input) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	i.textInput, cmd = i.textInput.Update(msg)
	return i, cmd
}

// View returns the rendered input as a string.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (i *Input) View() string {
	return i.Render()
}

// Init implements tea.Model interface. Returns nil as no initialization is needed.
//
// Returns:
//   - A tea.Cmd value.
//
// Side effects:
//   - None.
func (i *Input) Init() tea.Cmd {
	return nil
}

// Render returns the styled input as a string.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (i *Input) Render() string {
	var parts []string

	if i.label != "" {
		labelText := i.labelStyle.Render(i.label)
		parts = append(parts, labelText)
	}

	inputView := i.textInput.View()

	borderColor := i.BorderColor()
	if i.textInput.Focused() {
		borderColor = i.AccentColor()
	}
	if i.errorMsg != "" {
		borderColor = i.ErrorColor()
	}

	borderedInput := i.borderStyle.
		BorderForeground(borderColor).
		Render(inputView)
	parts = append(parts, borderedInput)

	if i.errorMsg != "" {
		errorText := i.errorStyle.Render(i.errorMsg)
		parts = append(parts, errorText)
	}

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// applyTheming applies theme colors to the input styles.
//
// Side effects:
//   - Mutates i.labelStyle, i.errorStyle, and i.borderStyle fields.
func (i *Input) applyTheming() {
	i.labelStyle = lipgloss.NewStyle().
		Foreground(i.SecondaryColor()).
		Bold(true).
		MarginBottom(0)

	i.errorStyle = lipgloss.NewStyle().
		Foreground(i.ErrorColor()).
		MarginTop(0)

	i.borderStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)

	i.textInput.TextStyle = lipgloss.NewStyle().Foreground(i.Theme().ForegroundColor())
	i.textInput.PlaceholderStyle = lipgloss.NewStyle().Foreground(i.MutedColor())
	i.textInput.PromptStyle = lipgloss.NewStyle().Foreground(i.PrimaryColor())
	i.textInput.Cursor.Style = lipgloss.NewStyle().Foreground(i.AccentColor())
}
