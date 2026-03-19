// Package chat provides the chat view component for the TUI.
package chat

import (
	"strings"

	"github.com/charmbracelet/glamour"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

// Message represents a chat message with a role and content.
type Message struct {
	Role    string
	Content string
}

// View represents the chat view component with messages and streaming state.
type View struct {
	theme.Aware

	width        int
	height       int
	messages     []Message
	streaming    bool
	response     string
	spinnerFrame int
	renderFunc   func(string, int) string
}

// NewView creates a new chat view with default dimensions and markdown rendering.
//
// Returns:
//   - An initialised View with default width (80), height (24), and markdown renderer.
//
// Side effects:
//   - None.
func NewView() *View {
	return &View{
		width:      80,
		height:     24,
		renderFunc: renderMarkdown,
	}
}

// Width returns the current width of the view.
//
// Returns:
//   - The current view width in columns.
//
// Side effects:
//   - None.
func (v *View) Width() int {
	return v.width
}

// Height returns the current height of the view.
//
// Returns:
//   - The current view height in rows.
//
// Side effects:
//   - None.
func (v *View) Height() int {
	return v.height
}

// SetDimensions sets the width and height of the view.
//
// Expected:
//   - width and height are positive integers.
//
// Side effects:
//   - Updates the view's width and height fields.
func (v *View) SetDimensions(width, height int) {
	v.width = width
	v.height = height
}

// AddMessage appends a message to the view's message list.
//
// Expected:
//   - msg is a Message with Role and Content.
//
// Side effects:
//   - Appends the message to the messages slice.
func (v *View) AddMessage(msg Message) {
	v.messages = append(v.messages, msg)
}

// SetStreaming sets the streaming state and partial response content.
//
// Expected:
//   - streaming is a boolean indicating if streaming is active.
//   - response is the partial response content (may be empty).
//
// Side effects:
//   - Updates the streaming and response fields.
func (v *View) SetStreaming(streaming bool, response string) {
	v.streaming = streaming
	v.response = response
}

// SetSpinnerFrame sets the animation frame index for the streaming spinner.
//
// Expected:
//   - frame is the current tick frame from the Intent.
//
// Side effects:
//   - Updates the spinnerFrame field.
func (v *View) SetSpinnerFrame(frame int) {
	v.spinnerFrame = frame
}

// SetMarkdownRenderer sets a custom function for rendering markdown content.
//
// Expected:
//   - fn is a function that takes content and width, returns rendered string.
//
// Side effects:
//   - Updates the renderFunc field.
func (v *View) SetMarkdownRenderer(fn func(string, int) string) {
	v.renderFunc = fn
}

// SetTheme sets the theme for the chat view.
//
// Expected:
//   - th is a valid theme instance (can be nil).
//
// Side effects:
//   - Updates the embedded theme.Aware with the provided theme.
func (v *View) SetTheme(th theme.Theme) {
	v.Aware.SetTheme(th)
}

// renderMarkdown renders markdown content using glamour with dark theme and word wrapping.
//
// Expected:
//   - content is markdown text.
//   - width is the terminal width for word wrapping.
//
// Returns:
//   - Rendered markdown as a string, or original content if rendering fails.
//
// Side effects:
//   - None.
func renderMarkdown(content string, width int) string {
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return content
	}
	out, err := r.Render(content)
	if err != nil {
		return content
	}
	return strings.TrimRight(out, "\n")
}

// RenderContent renders the chat view content including messages and streaming response.
//
// Expected:
//   - width is the terminal width in columns.
//
// Returns:
//   - A rendered chat view string with messages and streaming indicator.
//
// Side effects:
//   - None.
func (v *View) RenderContent(width int) string {
	var sb strings.Builder

	th := v.Theme()

	for _, msg := range v.messages {
		mw := widgets.NewMessageWidget(msg.Role, msg.Content, th)
		if v.renderFunc != nil {
			mw.SetMarkdownRenderer(v.renderFunc)
		}
		sb.WriteString(mw.Render(width))
		sb.WriteString("\n\n")
	}

	if v.streaming {
		if v.response != "" {
			sb.WriteString(v.response)
			sb.WriteString("\n")
		}
		si := widgets.NewStatusIndicator(th)
		si.SetActive(true)
		si.SetFrame(v.spinnerFrame)
		sb.WriteString(si.Render())
		sb.WriteString("\n")
	}

	return sb.String()
}

// ResultSend represents the result of sending a chat message.
type ResultSend struct {
	Message string
}

// ResultCancel represents the result of cancelling a chat operation.
type ResultCancel struct{}
