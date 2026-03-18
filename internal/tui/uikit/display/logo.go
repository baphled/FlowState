// Package display provides display components for the UIKit.
// These components handle visual elements like logos, tables, and lists.
package display

import (
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/ui/themes"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Logo represents the KaRiya ASCII logo with optional animation.
// It supports fade-in animation and theme-aware styling.
//
// Usage:
//
//	logo := display.NewLogo(true, 80).
//	    WithTheme(theme).
//	    WithTagline("Career Event Management System").
//	    WithVersion("v1.0.0")
//	rendered := logo.View()
type Logo struct {
	animated     bool
	fadeProgress float64
	width        int
	tagline      string
	showTagline  bool
	version      string
	showVersion  bool
	theme        themes.Theme
}

const (
	// Bold ASCII art logo for KaRiya.
	logoArt = `██╗  ██╗ █████╗ ██████╗ ██╗██╗   ██╗ █████╗ 
██║ ██╔╝██╔══██╗██╔══██╗██║╚██╗ ██╔╝██╔══██╗
█████╔╝ ███████║██████╔╝██║ ╚████╔╝ ███████║
██╔═██╗ ██╔══██║██╔══██╗██║  ╚██╔╝  ██╔══██║
██║  ██╗██║  ██║██║  ██║██║   ██║   ██║  ██║
╚═╝  ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝╚═╝   ╚═╝   ╚═╝  ╚═╝`

	// Animation settings.
	frameInterval = 30 * time.Millisecond

	// LogoArtHeight is the number of lines in the ASCII logo art.
	LogoArtHeight = 6

	// DefaultLogoHeight is the typical rendered height of the logo
	// including tagline and version (6 art + 1 blank + 1 tagline + 1 version).
	// Use this constant for modal positioning calculations when the logo
	// configuration is unknown. This avoids magic numbers scattered across
	// the codebase and provides a single source of truth.
	DefaultLogoHeight = 9
)

// TickMsg is sent periodically to update the animation.
type TickMsg time.Time

// NewLogo creates a new ASCII logo component.
//
// Expected:
//   - bool must be valid.
//   - int must be valid.
//
// Returns:
//   - A fully initialized Logo ready for use.
//
// Side effects:
//   - None.
func NewLogo(animated bool, width int) *Logo {
	return &Logo{
		animated:     animated,
		fadeProgress: 0.0,
		width:        width,
		tagline:      "Career Event Management System",
		showTagline:  true,
		version:      "dev",
		showVersion:  true,
	}
}

// WithTheme sets the theme for the logo.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Logo ready for use.
//
// Side effects:
//   - None.
func (l *Logo) WithTheme(theme themes.Theme) *Logo {
	l.theme = theme
	return l
}

// WithTagline sets the tagline text.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Logo ready for use.
//
// Side effects:
//   - None.
func (l *Logo) WithTagline(tagline string) *Logo {
	l.tagline = tagline
	return l
}

// WithVersion sets the version text.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Logo ready for use.
//
// Side effects:
//   - None.
func (l *Logo) WithVersion(version string) *Logo {
	l.version = version
	return l
}

// SetWidth sets the width for centering calculations.
//
// Expected:
//   - int must be valid.
//
// Side effects:
//   - None.
func (l *Logo) SetWidth(width int) {
	l.width = width
}

// ShowTagline controls tagline visibility.
//
// Expected:
//   - bool must be valid.
//
// Side effects:
//   - None.
func (l *Logo) ShowTagline(show bool) {
	l.showTagline = show
}

// ShowVersion controls version visibility.
//
// Expected:
//   - bool must be valid.
//
// Side effects:
//   - None.
func (l *Logo) ShowVersion(show bool) {
	l.showVersion = show
}

// getTheme returns the theme or a default.
//
// Returns:
//   - The assigned theme, or a default theme if none is set.
//
// Side effects:
//   - None.
func (l *Logo) getTheme() themes.Theme {
	if l.theme != nil {
		return l.theme
	}
	return themes.NewDefaultTheme()
}

// Init initializes the logo component.
//
// Returns:
//   - A tea.Cmd value.
//
// Side effects:
//   - None.
func (l *Logo) Init() tea.Cmd {
	if l.animated {
		return l.tick()
	}
	l.fadeProgress = 1.0
	return nil
}

// Update handles animation updates.
//
// Expected:
//   - msg must be a valid tea.Msg type.
//
// Returns:
//   - Updated model.
//   - Command to execute.
//
// Side effects:
//   - Updates fade progress if animated.
func (l *Logo) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(TickMsg); ok {
		if l.animated && l.fadeProgress < 1.0 {
			l.fadeProgress += 0.1
			if l.fadeProgress < 1.0 {
				cmd := l.tick()
				return l, cmd
			}
			l.fadeProgress = 1.0
		}
	}
	return l, nil
}

