package widgets

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
)

// MessageWidget renders a styled chat message with role differentiation.
type MessageWidget struct {
	theme.Aware
	role                string
	content             string
	renderFunc          func(string, int) string
	toolName            string
	toolInput           string
	agentColor          lipgloss.Color
	modelID             string
	mode                string
	duration            time.Duration
	interrupted         bool
	footer              *MessageFooter
	labelStyle          lipgloss.Style
	assistantLabelStyle lipgloss.Style
	contentStyle        lipgloss.Style
	toolStyle           lipgloss.Style
	resultStyle         lipgloss.Style
	errorStyle          lipgloss.Style
	skillStyle          lipgloss.Style
	systemStyle         lipgloss.Style
	todoStyle           lipgloss.Style
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
		w.assistantLabelStyle = lipgloss.NewStyle().Foreground(th.SecondaryColor()).Bold(true)
		w.footer = NewMessageFooter(th)
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

// SetToolName sets the tool name for tool_result messages.
//
// Expected:
//   - name is the tool name that produced the result.
//
// Side effects:
//   - Updates the toolName field.
func (w *MessageWidget) SetToolName(name string) { w.toolName = name }

// SetToolInput sets the primary argument for tool_result BlockTool rendering.
//
// Expected:
//   - input is the primary argument passed to the tool (may be empty).
//
// Returns:
//   - None.
//
// Side effects:
//   - Updates the toolInput field.
func (w *MessageWidget) SetToolInput(input string) { w.toolInput = input }

// SetAgentColor sets the agent colour for assistant messages.
//
// Expected:
//   - c is a lipgloss.Color; zero value means use theme default.
//
// Side effects:
//   - Updates the agentColor field.
func (w *MessageWidget) SetAgentColor(c lipgloss.Color) { w.agentColor = c }

// SetModelID sets the model identifier for assistant message footers.
//
// Expected:
//   - id is the model identifier string; empty means no footer.
//
// Side effects:
//   - Updates the modelID field.
func (w *MessageWidget) SetModelID(id string) { w.modelID = id }

// SetMode sets the agent mode for assistant message footers.
//
// Expected:
//   - mode is the agent mode (e.g. "chat", "build", "plan"); empty omits the mode segment.
//
// Side effects:
//   - Updates the mode field.
func (w *MessageWidget) SetMode(mode string) { w.mode = mode }

// SetDuration sets the response generation duration for assistant message footers.
//
// Expected:
//   - d is a non-negative time.Duration.
//
// Side effects:
//   - Updates the duration field.
func (w *MessageWidget) SetDuration(d time.Duration) { w.duration = d }

// SetInterrupted marks the message as interrupted for assistant message footers.
//
// Expected:
//   - b is true when the message response was cut short, false otherwise.
//
// Side effects:
//   - Updates the interrupted field.
func (w *MessageWidget) SetInterrupted(b bool) { w.interrupted = b }

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
		labelColor := th.SecondaryColor()
		if w.agentColor != lipgloss.Color("") {
			labelColor = w.agentColor
		}
		label := lipgloss.NewStyle().Foreground(labelColor).Bold(true)
		sb.WriteString(label.Render("Assistant"))
		sb.WriteString("\n")

		content := w.content
		if w.renderFunc != nil {
			content = w.renderFunc(content, width-2)
		}

		sb.WriteString(w.contentStyle.Render(content))

		if w.modelID != "" && w.footer != nil {
			w.footer.SetMetadata(w.mode, w.modelID, w.duration, w.interrupted, w.agentColor)
			sb.WriteString("\n")
			sb.WriteString(w.footer.Render())
		}

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
		// Suppressed: every committed tool_call is paired with a
		// tool_result message that renders the full rich BlockTool
		// (name + input + output). The previous "<icon> <name>" line
		// above the BlockTool was pure redundancy. Suppressing it
		// gives one rich block per tool invocation, matching the
		// streaming ToolCallWidget's combined block.
		return ""
	case "tool_result":
		if w.toolName != "" {
			// Default-collapsed: render the title line only. Long
			// tool outputs (file reads, command output) would dump
			// up to 10 lines of content into chat history with the
			// expanded form, which the user reported as "the
			// reading output is rendered straight to the chat".
			// Claude Code's pattern: compact one-line summary in
			// history, with the live streaming widget having
			// already shown a result preview during execution. The
			// expanded form remains available for a future
			// interactive toggle.
			return NewBlockTool(w.toolName, w.toolInput, w.content).Render()
		}
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
