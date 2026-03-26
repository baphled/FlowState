// Package themes provides a theme system for the KaRiya TUI.
// It enables runtime theme switching with multiple built-in themes
// and consistent styling across all UI components.
package themes

import (
	"github.com/charmbracelet/lipgloss"
)

// ColorPalette defines all colors used in a theme.
// Each color has a specific semantic purpose to ensure
// consistent usage across the UI.
type ColorPalette struct {
	// Background colors
	Background     lipgloss.Color // Primary background
	BackgroundAlt  lipgloss.Color // Alternate/contrast background
	BackgroundCard lipgloss.Color // Card/panel background

	// Foreground colors
	Foreground      lipgloss.Color // Primary text
	ForegroundDim   lipgloss.Color // Secondary/dimmed text
	ForegroundMuted lipgloss.Color // Muted/disabled text

	// Accent colors
	Primary   lipgloss.Color // Primary action/brand color
	Secondary lipgloss.Color // Secondary actions
	Tertiary  lipgloss.Color // Tertiary/highlight

	// Status colors
	Success lipgloss.Color
	Warning lipgloss.Color
	Error   lipgloss.Color
	Info    lipgloss.Color

	// Border colors
	Border       lipgloss.Color // Default borders
	BorderActive lipgloss.Color // Focused/active borders
	BorderError  lipgloss.Color // Error state borders

	// Special colors
	Selection lipgloss.Color // Selected item background
	Highlight lipgloss.Color // Highlighted text background
	Link      lipgloss.Color // Hyperlink color
}

// Theme defines the contract for all themes.
// Implementations provide color palettes and pre-composed styles
// for consistent UI rendering.
type Theme interface {
	// Name returns the theme's display name.
	Name() string
	// Description returns the theme's description.
	Description() string
	// Author returns the theme's author.
	Author() string
	// IsDark returns true if this is a dark theme.
	IsDark() bool

	// Palette returns the color palette for this theme.
	Palette() *ColorPalette

	// Styles returns the pre-composed style set for this theme.
	Styles() *StyleSet

	// PrimaryColor returns the primary action/brand color.
	PrimaryColor() lipgloss.Color
	// SecondaryColor returns the secondary action color.
	SecondaryColor() lipgloss.Color
	// AccentColor returns the accent/highlight color.
	AccentColor() lipgloss.Color
	// BackgroundColor returns the primary background color.
	BackgroundColor() lipgloss.Color
	// ForegroundColor returns the primary text color.
	ForegroundColor() lipgloss.Color
	// MutedColor returns the muted/disabled text color.
	MutedColor() lipgloss.Color
	// SuccessColor returns the success state color.
	SuccessColor() lipgloss.Color
	// WarningColor returns the warning state color.
	WarningColor() lipgloss.Color
	// ErrorColor returns the error state color.
	ErrorColor() lipgloss.Color
	// InfoColor returns the info state color.
	InfoColor() lipgloss.Color
	// BorderColor returns the default border color.
	BorderColor() lipgloss.Color
	// BorderActiveColor returns the active/focused border color.
	BorderActiveColor() lipgloss.Color
}

// BaseTheme is the default implementation of the Theme interface.
// It wraps a ColorPalette and generates StyleSet from it.
type BaseTheme struct {
	name        string
	description string
	author      string
	isDark      bool
	palette     *ColorPalette
	styles      *StyleSet
}

// NewBaseTheme creates a new BaseTheme with the given parameters.
//
// Expected:
//   - Must be a valid string.
//   - bool must be valid.
//   - colorpalette must be valid.
//
// Returns:
//   - A fully initialized BaseTheme ready for use.
//
// Side effects:
//   - None.
func NewBaseTheme(name, description, author string, isDark bool, palette *ColorPalette) *BaseTheme {
	return &BaseTheme{
		name:        name,
		description: description,
		author:      author,
		isDark:      isDark,
		palette:     palette,
		styles:      GenerateStyles(palette),
	}
}

// Name returns the theme's identifier.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (t *BaseTheme) Name() string {
	return t.name
}

// Description returns a human-readable description of the theme.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (t *BaseTheme) Description() string {
	return t.description
}

// Author returns the theme's author.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (t *BaseTheme) Author() string {
	return t.author
}

// IsDark returns true if this is a dark theme.
//
// Returns:
//   - A bool value.
//
// Side effects:
//   - None.
func (t *BaseTheme) IsDark() bool {
	return t.isDark
}

// Palette returns the theme's color palette.
//
// Returns:
//   - A fully initialized ColorPalette ready for use.
//
// Side effects:
//   - None.
func (t *BaseTheme) Palette() *ColorPalette {
	return t.palette
}

// Styles returns the pre-composed styles for this theme.
//
// Returns:
//   - A fully initialized StyleSet ready for use.
//
// Side effects:
//   - None.
func (t *BaseTheme) Styles() *StyleSet {
	return t.styles
}

// PrimaryColor returns the primary accent color.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (t *BaseTheme) PrimaryColor() lipgloss.Color {
	return t.palette.Primary
}

// SecondaryColor returns the secondary accent color.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (t *BaseTheme) SecondaryColor() lipgloss.Color {
	return t.palette.Secondary
}

// AccentColor returns the tertiary/accent color.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (t *BaseTheme) AccentColor() lipgloss.Color {
	return t.palette.Tertiary
}

// BackgroundColor returns the primary background color.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (t *BaseTheme) BackgroundColor() lipgloss.Color {
	return t.palette.Background
}

// ForegroundColor returns the primary foreground/text color.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (t *BaseTheme) ForegroundColor() lipgloss.Color {
	return t.palette.Foreground
}

// MutedColor returns the muted/disabled text color.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (t *BaseTheme) MutedColor() lipgloss.Color {
	return t.palette.ForegroundMuted
}

// SuccessColor returns the success status color.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (t *BaseTheme) SuccessColor() lipgloss.Color {
	return t.palette.Success
}

// WarningColor returns the warning status color.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (t *BaseTheme) WarningColor() lipgloss.Color {
	return t.palette.Warning
}

// ErrorColor returns the error status color.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (t *BaseTheme) ErrorColor() lipgloss.Color {
	return t.palette.Error
}

// InfoColor returns the info status color.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (t *BaseTheme) InfoColor() lipgloss.Color {
	return t.palette.Info
}

// BorderColor returns the default border color.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (t *BaseTheme) BorderColor() lipgloss.Color {
	return t.palette.Border
}

// BorderActiveColor returns the active/focused border color.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (t *BaseTheme) BorderActiveColor() lipgloss.Color {
	return t.palette.BorderActive
}
