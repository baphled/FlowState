package themes

import (
	"github.com/charmbracelet/lipgloss"
)

// DefaultPalette contains the default KaRiya color scheme.
// These colors match the existing styles in internal/cli/styles/styles.go
// to ensure backwards compatibility.
var DefaultPalette = ColorPalette{
	// Background colors - from styles.ColorBackground*
	Background:     lipgloss.Color("#1a1f2e"), // Dark blue-gray background
	BackgroundAlt:  lipgloss.Color("#242936"), // Slightly lighter for contrast
	BackgroundCard: lipgloss.Color("#2d3346"), // Card/panel background

	// Foreground colors - from styles.ColorText*
	Foreground:      lipgloss.Color("#c7ccd1"), // Primary text (light gray)
	ForegroundDim:   lipgloss.Color("#8b92a0"), // Secondary text (medium gray)
	ForegroundMuted: lipgloss.Color("#5e6673"), // Muted text (dark gray)

	// Accent colors - from styles.ColorAccent*
	Primary:   lipgloss.Color("#5fb3b3"), // Muted teal for primary actions
	Secondary: lipgloss.Color("#6cb56c"), // Muted green for success/confirmation
	Tertiary:  lipgloss.Color("#a99bd1"), // Muted purple for selected items

	// Status colors - from styles.Color*
	Success: lipgloss.Color("#6cb56c"), // Success green (same as accent)
	Warning: lipgloss.Color("#d9a66c"), // Warning amber (muted)
	Error:   lipgloss.Color("#d76e6e"), // Error red (muted)
	Info:    lipgloss.Color("#6ab0d3"), // Info blue (muted)

	// Border colors - from styles.ColorBorder*
	Border:       lipgloss.Color("#3d4454"), // Default border
	BorderActive: lipgloss.Color("#5fb3b3"), // Active/focused border (teal)
	BorderError:  lipgloss.Color("#d76e6e"), // Error border

	// Special colors
	Selection: lipgloss.Color("#3d4454"), // Selected item background
	Highlight: lipgloss.Color("#4d5566"), // Highlighted text background
	Link:      lipgloss.Color("#6ab0d3"), // Hyperlink color (same as info)
}

// NewDefaultTheme creates the default KaRiya theme.
//
// Returns:
//   - A Theme value.
//
// Side effects:
//   - None.
func NewDefaultTheme() Theme {
	return NewBaseTheme(
		"default",
		"KaRiya Default Theme - Professional dark theme with muted accents",
		"KaRiya Team",
		true,
		&DefaultPalette,
	)
}
