package containers

import (
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/charmbracelet/lipgloss"
)

// Overlay provides centered modal overlay with optional background dimming.
//
// Usage:
//
//	overlay := containers.NewOverlay(80, 24).
//	    Content("Modal content here").
//	    Dimmed()
//	rendered := overlay.Render()
type Overlay struct {
	theme.Aware

	width   int
	height  int
	content string
	dimmed  bool
	dimChar rune
}

// NewOverlay creates a new overlay with the given dimensions.
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A fully initialized Overlay ready for use.
//
// Side effects:
//   - None.
func NewOverlay(width, height int) *Overlay {
	overlay := &Overlay{
		width:   width,
		height:  height,
		dimmed:  false,
		dimChar: ' ', // Default to space for clean modal backgrounds
	}
	overlay.SetTheme(theme.Default())
	return overlay
}

// Content sets the content to display in the center.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Overlay ready for use.
//
// Side effects:
//   - None.
func (o *Overlay) Content(content string) *Overlay {
	o.content = content
	return o
}

// Dimmed enables background dimming.
//
// Returns:
//   - A fully initialized Overlay ready for use.
//
// Side effects:
//   - None.
func (o *Overlay) Dimmed() *Overlay {
	o.dimmed = true
	return o
}

// DimmedWith sets a custom dim character.
//
// Expected:
//   - rune must be valid.
//
// Returns:
//   - A fully initialized Overlay ready for use.
//
// Side effects:
//   - None.
func (o *Overlay) DimmedWith(char rune) *Overlay {
	o.dimChar = char
	o.dimmed = true // Automatically enable dimming when custom char is set
	return o
}

// Render returns the rendered overlay as a string.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (o *Overlay) Render() string {
	// Create background if dimmed
	var background string
	if o.dimmed {
		// Fill with dim characters
		dimLine := strings.Repeat(string(o.dimChar), o.width)
		dimLines := make([]string, o.height)
		for i := range dimLines {
			dimLines[i] = dimLine
		}
		background = strings.Join(dimLines, "\n")

		// Style the background with muted color
		backgroundStyle := lipgloss.NewStyle().
			Foreground(o.MutedColor())
		background = backgroundStyle.Render(background)
	} else {
		// Empty background
		emptyLine := strings.Repeat(" ", o.width)
		emptyLines := make([]string, o.height)
		for i := range emptyLines {
			emptyLines[i] = emptyLine
		}
		background = strings.Join(emptyLines, "\n")
	}

	// If no content, just return background
	if o.content == "" {
		return background
	}

	// Place content in center using lipgloss.Place
	centered := lipgloss.Place(
		o.width,
		o.height,
		lipgloss.Center,
		lipgloss.Center,
		o.content,
		lipgloss.WithWhitespaceChars(string(o.dimChar)),
		lipgloss.WithWhitespaceForeground(o.MutedColor()),
	)

	return centered
}
