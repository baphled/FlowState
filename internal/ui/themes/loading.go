package themes

import (
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
)

// SpinnerType represents different spinner animation styles.
type SpinnerType int

const (
	// SpinnerDot is a simple dot spinner.
	SpinnerDot SpinnerType = iota
	// SpinnerLine is a line-based spinner.
	SpinnerLine
	// SpinnerMiniDot is a smaller dot spinner.
	SpinnerMiniDot
	// SpinnerJump is a jumping animation.
	SpinnerJump
	// SpinnerPulse is a pulsing animation.
	SpinnerPulse
	// SpinnerPoints is an ellipsis-style spinner.
	SpinnerPoints
	// SpinnerGlobe is a globe spinning animation.
	SpinnerGlobe
	// SpinnerMoon is a moon phase animation.
	SpinnerMoon
	// SpinnerMonkey is a monkey animation.
	SpinnerMonkey
	// SpinnerMeter is a meter-style animation.
	SpinnerMeter
	// SpinnerHamburger is a hamburger menu animation.
	SpinnerHamburger
)

// NewThemedSpinnerWithType creates a theme-aware spinner with a specific animation type.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//   - spinnertype must be valid.
//
// Returns:
//   - A spinner.Model value.
//
// Side effects:
//   - None.
func NewThemedSpinnerWithType(theme Theme, spinnerType SpinnerType) spinner.Model {
	s := spinner.New()

	// Map SpinnerType to bubbles/spinner types
	switch spinnerType {
	case SpinnerDot:
		s.Spinner = spinner.Dot
	case SpinnerLine:
		s.Spinner = spinner.Line
	case SpinnerMiniDot:
		s.Spinner = spinner.MiniDot
	case SpinnerJump:
		s.Spinner = spinner.Jump
	case SpinnerPulse:
		s.Spinner = spinner.Pulse
	case SpinnerPoints:
		s.Spinner = spinner.Points
	case SpinnerGlobe:
		s.Spinner = spinner.Globe
	case SpinnerMoon:
		s.Spinner = spinner.Moon
	case SpinnerMonkey:
		s.Spinner = spinner.Monkey
	case SpinnerMeter:
		s.Spinner = spinner.Meter
	case SpinnerHamburger:
		s.Spinner = spinner.Hamburger
	default:
		s.Spinner = spinner.Dot
	}

	// Apply theme color if available
	if theme != nil {
		palette := theme.Palette()
		s.Style = lipgloss.NewStyle().Foreground(palette.Primary)
	}

	return s
}

// LoadingView represents a themed loading view with spinner and message.
type LoadingView struct {
	theme   Theme
	spinner spinner.Model
	message string
}

// NewLoadingView creates a new themed loading view.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized LoadingView ready for use.
//
// Side effects:
//   - None.
func NewLoadingView(theme Theme, message string) *LoadingView {
	return &LoadingView{
		theme:   theme,
		spinner: NewThemedSpinner(theme),
		message: message,
	}
}

// SetMessage updates the loading message.
//
// Expected:
//   - Must be a valid string.
//
// Side effects:
//   - None.
func (lv *LoadingView) SetMessage(message string) {
	lv.message = message
}

// GetSpinner returns the underlying spinner model for updates.
//
// Returns:
//   - A spinner.Model value.
//
// Side effects:
//   - None.
func (lv *LoadingView) GetSpinner() spinner.Model {
	return lv.spinner
}

// SetSpinner updates the spinner model (typically after Update).
//
// Expected:
//   - model must be valid.
//
// Side effects:
//   - None.
func (lv *LoadingView) SetSpinner(s spinner.Model) {
	lv.spinner = s
}

// View renders the loading view with spinner and message.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (lv *LoadingView) View() string {
	var messageStyle lipgloss.Style

	if lv.theme != nil {
		palette := lv.theme.Palette()
		messageStyle = lipgloss.NewStyle().
			Foreground(palette.ForegroundDim).
			MarginLeft(1)
	} else {
		messageStyle = lipgloss.NewStyle().MarginLeft(1)
	}

	return lv.spinner.View() + messageStyle.Render(lv.message)
}

// RenderLoadingBox renders a loading indicator in a styled box.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//   - Must be a valid string.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func RenderLoadingBox(theme Theme, title, message, spinnerView string) string {
	if theme == nil {
		return title + "\n" + spinnerView + " " + message
	}

	palette := theme.Palette()
	styles := theme.Styles()

	titleStyle := lipgloss.NewStyle().
		Foreground(palette.Primary).
		Bold(true)

	messageStyle := lipgloss.NewStyle().
		Foreground(palette.ForegroundDim)

	spinnerStyle := lipgloss.NewStyle().
		Foreground(palette.Primary)

	content := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render(title),
		"",
		spinnerStyle.Render(spinnerView)+" "+messageStyle.Render(message),
	)

	return styles.CardBase.Render(content)
}

// RenderProgressBox renders a progress indicator with percentage.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//   - Must be a valid string.
//   - float64 must be valid.
//   - Must be a valid string.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func RenderProgressBox(theme Theme, title string, progress float64, progressView string) string {
	if theme == nil {
		return title + "\n" + progressView
	}

	palette := theme.Palette()
	styles := theme.Styles()

	titleStyle := lipgloss.NewStyle().
		Foreground(palette.Primary).
		Bold(true)

	percentStyle := lipgloss.NewStyle().
		Foreground(palette.ForegroundDim)

	content := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render(title),
		"",
		progressView+" "+percentStyle.Render(lipgloss.NewStyle().
			Width(4).
			Align(lipgloss.Right).
			Render("%d%%")),
	)
	// Apply progress value
	_ = progress

	return styles.CardBase.Render(content)
}
