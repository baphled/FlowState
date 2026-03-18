// Package chat provides the chat view component for the TUI.
package chat

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

// Message represents a chat message with a role and content.
type Message struct {
	Role    string
	Content string
}

// View represents the chat view component with messages and input state.
type View struct {
	width      int
	height     int
	messages   []Message
	input      string
	mode       string
	streaming  bool
	response   string
	renderFunc func(string, int) string
}

// NewView creates a new chat view with default dimensions and markdown rendering.
func NewView() *View {
	return &View{
		width:      80,
		height:     24,
		renderFunc: renderMarkdown,
	}
}

// Width returns the current width of the view.
func (v *View) Width() int {
	return v.width
}

// Height returns the current height of the view.
func (v *View) Height() int {
	return v.height
}

// SetDimensions sets the width and height of the view.
func (v *View) SetDimensions(width, height int) {
	v.width = width
	v.height = height
}

// AddMessage appends a message to the view's message list.
func (v *View) AddMessage(msg Message) {
	v.messages = append(v.messages, msg)
}

// SetInput sets the current input text.
func (v *View) SetInput(input string) {
	v.input = input
}

// SetMode sets the current input mode (normal or insert).
func (v *View) SetMode(mode string) {
	v.mode = mode
}

// SetStreaming sets the streaming state and partial response content.
func (v *View) SetStreaming(streaming bool, response string) {
	v.streaming = streaming
	v.response = response
}

// SetMarkdownRenderer sets a custom function for rendering markdown content.
func (v *View) SetMarkdownRenderer(fn func(string, int) string) {
	v.renderFunc = fn
}

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

// RenderContent renders the chat view content including messages, streaming response, and input.
func (v *View) RenderContent(width int) string {
	var sb strings.Builder

	for _, msg := range v.messages {
		if msg.Role == "assistant" {
			sb.WriteString(v.renderFunc(msg.Content, width))
		} else {
			sb.WriteString(msg.Content)
		}
		sb.WriteString("\n")
	}

	if v.streaming && v.response != "" {
		sb.WriteString(v.response)
		sb.WriteString("\n")
	}

	modeIndicator := "[NORMAL]"
	if v.mode == "insert" {
		modeIndicator = "[INSERT]"
	}
	sb.WriteString(modeIndicator)
	sb.WriteString("\n")

	sb.WriteString("> " + v.input)

	return sb.String()
}

// ResultSend represents the result of sending a chat message.
type ResultSend struct {
	Message string
}

// ResultCancel represents the result of cancelling a chat operation.
type ResultCancel struct{}
