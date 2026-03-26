// Package chat provides the chat view component for the TUI.
package chat

import (
	"strings"

	"github.com/charmbracelet/glamour"

	"github.com/baphled/flowstate/internal/provider"
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

	width          int
	height         int
	messages       []Message
	streaming      bool
	response       string
	renderFunc     func(string, int) string
	toolCallName   string
	toolCallStatus string
	delegationInfo *provider.DelegationInfo
	tickFrame      int
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

// Messages returns a copy of the view's messages slice.
//
// Returns:
//   - A slice of Message values representing the chat history.
//
// Side effects:
//   - None.
func (v *View) Messages() []Message {
	return append([]Message(nil), v.messages...)
}

// StartStreaming marks the view as actively streaming and clears partial state.
//
// Side effects:
//   - Sets streaming to true, clears response and tool call state.
func (v *View) StartStreaming() {
	v.streaming = true
	v.response = ""
	v.toolCallName = ""
	v.toolCallStatus = ""
}

// HandleChunk processes a streaming response chunk.
//
// Expected:
//   - content is the partial response text (may be empty).
//   - done indicates whether this is the final chunk.
//   - errMsg is a pre-formatted error string (empty if no error).
//   - toolCallName and toolCallStatus describe an active tool call (may be empty).
//
// Side effects:
//   - Accumulates content in response when not done.
//   - Finalises the response and appends to messages when done.
func (v *View) HandleChunk(content string, done bool, errMsg string, toolCallName string, toolCallStatus string) {
	if !done {
		v.streaming = true
		v.response += content
		if toolCallName != "" {
			v.toolCallName = toolCallName
			v.toolCallStatus = toolCallStatus
		}
	} else {
		v.finaliseChunk(content, errMsg)
		v.toolCallName = ""
		v.toolCallStatus = ""
	}
}

// finaliseChunk completes a streaming response and appends it to messages.
//
// Expected:
//   - content is the final chunk content (may be empty).
//   - errMsg is a pre-formatted error string (empty if no error).
//
// Side effects:
//   - Appends the final assistant message to messages.
//   - Resets streaming and response state.
func (v *View) finaliseChunk(content string, errMsg string) {
	fullContent := v.response + content
	if errMsg != "" {
		if fullContent != "" {
			fullContent += "\n\n" + errMsg
		} else {
			fullContent = errMsg
		}
	}
	if fullContent != "" {
		v.AddMessage(Message{
			Role:    "assistant",
			Content: fullContent,
		})
	}
	v.streaming = false
	v.response = ""
}

// IsStreaming returns whether the view is currently streaming a response.
//
// Returns:
//   - true if streaming, false otherwise.
//
// Side effects:
//   - None.
func (v *View) IsStreaming() bool {
	return v.streaming
}

// Response returns the current partial streaming response.
//
// Returns:
//   - The partial response string accumulated during streaming.
//
// Side effects:
//   - None.
func (v *View) Response() string {
	return v.response
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

// SetToolCall sets the active tool call for rendering.
//
// Expected:
//   - name is the tool name (e.g., "web_search").
//   - status is one of "running", "complete", "error".
//
// Side effects:
//   - Updates the toolCallName and toolCallStatus fields.
func (v *View) SetToolCall(name, status string) {
	v.toolCallName = name
	v.toolCallStatus = status
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
//   - A rendered chat view string with messages and partial streaming response.
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

	v.appendStreamingContent(&sb, th, width)

	return sb.String()
}

// SetTickFrame updates the current animation frame for spinners.
//
// Expected:
//   - frame is the current spinner frame.
//
// Returns:
//   - None.
//
// Side effects:
//   - Updates the tick frame used by spinner rendering.
func (v *View) SetTickFrame(frame int) {
	v.tickFrame = frame
}

// HandleDelegation updates the delegation status.
//
// Expected:
//   - info is nil or contains the current delegation state.
//
// Returns:
//   - None.
//
// Side effects:
//   - Updates the delegation display state.
//   - May append a system message when delegation completes or fails.
func (v *View) HandleDelegation(info *provider.DelegationInfo) {
	if info == nil {
		return
	}

	switch info.Status {
	case "completed", "failed":
		// Clear active delegation display
		v.delegationInfo = nil

		// Add completion/failure summary as a system message
		// Note: The caller might also want to display the result content separately if any
		widget := NewDelegationStatusWidget(info, v.Theme())
		v.AddMessage(Message{
			Role:    "system",
			Content: widget.Render(),
		})
	default:
		// Active delegation
		v.delegationInfo = info
		v.streaming = true // Consider delegation as streaming/active
	}
}

// appendStreamingContent adds the current streaming response to the rendered view.
//
// Expected:
//   - sb is ready for appended content.
//   - th is the active theme.
//   - width is the render width.
//
// Returns:
//   - None.
//
// Side effects:
//   - Writes streaming tool call and assistant content into sb when streaming is active.
func (v *View) appendStreamingContent(sb *strings.Builder, th theme.Theme, width int) {
	if !v.streaming && v.delegationInfo == nil {
		return
	}

	if v.delegationInfo != nil {
		widget := NewDelegationStatusWidget(v.delegationInfo, th)
		widget.SetFrame(v.tickFrame)
		sb.WriteString(widget.Render())
		sb.WriteString("\n")
	}

	if v.toolCallName != "" && v.toolCallStatus != "" {
		tcw := widgets.NewToolCallWidget(v.toolCallName, v.toolCallStatus)
		sb.WriteString(tcw.Render())
		sb.WriteString("\n")
	}
	if v.response != "" {
		mw := widgets.NewMessageWidget("assistant", v.response, th)
		if v.renderFunc != nil {
			mw.SetMarkdownRenderer(v.renderFunc)
		}
		sb.WriteString(mw.Render(width))
		sb.WriteString("\n")
	}
}

// ResultSend represents the result of sending a chat message.
type ResultSend struct {
	Message string
}

// ResultCancel represents the result of cancelling a chat operation.
type ResultCancel struct{}
