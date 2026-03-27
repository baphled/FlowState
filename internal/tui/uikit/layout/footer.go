package layout

import (
	"strings"

	"github.com/baphled/flowstate/internal/ui/themes"
	"github.com/charmbracelet/lipgloss"
)

// Footer renders a consistent screen footer with status, mode, and help text.
// It supports theme-aware styling and optional help footer integration.
//
// Usage:
//
//	footer := layout.NewFooter(80).
//	    WithTheme(theme).
//	    WithStatus("3/10 events").
//	    WithMode("Capture Mode: Timeline")
//	rendered := footer.View()
type Footer struct {
	statusMessage string
	modeContext   string // Current mode/context (e.g., "Capture Mode: Timeline")
	width         int
	height        int
	showStatus    bool
	showMode      bool
	showHelp      bool
	helpText      string // Simple help text (alternative to HelpFooter)
	theme         themes.Theme
}

// NewFooter creates a new footer with width.
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A fully initialized Footer ready for use.
//
// Side effects:
//   - None.
func NewFooter(width int) *Footer {
	return &Footer{
		statusMessage: "",
		modeContext:   "",
		width:         width,
		height:        1,
		showStatus:    true,
		showMode:      true,
		showHelp:      false,
		helpText:      "",
	}
}

// WithTheme sets the theme for the footer.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Footer ready for use.
//
// Side effects:
//   - None.
func (f *Footer) WithTheme(theme themes.Theme) *Footer {
	f.theme = theme
	return f
}

// WithStatus sets the status message.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Footer ready for use.
//
// Side effects:
//   - None.
func (f *Footer) WithStatus(message string) *Footer {
	f.statusMessage = message
	return f
}

// WithMode sets the mode/context string.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Footer ready for use.
//
// Side effects:
//   - None.
func (f *Footer) WithMode(context string) *Footer {
	f.modeContext = context
	return f
}

// WithHelp sets the help text.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Footer ready for use.
//
// Side effects:
//   - None.
func (f *Footer) WithHelp(helpText string) *Footer {
	f.helpText = helpText
	f.showHelp = true
	return f
}

// SetWidth sets the footer width.
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A fully initialized Footer ready for use.
//
// Side effects:
//   - None.
func (f *Footer) SetWidth(width int) *Footer {
	f.width = width
	return f
}

// SetHeight sets the footer height.
//
// Expected:
//   - int must be valid.
//
// Side effects:
//   - None.
func (f *Footer) SetHeight(height int) {
	f.height = height
}

// SetShowStatus sets whether to display the status message.
//
// Expected:
//   - bool must be valid.
//
// Side effects:
//   - None.
func (f *Footer) SetShowStatus(show bool) {
	f.showStatus = show
}

// SetShowMode sets whether to display the mode context.
//
// Expected:
//   - bool must be valid.
//
// Side effects:
//   - None.
func (f *Footer) SetShowMode(show bool) {
	f.showMode = show
}

// SetShowHelp sets whether to display the help text.
//
// Expected:
//   - bool must be valid.
//
// Side effects:
//   - None.
func (f *Footer) SetShowHelp(show bool) {
	f.showHelp = show
}

// GetStatusMessage returns the current status message.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (f *Footer) GetStatusMessage() string {
	return f.statusMessage
}

// GetModeContext returns the current mode context.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (f *Footer) GetModeContext() string {
	return f.modeContext
}

// getTheme returns the theme or a default.
//
// Returns:
//   - The assigned theme, or a default theme if none is set.
//
// Side effects:
//   - None.
func (f *Footer) getTheme() themes.Theme {
	if f.theme != nil {
		return f.theme
	}
	return themes.NewDefaultTheme()
}

// View renders the footer.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (f *Footer) View() string {
	if f.width <= 0 {
		return ""
	}

	theme := f.getTheme()
	var parts []string

	if f.showStatus && f.statusMessage != "" && f.showMode && f.modeContext != "" {
		statusModeLine := f.renderStatusAndMode(theme)
		parts = append(parts, statusModeLine)
	} else {
		if f.showStatus && f.statusMessage != "" {
			parts = append(parts, f.renderStatus(theme))
		}
		if f.showMode && f.modeContext != "" {
			parts = append(parts, f.renderMode(theme))
		}
	}

	if f.showHelp && f.helpText != "" {
		helpView := f.renderHelp(theme)
		if helpView != "" {
			parts = append(parts, helpView)
		}
	}

	return strings.Join(parts, "\n")
}

// renderStatusAndMode renders status and mode on the same line.
//
// Expected:
//   - theme must be a valid theme instance.
//
// Returns:
//   - A formatted string with status and mode separated by a pipe.
//
// Side effects:
//   - None.
func (f *Footer) renderStatusAndMode(theme themes.Theme) string {
	statusStyle := lipgloss.NewStyle().
		Foreground(theme.SecondaryColor())

	modeStyle := lipgloss.NewStyle().
		Foreground(theme.MutedColor()).
		Italic(true)

	separatorStyle := lipgloss.NewStyle().
		Foreground(theme.MutedColor()).
		Italic(true)

	status := statusStyle.Render(f.statusMessage)
	mode := modeStyle.Render(f.modeContext)
	separator := separatorStyle.Render("  |  ")

	line := status + separator + mode

	if lipgloss.Width(line) > f.width {
		return status
	}

	return line
}

// renderStatus renders just the status message.
//
// Expected:
//   - theme must be a valid theme instance.
//
// Returns:
//   - A styled status message string, truncated if needed.
//
// Side effects:
//   - None.
func (f *Footer) renderStatus(theme themes.Theme) string {
	statusStyle := lipgloss.NewStyle().
		Foreground(theme.SecondaryColor())

	status := f.statusMessage
	if len(status) > f.width {
		status = status[:f.width-3] + "..."
	}

	return statusStyle.Render(status)
}

// renderMode renders just the mode context.
//
// Expected:
//   - theme must be a valid theme instance.
//
// Returns:
//   - A styled mode context string, truncated if needed.
//
// Side effects:
//   - None.
func (f *Footer) renderMode(theme themes.Theme) string {
	modeStyle := lipgloss.NewStyle().
		Foreground(theme.MutedColor()).
		Italic(true)

	mode := f.modeContext
	if len(mode) > f.width {
		mode = mode[:f.width-3] + "..."
	}

	return modeStyle.Render(mode)
}

// renderHelp renders the help text.
//
// Expected:
//   - theme must be a valid theme instance.
//
// Returns:
//   - A styled help text string.
//
// Side effects:
//   - None.
func (f *Footer) renderHelp(theme themes.Theme) string {
	helpStyle := lipgloss.NewStyle().
		Foreground(theme.MutedColor())

	return helpStyle.Render(f.helpText)
}
