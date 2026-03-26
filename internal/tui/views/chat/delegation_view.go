package chat

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
)

// DelegationStatusWidget displays the status of an agent delegation.
type DelegationStatusWidget struct {
	info   *provider.DelegationInfo
	theme  theme.Theme
	frame  int
	frames []string
}

// NewDelegationStatusWidget creates a new widget for delegation status.
//
// Returns:
//   - *DelegationStatusWidget: a widget configured with delegation details and spinner frames.
//
// Expected: info may be nil for an empty widget state.
//
// Side effects: None.
func NewDelegationStatusWidget(info *provider.DelegationInfo, th theme.Theme) *DelegationStatusWidget {
	return &DelegationStatusWidget{
		info:   info,
		theme:  th,
		frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	}
}

// SetFrame updates the spinner animation frame.
//
// Expected: frame is a valid zero-based spinner index.
// Side effects: Updates the widget's internal frame state.
func (w *DelegationStatusWidget) SetFrame(frame int) {
	w.frame = frame
}

// Init initializes the widget.
//
// Returns:
//   - tea.Cmd: nil, because the widget does not schedule its own updates.
//
// Side effects: None.
func (w *DelegationStatusWidget) Init() tea.Cmd {
	return nil
}

// Update handles messages for the widget (if it were a full Bubble Tea model).
// Since this is a view-only component driven by the parent, this is minimal.
//
// Returns:
//   - tea.Model: the widget itself.
//   - tea.Cmd: nil, because the widget does not emit commands.
//
// Expected: msg is any Bubble Tea message.
// Side effects: None.
func (w *DelegationStatusWidget) Update(_ tea.Msg) (tea.Model, tea.Cmd) {
	return w, nil
}

// Render returns the rendered widget string.
//
// Returns:
//   - string: the formatted delegation status view.
//
// Side effects: None.
func (w *DelegationStatusWidget) Render() string {
	if w.info == nil {
		return ""
	}

	var symbol string
	var statusText string
	var details string

	styles := w.theme.Styles()
	headerStyle := styles.InfoText.Bold(true)
	detailStyle := styles.MutedText

	switch w.info.Status {
	case "completed":
		symbol = styles.SuccessText.Render("✓")
		statusText = fmt.Sprintf("Delegation to %s completed", w.info.TargetAgent)
		if w.info.Description != "" {
			details = w.info.Description
		}
	case "failed":
		symbol = styles.ErrorText.Render("✗")
		statusText = fmt.Sprintf("Delegation to %s failed", w.info.TargetAgent)
	default: // running/started
		// Use PrimaryColor for the spinner if possible, or just InfoText
		symbol = styles.InfoText.Render(w.frames[w.frame%len(w.frames)])
		statusText = fmt.Sprintf("Delegating to %s (%s via %s)",
			w.info.TargetAgent,
			w.info.ModelName,
			w.info.ProviderName)
		if w.info.Description != "" {
			details = w.info.Description
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s %s", symbol, headerStyle.Render(statusText))

	if details != "" {
		sb.WriteString("\n  " + detailStyle.Render(details))
	}

	// Wrap in a box or add margin if needed, currently kept minimal as per inline task pattern
	return lipgloss.NewStyle().MarginTop(1).MarginBottom(1).Render(sb.String())
}

// View returns the string representation.
//
// Returns:
//   - string: the rendered widget output.
//
// Side effects: None.
func (w *DelegationStatusWidget) View() string {
	return w.Render()
}
