package widgets

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
)

// MessageWidget renders a styled chat message with role differentiation.
type MessageWidget struct {
	theme.Aware
	role         string
	content      string
	renderFunc   func(string, int) string
	labelStyle   lipgloss.Style
	contentStyle lipgloss.Style
	toolStyle    lipgloss.Style
	resultStyle  lipgloss.Style
	errorStyle   lipgloss.Style
	skillStyle   lipgloss.Style
	systemStyle  lipgloss.Style
	todoStyle    lipgloss.Style
}

// NewMessageWidget creates a new message widget for the given role and content.
//
// Expected:
//   - role is "user" or "assistant".
//   - content is the message text.
//   - th can be nil (uses default theme).
//
// Returns:
//   - A configured MessageWidget ready for rendering.
//
// Side effects:
//   - None.
func NewMessageWidget(role, content string, th theme.Theme) *MessageWidget {
	w := &MessageWidget{
		role:         role,
		content:      content,
		contentStyle: lipgloss.NewStyle().PaddingLeft(2),
		toolStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true),
		resultStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		errorStyle:   lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
		skillStyle:   lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true),
		systemStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true),
		todoStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("75")),
	}
	if th != nil {
		w.SetTheme(th)
		w.labelStyle = lipgloss.NewStyle().Foreground(th.PrimaryColor()).Bold(true)
	}
	return w
}

// SetMarkdownRenderer sets a custom function for rendering markdown content.
//
// Expected:
//   - fn takes content and width, returns rendered string.
//
// Side effects:
//   - Updates the renderFunc field.
func (w *MessageWidget) SetMarkdownRenderer(fn func(string, int) string) {
	w.renderFunc = fn
}

// Render returns the styled message as a string.
//
// Expected:
//   - width is the terminal width in columns.
//
// Returns:
//   - A styled string with role label and message content.
//
// Side effects:
//   - None.
func (w *MessageWidget) Render(width int) string {
	th := w.Theme()

	var sb strings.Builder

	switch w.role {
	case "assistant":
		assistantLabel := lipgloss.NewStyle().Foreground(th.SecondaryColor()).Bold(true)
		sb.WriteString(assistantLabel.Render("Assistant"))
		sb.WriteString("\n")

		content := w.content
		if w.renderFunc != nil {
			content = w.renderFunc(content, width-2)
		}

		sb.WriteString(w.contentStyle.Render(content))

	case "tool_call", "tool_result", "tool_error", "skill_load", "system", "todo_update", "thinking":
		sb.WriteString(w.renderToolMessage())

	default:
		sb.WriteString(w.labelStyle.Render("You"))
		sb.WriteString("\n")

		sb.WriteString(w.contentStyle.Render(w.content))
	}

	return sb.String()
}

// renderToolMessage renders tool-related messages with appropriate styling and emoji.
//
// Expected:
//   - w.role is one of: "tool_call", "tool_result", "tool_error", "skill_load", "system", "todo_update", "thinking".
//
// Returns:
//   - A styled string with emoji prefix and content.
//
// Side effects:
//   - None.
func (w *MessageWidget) renderToolMessage() string {
	switch w.role {
	case "tool_call":
		return w.toolStyle.Render("🔧 " + w.content)
	case "tool_result":
		return w.resultStyle.Render("📤 " + w.content)
	case "tool_error":
		return w.errorStyle.Render("❌ " + w.content)
	case "skill_load":
		return w.skillStyle.Render("📚 " + w.content)
	case "system":
		return w.systemStyle.Render(w.content)
	case "todo_update":
		return w.todoStyle.Render(w.content)
	case "thinking":
		return w.systemStyle.Render("💭 " + w.content)
	default:
		return ""
	}
}
