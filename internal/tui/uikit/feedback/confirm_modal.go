package feedback

import (
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/containers"
	"github.com/baphled/flowstate/internal/tui/uikit/primitives"
	themes "github.com/baphled/flowstate/internal/tui/uikit/theme"
	tea "github.com/charmbracelet/bubbletea"
)

// ConfirmVariant defines the visual style of the confirmation modal.
type ConfirmVariant int

const (
	// ConfirmDefault is the standard confirmation style (primary border).
	ConfirmDefault ConfirmVariant = iota
	// ConfirmDestructive uses error styling (red border) for delete operations.
	ConfirmDestructive
	// ConfirmWarning uses warning styling (yellow border) for caution.
	ConfirmWarning
)

// ConfirmModal is a generic confirmation modal that prompts the user
// to confirm or cancel an action using y/n or Enter/Esc keys.
//
// This is the UIKit replacement for components.DeleteConfirmModal,
// providing a reusable, theme-aware confirmation dialog.
//
// Usage:
//
//	modal := feedback.NewConfirmModal("Delete Event", "Are you sure?").
//	    WithVariant(feedback.ConfirmDestructive).
//	    WithTheme(theme)
//
//	// In Update:
//	cmd, confirmed := modal.Update(msg)
//	if confirmed {
//	    // User confirmed action
//	} else if !modal.IsVisible() {
//	    // User cancelled
//	}
type ConfirmModal struct {
	title     string
	message   string
	variant   ConfirmVariant
	theme     themes.Theme
	visible   bool
	confirmed bool
	width     int
	height    int
}

// NewConfirmModal creates a new confirmation modal with the given title and message.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized ConfirmModal ready for use.
//
// Side effects:
//   - None.
func NewConfirmModal(title, message string) *ConfirmModal {
	return &ConfirmModal{
		title:     title,
		message:   message,
		variant:   ConfirmDefault,
		theme:     nil, // Will use default theme in getTheme()
		visible:   true,
		confirmed: false,
		width:     100,
		height:    24,
	}
}

// WithVariant sets the visual variant of the confirmation modal.
//
// Expected:
//   - confirmvariant must be valid.
//
// Returns:
//   - A fully initialized ConfirmModal ready for use.
//
// Side effects:
//   - None.
func (m *ConfirmModal) WithVariant(variant ConfirmVariant) *ConfirmModal {
	m.variant = variant
	return m
}

// WithTheme sets the theme for the modal.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized ConfirmModal ready for use.
//
// Side effects:
//   - None.
func (m *ConfirmModal) WithTheme(theme themes.Theme) *ConfirmModal {
	m.theme = theme
	return m
}

// getTheme returns the theme or default if nil (nil-theme guard pattern).
//
// Returns:
//   - The assigned theme, or the default theme if none is set.
//
// Side effects:
//   - None.
func (m *ConfirmModal) getTheme() themes.Theme {
	if m.theme != nil {
		return m.theme
	}
	return themes.Default()
}

// Init initializes the modal (required by BubbleTea lifecycle).
//
// Returns:
//   - A tea.Cmd value.
//
// Side effects:
//   - None.
func (m *ConfirmModal) Init() tea.Cmd {
	return nil
}

// Update handles keyboard input for the confirmation modal.
//
// Expected:
//   - msg must be a valid tea.Msg type.
//
// Returns:
//   - tea.Cmd: command to execute (usually nil)
//   - bool: true if user confirmed, false otherwise
//
// Side effects:
//   - May close modal and set confirmed state.
func (m *ConfirmModal) Update(msg tea.Msg) (tea.Cmd, bool) {
	if !m.visible {
		return nil, false
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return nil, false

	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y", "enter":
			m.confirmed = true
			m.visible = false
			return nil, true

		case "n", "N", "esc":
			m.confirmed = false
			m.visible = false
			return nil, false
		}
	}

	return nil, false
}

// View renders the confirmation modal as a centered box.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (m *ConfirmModal) View() string {
	if !m.visible {
		return ""
	}

	theme := m.getTheme()

	footer := primitives.RenderHelpFooter(theme,
		primitives.HelpKeyBadge("y/Enter", "Confirm", theme),
		primitives.HelpKeyBadge("n/Esc", "Cancel", theme),
	)

	var content strings.Builder

	titleText := m.renderTitle(theme)
	content.WriteString(titleText)
	content.WriteString("\n\n")

	content.WriteString(primitives.Body(m.message, theme).Width(50).MarginBottom(1).Render())
	content.WriteString("\n\n")

	content.WriteString(footer)

	modalWidth := 60
	if modalWidth > m.width-4 {
		modalWidth = m.width - 4
	}
	if modalWidth < 40 {
		modalWidth = 40
	}

	boxVariant := m.getBoxVariant()
	return containers.NewBox(theme).
		Content(content.String()).
		Variant(boxVariant).
		Width(modalWidth).
		Padding(2).
		Background(theme.BackgroundColor()).
		Render()
}

// renderTitle renders the title with appropriate styling for the variant.
//
// Expected:
//   - theme must be a valid theme instance.
//
// Returns:
//   - A styled title string matching the confirmation variant.
//
// Side effects:
//   - None.
func (m *ConfirmModal) renderTitle(theme themes.Theme) string {
	switch m.variant {
	case ConfirmDestructive:
		return primitives.ErrorText(m.title, theme).Bold().MarginBottom(1).Render()
	case ConfirmWarning:
		return primitives.WarningText(m.title, theme).Bold().MarginBottom(1).Render()
	default:
		return primitives.Title(m.title, theme).MarginBottom(1).Render()
	}
}

// getBoxVariant returns the container box variant for this confirmation variant.
//
// Returns:
//   - A containers.BoxVariant matching the confirmation modal variant.
//
// Side effects:
//   - None.
func (m *ConfirmModal) getBoxVariant() containers.BoxVariant {
	switch m.variant {
	case ConfirmDestructive:
		return containers.BoxDestructive
	case ConfirmWarning:
		return containers.BoxWarning
	default:
		return containers.BoxDefault
	}
}

// IsVisible returns whether the modal is currently visible.
//
// Returns:
//   - A bool value.
//
// Side effects:
//   - None.
func (m *ConfirmModal) IsVisible() bool {
	return m.visible
}

// WasConfirmed returns whether the user confirmed the action.
//
// Returns:
//   - A bool value.
//
// Side effects:
//   - None.
func (m *ConfirmModal) WasConfirmed() bool {
	return m.confirmed
}

// Show makes the modal visible and resets the confirmed state.
//
// Side effects:
//   - None.
func (m *ConfirmModal) Show() {
	m.visible = true
	m.confirmed = false
}

// Hide hides the modal without confirming.
//
// Side effects:
//   - None.
func (m *ConfirmModal) Hide() {
	m.visible = false
	m.confirmed = false
}

// SetDimensions sets the terminal dimensions for responsive sizing.
//
// Expected:
//   - int must be valid.
//
// Side effects:
//   - None.
func (m *ConfirmModal) SetDimensions(width, height int) {
	m.width = width
	m.height = height
}
