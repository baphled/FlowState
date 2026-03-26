package feedback

import (
	"strings"

	"github.com/baphled/flowstate/internal/ui/themes"
	"github.com/charmbracelet/lipgloss"
)

// ModalContainer renders a modal dialog with title, message, buttons, and instructions.
// It provides consistent modal layout with support for destructive actions and
// centered rendering.
// ModalContainer is a stateless rendering component.
//
// This is the UIKit version that uses theme-based styling exclusively.
type ModalContainer struct {
	title           string
	message         string
	buttons         []string
	instructions    string
	isDestructive   bool
	hasTitle        bool
	hasMessage      bool
	hasButtons      bool
	hasInstructions bool
	theme           themes.Theme
	width           int
	showScrollHint  bool
}

// NewModalContainer creates a new ModalContainer.
//
// Returns:
//   - A fully initialized ModalContainer ready for use.
//
// Side effects:
//   - None.
func NewModalContainer() *ModalContainer {
	return &ModalContainer{
		isDestructive:   false,
		hasTitle:        false,
		hasMessage:      false,
		hasButtons:      false,
		hasInstructions: false,
		theme:           themes.NewDefaultTheme(),
	}
}

// getTheme returns the theme or default if nil.
//
// Returns:
//   - The assigned theme, or a default theme if none is set.
//
// Side effects:
//   - None.
func (mc *ModalContainer) getTheme() themes.Theme {
	if mc.theme != nil {
		return mc.theme
	}
	return themes.NewDefaultTheme()
}

// SetTitle sets the title for the modal.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized ModalContainer ready for use.
//
// Side effects:
//   - None.
func (mc *ModalContainer) SetTitle(title string) *ModalContainer {
	mc.title = title
	mc.hasTitle = true
	return mc
}

// SetMessage sets the message content for the modal.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized ModalContainer ready for use.
//
// Side effects:
//   - None.
func (mc *ModalContainer) SetMessage(message string) *ModalContainer {
	mc.message = message
	mc.hasMessage = true
	return mc
}

// SetButtons sets the button labels for the modal.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized ModalContainer ready for use.
//
// Side effects:
//   - None.
func (mc *ModalContainer) SetButtons(buttons []string) *ModalContainer {
	mc.buttons = buttons
	mc.hasButtons = true
	return mc
}

// SetInstructions sets the instruction text for the modal.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized ModalContainer ready for use.
//
// Side effects:
//   - None.
func (mc *ModalContainer) SetInstructions(instructions string) *ModalContainer {
	mc.instructions = instructions
	mc.hasInstructions = true
	return mc
}

// WithDestructiveStyle marks the modal as destructive (e.g., for delete confirmations).
//
// Returns:
//   - A fully initialized ModalContainer ready for use.
//
// Side effects:
//   - None.
func (mc *ModalContainer) WithDestructiveStyle() *ModalContainer {
	mc.isDestructive = true
	return mc
}

// WithTheme sets the theme for the modal.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized ModalContainer ready for use.
//
// Side effects:
//   - None.
func (mc *ModalContainer) WithTheme(theme themes.Theme) *ModalContainer {
	mc.theme = theme
	return mc
}

// WithWidth sets the width for the modal.
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A fully initialized ModalContainer ready for use.
//
// Side effects:
//   - None.
func (mc *ModalContainer) WithWidth(width int) *ModalContainer {
	mc.width = width
	return mc
}

// WithScrollHint enables the scroll indicator hint.
//
// Expected:
//   - bool must be valid.
//
// Returns:
//   - A fully initialized ModalContainer ready for use.
//
// Side effects:
//   - None.
func (mc *ModalContainer) WithScrollHint(show bool) *ModalContainer {
	mc.showScrollHint = show
	return mc
}

// Render returns the styled modal container as a string.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (mc *ModalContainer) Render() string {
	theme := mc.getTheme()
	var parts []string

	// Determine styles based on destructive flag
	var titleStyle, messageStyle, buttonStyle, instructionStyle lipgloss.Style

	if mc.isDestructive {
		titleStyle = lipgloss.NewStyle().
			Foreground(theme.ErrorColor()).
			Bold(true).
			MarginBottom(1)
		messageStyle = lipgloss.NewStyle().
			Foreground(theme.ForegroundColor()).
			MarginBottom(2)
		buttonStyle = lipgloss.NewStyle().
			Foreground(theme.ErrorColor()).
			Bold(true)
	} else {
		titleStyle = lipgloss.NewStyle().
			Foreground(theme.ForegroundColor()).
			Bold(true).
			MarginBottom(1)
		messageStyle = lipgloss.NewStyle().
			Foreground(theme.ForegroundColor()).
			MarginBottom(2)
		buttonStyle = lipgloss.NewStyle().
			Foreground(theme.PrimaryColor()).
			Bold(true)
	}

	instructionStyle = lipgloss.NewStyle().
		Foreground(theme.MutedColor()).
		MarginTop(1)

	buttonContainerStyle := lipgloss.NewStyle().
		MarginTop(2).
		MarginBottom(1)

	// Render title if present
	if mc.hasTitle {
		parts = append(parts, titleStyle.Render(mc.title))
	}

	// Render message if present
	if mc.hasMessage {
		parts = append(parts, messageStyle.Render(mc.message))
	}

	// Render buttons if present
	if mc.hasButtons && len(mc.buttons) > 0 {
		buttonTexts := []string{}
		for _, btn := range mc.buttons {
			buttonTexts = append(buttonTexts, buttonStyle.Render(btn))
		}
		buttonContainer := buttonContainerStyle.
			Render(strings.Join(buttonTexts, " "))
		parts = append(parts, buttonContainer)
	}

	// Render instructions if present
	if mc.hasInstructions {
		parts = append(parts, instructionStyle.Render(mc.instructions))
	}

	// Add scroll hint if enabled
	if mc.showScrollHint {
		scrollHint := lipgloss.NewStyle().
			Foreground(theme.MutedColor()).
			Italic(true).
			Render("↑↓ Scroll")
		parts = append(parts, scrollHint)
	}

	// Combine all parts
	content := strings.Join(parts, "\n")

	// Apply modal styling with border and background
	var modalStyle lipgloss.Style
	if mc.isDestructive {
		modalStyle = lipgloss.NewStyle().
			Padding(1, 2).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(theme.ErrorColor()).
			Background(theme.BackgroundColor()).
			Foreground(theme.ForegroundColor())
	} else {
		modalStyle = lipgloss.NewStyle().
			Padding(1, 2).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(theme.BorderColor()).
			Background(theme.BackgroundColor()).
			Foreground(theme.ForegroundColor())
	}

	// Apply width if specified
	if mc.width > 0 {
		modalStyle = modalStyle.Width(mc.width)
	}

	return modalStyle.Render(content)
}