// tick returns a command that sends a TickMsg after the frame interval.
//
// Returns:
//   - A tea.Cmd that schedules a TickMsg.
//
// Side effects:
//   - None.
func (l *Logo) tick() tea.Cmd {
	return tea.Tick(frameInterval, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// View renders the logo.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (l *Logo) View() string {
	return l.render()
}

// ViewStatic renders the logo without animation (instant display).
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (l *Logo) ViewStatic() string {
	savedProgress := l.fadeProgress
	l.fadeProgress = 1.0
	result := l.render()
	l.fadeProgress = savedProgress
	return result
}

// render generates the logo output with current fade progress.
//
// Returns:
//   - A rendered string with the logo art and optional version.
//
// Side effects:
//   - None.
func (l *Logo) render() string {
	theme := l.getTheme()
	var parts []string

	// Render the ASCII art with fade effect
	logoLines := strings.Split(logoArt, "\n")
	styledLines := make([]string, len(logoLines))

	for i, line := range logoLines {
		styledLine := l.applyFadeStyle(line, theme)
		styledLines[i] = styledLine
	}

	logoRendered := strings.Join(styledLines, "\n")
	parts = append(parts, logoRendered)

	// Add tagline if enabled
	if l.showTagline && l.tagline != "" {
		taglineStyle := lipgloss.NewStyle().
			Foreground(theme.SecondaryColor()).
			Faint(l.fadeProgress < 1.0)

		taglineText := taglineStyle.Render(l.tagline)
		parts = append(parts, "", taglineText)
	}

	// Add version if enabled
	if l.showVersion && l.version != "" {
		versionStyle := lipgloss.NewStyle().
			Foreground(theme.MutedColor()).
			Italic(true).
			Faint(l.fadeProgress < 1.0)

		versionText := versionStyle.Render(l.version)
		parts = append(parts, versionText)
	}

	return strings.Join(parts, "\n")
}

// applyFadeStyle applies the fade effect based on current progress.
//
// Expected:
//   - theme must be a valid theme instance.
//
// Returns:
//   - The text rendered with the current fade opacity.
//
// Side effects:
//   - None.
func (l *Logo) applyFadeStyle(text string, theme themes.Theme) string {
	if l.fadeProgress >= 1.0 {
		// Full opacity - use primary accent color
		return lipgloss.NewStyle().
			Foreground(theme.AccentColor()).
			Bold(true).
			Render(text)
	}

	// Fading in - adjust opacity by making it faint
	return lipgloss.NewStyle().
		Foreground(theme.AccentColor()).
		Bold(true).
		Faint(true).
		Render(text)
}

// GetHeight returns the height of the logo in lines.
//
// Returns:
//   - A int value.
//
// Side effects:
//   - None.
func (l *Logo) GetHeight() int {
	height := LogoArtHeight

	if l.showTagline {
		height += 2
	}

	if l.showVersion {
		height += 1
	}

	return height
}

// GetWidth returns the width of the logo.
//
// Returns:
//   - A int value.
//
// Side effects:
//   - None.
func (l *Logo) GetWidth() int {
	// The logo art is 51 characters wide
	return 51
}
