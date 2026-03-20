// Package widgets provides reusable UI components for the TUI.
package widgets

import "github.com/charmbracelet/lipgloss"

// ToolCallWidget displays an inline tool call status in the chat view.
type ToolCallWidget struct {
	name   string
	status string
}

// NewToolCallWidget creates a ToolCallWidget for the given tool name and status.
//
// Expected:
//   - status must be one of "running", "complete", "error"
//
// Returns:
//   - A new ToolCallWidget ready for rendering.
//
// Side effects:
//   - None.
func NewToolCallWidget(name, status string) *ToolCallWidget {
	return &ToolCallWidget{
		name:   name,
		status: status,
	}
}

// Render returns the formatted string representation of the tool call status.
//
// Returns:
//   - A styled string: "⚡ {name} [{status}]" with colour per status
//
// Side effects:
//   - None.
func (w *ToolCallWidget) Render() string {
	var statusStyle lipgloss.Style

	switch w.status {
	case "running":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
	case "complete":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	case "error":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	}

	status := statusStyle.Render("[" + w.status + "]")
	return "⚡ " + w.name + " " + status
}
