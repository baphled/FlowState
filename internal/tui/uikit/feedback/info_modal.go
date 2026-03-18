package feedback

import (
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/primitives"
	themes "github.com/baphled/flowstate/internal/tui/uikit/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// InfoModalVariant defines the visual style variant of the modal.
type InfoModalVariant int

const (
	// InfoModalInfo uses informational styling (teal/blue border).
	InfoModalInfo InfoModalVariant = iota
	// InfoModalWarning uses warning styling (amber/yellow border).
	InfoModalWarning
	// InfoModalSuccess uses success styling (green border).
	InfoModalSuccess
)

// InfoModal is a reusable informational modal for displaying messages to the user.
// Unlike ConfirmModal, it has a single action: dismiss. It does not ask for
// confirmation - it simply displays information and allows the user to acknowledge it.
//
// This is the UIKit version that uses the theme system from uikit/theme.
//
// Usage:
//
//	modal := feedback.NewInfoModal("Title", "Message explaining something...")
//	modal := feedback.NewWarningInfoModal("Warning Title", "Warning message...")
//
//	// In Update:
//	if modal.Update(msg) {
//	    // User dismissed it.
//	    modal = nil
//	}
//
//	// In View:
//	if modal != nil && modal.IsVisible() {
//	    return modal.View()
//	}
//
// Dismisses on: Enter, Space, Esc.
type InfoModal struct {
	title   string
	message string
	visible bool
	variant InfoModalVariant
	theme   themes.Theme
	width   int
	height  int
}

// NewInfoModal creates a new info modal with informational styling (teal/blue border).
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized InfoModal ready for use.
//
// Side effects:
//   - None.
func NewInfoModal(title, message string) *InfoModal {
	return &InfoModal{
		title:   title,
		message: message,
		visible: true,
		variant: InfoModalInfo,
		theme:   themes.Default(),
		width:   100,
		height:  24,
	}
}

// NewWarningInfoModal creates a new info modal with warning styling (amber/yellow border).
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized InfoModal ready for use.
//
// Side effects:
//   - None.
func NewWarningInfoModal(title, message string) *InfoModal {
	return &InfoModal{
		title:   title,
		message: message,
		visible: true,
		variant: InfoModalWarning,
		theme:   themes.Default(),
		width:   100,
		height:  24,
	}
}

// NewSuccessInfoModal creates a new info modal with success styling (green border).
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized InfoModal ready for use.
//
// Side effects:
//   - None.
func NewSuccessInfoModal(title, message string) *InfoModal {
	return &InfoModal{
		title:   title,
		message: message,
		visible: true,
		variant: InfoModalSuccess,
		theme:   themes.Default(),
		width:   100,
		height:  24,
	}
}

// Init initializes the modal (required by BubbleTea lifecycle).
//
// Returns:
//   - A tea.Cmd value.
//
// Side effects:
//   - None.
func (m *InfoModal) Init() tea.Cmd {
	return nil
}

// Update handles keyboard input for the info modal.
//
// Expected:
//   - msg must be valid.
//
// Returns:
//   - A bool value.
//
// Side effects:
//   - None.
func (m *InfoModal) Update(msg tea.Msg) bool {
	if !m.visible {
		return false
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return false

	case tea.KeyMsg:
		switch msg.String() {
		case "enter", " ", "esc":
			// User dismissed the modal
			m.visible = false
			return true
		}
	}

	return false
}

// View renders the info modal as a centered box.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (m *InfoModal) View() string {
	if !m.visible {
		return ""
	}

	theme := m.getTheme()

	// Get border color based on variant
	borderColor := m.getBorderColor()

	// Build footer with KeyBadge components
	footer := primitives.RenderHelpFooter(theme,
		primitives.HelpKeyBadge("Enter/Esc", "Close", theme),
	)

	// Build modal content
	var content strings.Builder

	// Title (styled based on variant)
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(borderColor).
		MarginBottom(1)
	content.WriteString(titleStyle.Render(m.title))
	content.WriteString("\n\n")

	// Message (wrapped to modal width)
	messageStyle := lipgloss.NewStyle().
		Width(50).
		MarginBottom(1)
	content.WriteString(messageStyle.Render(m.message))
	content.WriteString("\n\n")

	// Footer
	content.WriteString(footer)

	// Create modal box (centered, with border)
	modalWidth := 60
	if modalWidth > m.width-4 {
		modalWidth = m.width - 4
	}
	if modalWidth < 40 {
		modalWidth = 40
	}

	modalStyle := lipgloss.NewStyle().
		Width(modalWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Background(theme.BackgroundColor()).
		Padding(1, 2).
		Align(lipgloss.Center)

	modalBox := modalStyle.Render(content.String())

	return modalBox
}

// getTheme returns the theme or default if nil (nil-theme guard pattern).
//
// Returns:
//   - The assigned theme, or the default theme if none is set.
//
// Side effects:
//   - None.
func (m *InfoModal) getTheme() themes.Theme {
	if m.theme != nil {
		return m.theme
	}
	return themes.Default()
}

// getBorderColor returns the appropriate border color based on variant.
//
// Returns:
//   - A lipgloss.Color matching the info modal variant.
//
// Side effects:
//   - None.
func (m *InfoModal) getBorderColor() lipgloss.Color {
	theme := m.getTheme()

	switch m.variant {
	case InfoModalWarning:
		return theme.WarningColor()
	case InfoModalSuccess:
		return theme.SuccessColor()
	default:
		return theme.InfoColor()
	}
}

// IsVisible returns whether the modal is currently visible.
//
// Returns:
//   - A bool value.
//
// Side effects:
//   - None.
func (m *InfoModal) IsVisible() bool {
	return m.visible
}

// Show makes the modal visible.
//
// Side effects:
//   - None.
func (m *InfoModal) Show() {
	m.visible = true
}

// Hide hides the modal.
//
// Side effects:
//   - None.
func (m *InfoModal) Hide() {
	m.visible = false
}

// WithTheme sets the theme for the modal (useful for testing or custom themes).
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized InfoModal ready for use.
//
// Side effects:
//   - None.
func (m *InfoModal) WithTheme(theme themes.Theme) *InfoModal {
	m.theme = theme
	return m
}

// SetDimensions sets the terminal dimensions for responsive sizing.
//
// Expected:
//   - int must be valid.
//
// Side effects:
//   - None.
func (m *InfoModal) SetDimensions(width, height int) {
	m.width = width
	m.height = height
}

// GetTitle returns the modal title.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (m *InfoModal) GetTitle() string {
	return m.title
}

// GetMessage returns the modal message.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (m *InfoModal) GetMessage() string {
	return m.message
}

// GetVariant returns the modal variant.
//
// Returns:
//   - A InfoModalVariant value.
//
// Side effects:
//   - None.
func (m *InfoModal) GetVariant() InfoModalVariant {
	return m.variant
}
