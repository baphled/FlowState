package chat

import (
	"strings"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/charmbracelet/lipgloss"
)

// DelegationStatusWidget displays the current status of agent delegation.
type DelegationStatusWidget struct {
	info   *provider.DelegationInfo
	theme  theme.Theme
	frame  int
	frames []string
}

// NewDelegationStatusWidget creates a new delegation status widget.
//
// Expected:
//   - info may be nil when the widget should render empty.
//   - t is a valid theme value.
//
// Returns:
//   - A widget configured with the current theme and spinner frames.
//
// Side effects:
//   - None.
func NewDelegationStatusWidget(info *provider.DelegationInfo, t theme.Theme) *DelegationStatusWidget {
	return &DelegationStatusWidget{
		info:   info,
		theme:  t,
		frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	}
}

// SetFrame updates the spinner frame for animation.
//
// Expected:
//   - frame is the current animation frame.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates the widget's frame state.
func (w *DelegationStatusWidget) SetFrame(frame int) {
	w.frame = frame
}

// Render returns the widget's string representation.
//
// Expected:
//   - The widget may have delegation information configured.
//
// Returns:
//   - The rendered delegation status, or an empty string when unset.
//
// Side effects:
//   - None.
func (w *DelegationStatusWidget) Render() string {
	if w.info == nil {
		return ""
	}

	palette := w.theme.Palette()
	style := lipgloss.NewStyle().Foreground(palette.ForegroundDim)
	activeStyle := lipgloss.NewStyle().Foreground(palette.Primary)
	statusStyle := lipgloss.NewStyle().Foreground(palette.Secondary)
	errorStyle := lipgloss.NewStyle().Foreground(palette.Error)

	var icon string
	var statusText string

	switch w.info.Status {
	case "completed":
		icon = "✓"
		statusText = statusStyle.Render(w.info.Status)
	case "failed":
		icon = "✗"
		statusText = errorStyle.Render(w.info.Status)
	default:
		icon = w.frames[w.frame%len(w.frames)]
		statusText = activeStyle.Render(w.info.Status)
	}

	parts := []string{
		activeStyle.Render(icon),
		style.Render("Delegation:"),
		activeStyle.Render(w.info.TargetAgent),
		style.Render("(" + w.info.ModelName + "/" + w.info.ProviderName + ")"),
		"[" + statusText + "]",
	}

	if w.info.Description != "" {
		parts = append(parts, style.Render("- "+w.info.Description))
	}

	return strings.Join(parts, " ")
}

// View renders the widget (alias for Render).
//
// Expected:
//   - The widget may have delegation information configured.
//
// Returns:
//   - The same content as Render().
//
// Side effects:
//   - None.
func (w *DelegationStatusWidget) View() string {
	return w.Render()
}
