package feedback

import (
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/containers"
	"github.com/baphled/flowstate/internal/tui/uikit/primitives"
	themes "github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// DetailModal is a generic scrollable content viewer modal.
// It displays pre-rendered content with a title, optional footer badges,
// and built-in viewport scrolling for long content.
//
// This is the UIKit replacement for domain-specific view modals like
// ViewEventDetailModal and ViewEventSkillsModal.
//
// Usage:
//
//	content := renderEventDetails(event) // Pre-rendered string
//	modal := feedback.NewDetailModal("Event Details", content).
//	    WithTheme(theme).
//	    WithFooterBadges(
//	        primitives.HelpKeyBadge("s", "Skills", theme),
//	        primitives.BackBadge(theme),
//	    )
//
//	// In Update:
//	model, cmd := modal.Update(msg)
//
//	// In View:
//	if modal.IsVisible() {
//	    return behaviors.RenderModalOverlay(modal, background)
//	}
type DetailModal struct {
	title        string
	content      string
	footerBadges []*primitives.Badge
	theme        themes.Theme
	visible      bool
	width        int
	height       int
	viewport     viewport.Model
	ready        bool
	hasContent   bool
}

// NewDetailModal creates a new detail modal with the given title and content.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized DetailModal ready for use.
//
// Side effects:
//   - None.
func NewDetailModal(title, content string) *DetailModal {
	return &DetailModal{
		title:        title,
		content:      content,
		footerBadges: nil,
		theme:        nil,
		visible:      true,
		width:        80,
		height:       24,
		ready:        false,
	}
}

// WithTheme sets the theme for the modal.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized DetailModal ready for use.
//
// Side effects:
//   - None.
func (m *DetailModal) WithTheme(theme themes.Theme) *DetailModal {
	m.theme = theme
	return m
}

// WithFooterBadges sets custom footer badges.
//
// Expected:
//   - badge must be valid.
//
// Returns:
//   - A fully initialized DetailModal ready for use.
//
// Side effects:
//   - None.
func (m *DetailModal) WithFooterBadges(badges ...*primitives.Badge) *DetailModal {
	m.footerBadges = badges
	return m
}

// getTheme returns the theme or default if nil (nil-theme guard pattern).
//
// Returns:
//   - The assigned theme, or the default theme if none is set.
//
// Side effects:
//   - None.
func (m *DetailModal) getTheme() themes.Theme {
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
func (m *DetailModal) Init() tea.Cmd {
	return nil
}

// Update handles keyboard input and scrolling.
//
// Expected:
//   - msg must be a valid tea.Msg type.
//
// Returns:
//   - Updated model.
//   - Command to execute.
//
// Side effects:
//   - May update viewport.
//   - May hide modal on close keys.
func (m *DetailModal) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if !m.visible {
		return m, nil
	}

	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = false
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "backspace", "enter", "q":
			m.Hide()
			return m, nil

		case "up", "k", "down", "j", "pgup", "pgdown", "ctrl+u", "ctrl+d":
			if m.ready && m.hasContent {
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		}
	}

	return m, nil
}

// View renders the modal content with solid background and scrolling.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (m *DetailModal) View() string {
	if !m.visible {
		return ""
	}

	theme := m.getTheme()

	maxModalHeight := 30
	terminalMaxHeight := int(float64(m.height) * 0.7)
	if terminalMaxHeight < maxModalHeight {
		maxModalHeight = terminalMaxHeight
	}
	if maxModalHeight < 10 {
		maxModalHeight = 10
	}

	modalWidth := m.width - 12
	if modalWidth < 60 {
		modalWidth = 60
	}
	if modalWidth > 80 {
		modalWidth = 80
	}

	contentLines := strings.Split(m.content, "\n")
	contentHeight := len(contentLines)

	viewportHeight := maxModalHeight - 8
	if viewportHeight < 5 {
		viewportHeight = 5
	}

	if !m.ready {
		vpWidth := modalWidth - 4
		if vpWidth < 10 {
			vpWidth = 10
		}
		m.viewport = viewport.New(vpWidth, viewportHeight)
		m.viewport.SetContent(m.content)
		m.hasContent = contentHeight > viewportHeight
		m.ready = true
	}

	titleRendered := primitives.Title(m.title, theme).Render()

	var scrollHint string
	if len(m.footerBadges) > 0 {
		badges := m.footerBadges
		if m.hasContent {
			percentScrolled := int(m.viewport.ScrollPercent() * 100)
			badges = append(badges, primitives.HelpKeyBadge("↑↓/jk", "Scroll", theme))
			badges = append([]*primitives.Badge{primitives.HelpKeyBadge(formatPercent(percentScrolled), "", theme)}, badges...)
		}
		scrollHint = primitives.RenderHelpFooter(theme, badges...)
	} else {
		badges := []*primitives.Badge{
			primitives.HelpKeyBadge("↑↓/jk", "Scroll", theme),
			primitives.HelpKeyBadge("Enter/Esc", "Close", theme),
		}
		if m.hasContent {
			percentScrolled := int(m.viewport.ScrollPercent() * 100)
			badges = append(badges, primitives.HelpKeyBadge(formatPercent(percentScrolled), "", theme))
		}
		scrollHint = primitives.RenderHelpFooter(theme, badges...)
	}

	modalContent := lipgloss.JoinVertical(lipgloss.Left, titleRendered, "", m.viewport.View(), "", scrollHint)

	return containers.NewBox(theme).
		Content(modalContent).
		Width(modalWidth).
		Height(maxModalHeight).
		Padding(2).
		Background(theme.BackgroundColor()).
		Render()
}

// formatPercent formats a percentage for display (0-100).
//
// Expected:
//   - percent is between 0 and 100.
//
// Returns:
//   - A formatted string like "[42%]".
//
// Side effects:
//   - None.
func formatPercent(percent int) string {
	return fmt.Sprintf("[%d%%]", percent)
}

// IsVisible returns whether the modal is currently visible.
//
// Returns:
//   - A bool value.
//
// Side effects:
//   - None.
func (m *DetailModal) IsVisible() bool {
	return m.visible
}

// Show makes the modal visible and resets the viewport.
//
// Side effects:
//   - None.
func (m *DetailModal) Show() {
	m.visible = true
	m.ready = false
}

// Hide hides the modal.
//
// Side effects:
//   - None.
func (m *DetailModal) Hide() {
	m.visible = false
}

// SetDimensions sets the terminal dimensions for responsive sizing.
//
// Expected:
//   - int must be valid.
//
// Side effects:
//   - None.
func (m *DetailModal) SetDimensions(width, height int) {
	m.width = width
	m.height = height
	m.ready = false
}

// SetContent updates the content being displayed.
//
// Expected:
//   - Must be a valid string.
//
// Side effects:
//   - None.
func (m *DetailModal) SetContent(content string) {
	m.content = content
	m.ready = false
}

// SetTitle updates the title being displayed.
//
// Expected:
//   - Must be a valid string.
//
// Side effects:
//   - None.
func (m *DetailModal) SetTitle(title string) {
	m.title = title
}
