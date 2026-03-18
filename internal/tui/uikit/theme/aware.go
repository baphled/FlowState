package theme

import (
	"github.com/charmbracelet/lipgloss"
)

// Aware is an embeddable struct that provides theme awareness to components.
// Components can embed this to automatically gain theme access and color getters.
//
// Example:
//
//	type Button struct {
//	    theme.Aware
//	    label string
//	}
//
//	btn := &Button{label: "Click Me"}
//	btn.SetTheme(theme.Default())
//	color := btn.PrimaryColor()
type Aware struct {
	theme Theme
}

// SetTheme sets the theme for this component.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Side effects:
//   - None.
func (a *Aware) SetTheme(t Theme) {
	a.theme = t
}

// Theme returns the current theme.
//
// Returns:
//   - A Theme value.
//
// Side effects:
//   - None.
func (a *Aware) Theme() Theme {
	if a.theme == nil {
		return Default()
	}
	return a.theme
}

// PrimaryColor returns the primary accent color from the theme.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (a *Aware) PrimaryColor() lipgloss.Color {
	return a.Theme().PrimaryColor()
}

// SecondaryColor returns the secondary accent color from the theme.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (a *Aware) SecondaryColor() lipgloss.Color {
	return a.Theme().SecondaryColor()
}

// AccentColor returns the tertiary/accent color from the theme.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (a *Aware) AccentColor() lipgloss.Color {
	return a.Theme().AccentColor()
}

// ErrorColor returns the error status color from the theme.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (a *Aware) ErrorColor() lipgloss.Color {
	return a.Theme().ErrorColor()
}

// SuccessColor returns the success status color from the theme.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (a *Aware) SuccessColor() lipgloss.Color {
	return a.Theme().SuccessColor()
}

// WarningColor returns the warning status color from the theme.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (a *Aware) WarningColor() lipgloss.Color {
	return a.Theme().WarningColor()
}

// BorderColor returns the default border color from the theme.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (a *Aware) BorderColor() lipgloss.Color {
	return a.Theme().BorderColor()
}

// BackgroundColor returns the primary background color from the theme.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (a *Aware) BackgroundColor() lipgloss.Color {
	return a.Theme().BackgroundColor()
}

// MutedColor returns the muted/disabled text color from the theme.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (a *Aware) MutedColor() lipgloss.Color {
	return a.Theme().MutedColor()
}

// InfoColor returns the info status color from the theme.
//
// Returns:
//   - A lipgloss.Color value.
//
// Side effects:
//   - None.
func (a *Aware) InfoColor() lipgloss.Color {
	return a.Theme().InfoColor()
}
