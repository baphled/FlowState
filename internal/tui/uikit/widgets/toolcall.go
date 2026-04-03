// Package widgets provides reusable UI components for the TUI.
package widgets

import "github.com/charmbracelet/lipgloss"

const defaultToolIcon = "⚡"

var toolPendingTexts = map[string]string{
	"bash":           "Writing command…",
	"read":           "Reading…",
	"write":          "Preparing write…",
	"edit":           "Preparing edit…",
	"glob":           "Finding files…",
	"grep":           "Searching…",
	"task":           "Delegating…",
	"call_omo_agent": "Delegating…",
	"skill_load":     "Loading skill…",
	"web_search":     "Fetching…",
}

const (
	statusRunning  = "running"
	statusComplete = "complete"
	statusError    = "error"
)

// ToolCallWidget displays an inline tool call status indicator in the chat view.
//
// It maps tool names to semantic icons and provides contextual pending text when tools are running.
type ToolCallWidget struct {
	name          string
	status        string
	runningStyle  lipgloss.Style
	completeStyle lipgloss.Style
	errorStyle    lipgloss.Style
}

// NewToolCallWidget creates a ToolCallWidget for the given tool name and status.
//
// Expected:
//   - name identifies the tool being rendered.
//   - status is the current tool state, such as "running" or "complete".
//
// Returns:
//   - A ToolCallWidget configured with the supplied tool metadata.
//
// Side effects:
//   - None.
func NewToolCallWidget(name, status string) *ToolCallWidget {
	return &ToolCallWidget{
		name:          name,
		status:        status,
		runningStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("226")),
		completeStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("46")),
		errorStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
	}
}

// Render returns the formatted string representation of the tool call status.
//
// Returns:
//   - The rendered tool call indicator string.
//
// Side effects:
//   - None.
func (w *ToolCallWidget) Render() string {
	statusStyle := w.runningStyle
	switch w.status {
	case statusRunning:
		statusStyle = w.runningStyle
	case statusComplete:
		statusStyle = w.completeStyle
	case statusError:
		statusStyle = w.errorStyle
	}

	if w.status == statusRunning {
		return toolIcon(w.name) + " " + toolPendingText(w.name)
	}

	status := statusStyle.Render("[" + w.status + "]")
	return toolIcon(w.name) + " " + w.name + " " + status
}

// toolIcon returns the icon used for the named tool.
//
// Expected:
//   - name is a tool name string, which may be empty or unknown.
//
// Returns:
//   - A single-character icon mapped to the tool name.
//   - defaultToolIcon when the name is not recognised.
//
// Side effects:
//   - None.
func toolIcon(name string) string {
	switch name {
	case "bash":
		return "$"
	case "read":
		return "→"
	case "write", "edit":
		return "←"
	case "glob":
		return "◆"
	case "grep":
		return "?"
	case "task", "call_omo_agent":
		return "│"
	case "skill_load":
		return "→"
	case "web_search":
		return "🌐"
	default:
		return defaultToolIcon
	}
}

// ToolIcon returns the icon character for the given tool name.
//
// Expected:
//   - name is a tool name string, which may be empty or unknown.
//
// Returns:
//   - The semantic icon for the tool, or defaultToolIcon for unknown names.
//
// Side effects:
//   - None.
func ToolIcon(name string) string {
	return toolIcon(name)
}

// toolPendingText returns the pending status text for the named tool.
//
// Expected:
//   - name is a tool name string, which may be empty or unknown.
//
// Returns:
//   - The configured pending text for recognised tools.
//   - "Running…" when the tool name is not recognised.
//
// Side effects:
//   - None.
func toolPendingText(name string) string {
	if text, ok := toolPendingTexts[name]; ok {
		return text
	}
	return "Running…"
}
