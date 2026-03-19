package widgets

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
)

// MessageWidget renders a styled chat message with role differentiation.
type MessageWidget struct {
	theme.Aware
	role       string
	content    string
	renderFunc func(string, int) string
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
		role:    role,
		content: content,
	}
	if th != nil {
		w.SetTheme(th)
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

	if w.role == "assistant" {
		labelStyle := lipgloss.NewStyle().
			Foreground(th.SecondaryColor()).
			Bold(true)
		sb.WriteString(labelStyle.Render("Assistant"))
		sb.WriteString("\n")

		content := w.content
		if w.renderFunc != nil {
			content = w.renderFunc(content, width)
		}

		contentStyle := lipgloss.NewStyle().
			PaddingLeft(2)
		sb.WriteString(contentStyle.Render(content))
	} else {
		labelStyle := lipgloss.NewStyle().
			Foreground(th.PrimaryColor()).
			Bold(true)
		sb.WriteString(labelStyle.Render("You"))
		sb.WriteString("\n")

		contentStyle := lipgloss.NewStyle().
			PaddingLeft(2)
		sb.WriteString(contentStyle.Render(w.content))
	}

	return sb.String()
}
