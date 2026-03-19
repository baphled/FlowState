package widgets

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// StatusIndicator displays a streaming/thinking status with spinner animation.
type StatusIndicator struct {
	theme.Aware
	active bool
	frame  int
}

// NewStatusIndicator creates a new status indicator.
//
// Expected:
//   - th can be nil (uses default theme).
//
// Returns:
//   - A configured StatusIndicator in idle state.
//
// Side effects:
//   - None.
func NewStatusIndicator(th theme.Theme) *StatusIndicator {
	si := &StatusIndicator{}
	if th != nil {
		si.SetTheme(th)
	}
	return si
}

// IsActive returns whether the indicator is currently active.
//
// Returns:
//   - true if streaming/thinking, false otherwise.
//
// Side effects:
//   - None.
func (si *StatusIndicator) IsActive() bool {
	return si.active
}

// SetActive sets whether the indicator is active.
//
// Expected:
//   - active indicates streaming/thinking state.
//
// Side effects:
//   - Updates the active state and resets frame on deactivation.
func (si *StatusIndicator) SetActive(active bool) {
	si.active = active
	if !active {
		si.frame = 0
	}
}

// Tick advances the spinner to the next frame.
//
// Side effects:
//   - Increments the frame counter.
func (si *StatusIndicator) Tick() {
	si.frame = (si.frame + 1) % len(spinnerFrames)
}

// Render returns the styled status indicator as a string.
//
// Returns:
//   - A styled string with spinner and label, or empty if inactive.
//
// Side effects:
//   - None.
func (si *StatusIndicator) Render() string {
	if !si.active {
		return ""
	}

	th := si.Theme()

	spinnerStyle := lipgloss.NewStyle().
		Foreground(th.AccentColor())

	labelStyle := lipgloss.NewStyle().
		Foreground(th.MutedColor()).
		Italic(true)

	spinner := spinnerStyle.Render(spinnerFrames[si.frame])
	label := labelStyle.Render(" Thinking...")

	return spinner + label
}
