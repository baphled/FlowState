// Package widgets provides reusable UI components for the TUI.
package widgets

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	tooldisplay "github.com/baphled/flowstate/internal/tool/display"
)

const defaultToolIcon = "⚡"

// resultPreviewLines is the maximum number of lines of tool output a
// ToolCallWidget renders inline below the summary line for completed
// or errored tool calls. Anything beyond this is truncated; the user
// can use the Ctrl+E details modal for the full payload.
const resultPreviewLines = 5

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
	args          map[string]any
	result        string
	runningStyle  lipgloss.Style
	completeStyle lipgloss.Style
	errorStyle    lipgloss.Style
	previewStyle  lipgloss.Style
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
		previewStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	}
}

// SetArgs records the raw provider tool-call argument map so Render can
// build an opencode-style "name: primary-arg" summary via tooldisplay.Summary.
//
// Expected:
//   - args is the provider.ToolCall.Arguments map; nil is acceptable and
//     causes Render to fall back to the bare tool name.
//
// Returns:
//   - The receiver, to support fluent chaining.
//
// Side effects:
//   - Updates the widget's args field.
func (w *ToolCallWidget) SetArgs(args map[string]any) *ToolCallWidget {
	w.args = args
	return w
}

// SetResult records the tool-result body (or error text) so Render can
// emit a 1-5 line preview underneath completed/errored tool calls.
//
// Expected:
//   - result is the tool output as received from the engine; empty is
//     acceptable and suppresses the preview.
//
// Returns:
//   - The receiver, to support fluent chaining.
//
// Side effects:
//   - Updates the widget's result field.
func (w *ToolCallWidget) SetResult(result string) *ToolCallWidget {
	w.result = result
	return w
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

	icon := toolIcon(w.name)
	summary := w.summary()

	if w.status == statusRunning {
		// Pre-args legacy callers see the friendly pending text; rich
		// callers (with args) get the opencode-style "name: arg" line.
		if w.args == nil {
			return icon + " " + toolPendingText(w.name)
		}
		return icon + " " + summary
	}

	statusBadge := statusStyle.Render("[" + w.status + "]")
	header := icon + " " + summary + " " + statusBadge

	preview := w.resultPreview()
	if preview == "" {
		return header
	}
	return header + "\n" + preview
}

// summary returns the opencode-style "name: primary-arg" string used in
// every render mode. Falls back to the widget's bare name when no args
// map has been supplied or when tooldisplay has no primary key for the
// tool.
//
// Expected:
//   - The widget has been constructed via NewToolCallWidget.
//
// Returns:
//   - "name: arg" when args carries a recognised primary value.
//   - The bare tool name otherwise.
//
// Side effects:
//   - None.
func (w *ToolCallWidget) summary() string {
	if w.args == nil {
		return w.name
	}
	return tooldisplay.Summary(w.name, w.args)
}

// resultPreview returns up to resultPreviewLines lines of the recorded
// tool result, indented and dimmed for inline display under the summary.
// Returns the empty string when no result is available so the caller can
// skip the trailing newline.
//
// Expected:
//   - w.result is the raw tool output (may be empty).
//
// Returns:
//   - A multi-line preview block, or empty string when no preview is shown.
//
// Side effects:
//   - None.
func (w *ToolCallWidget) resultPreview() string {
	if w.result == "" {
		return ""
	}
	lines := strings.Split(w.result, "\n")
	if len(lines) > resultPreviewLines {
		lines = lines[:resultPreviewLines]
	}
	indented := make([]string, len(lines))
	for i, line := range lines {
		indented[i] = "    " + line
	}
	return w.previewStyle.Render(strings.Join(indented, "\n"))
}

// toolIcon returns the icon used for the named tool.
//
// Accepts both bare tool names ("read") and the flattened "name: arg"
// summary form ("read: /etc/passwd") that intent.extractToolInfo emits;
// the colon prefix is stripped before the icon lookup so callers do not
// need to know which form the upstream layer produced.
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
	if idx := strings.Index(name, ": "); idx > 0 {
		name = name[:idx]
	}
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
	if idx := strings.Index(name, ": "); idx > 0 {
		name = name[:idx]
	}
	if text, ok := toolPendingTexts[name]; ok {
		return text
	}
	return "Running…"
}
