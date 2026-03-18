package chat

import (
	"fmt"
	"strings"
)

type View struct {
	width  int
	height int
}

func NewView() *View {
	return &View{
		width:  80,
		height: 24,
	}
}

func (v *View) Width() int {
	return v.width
}

func (v *View) Height() int {
	return v.height
}

func (v *View) SetDimensions(width, height int) {
	v.width = width
	v.height = height
}

func (v *View) RenderContent(messages []string, input, mode string, streaming bool, response string) string {
	var sb strings.Builder

	for _, msg := range messages {
		sb.WriteString(msg)
		sb.WriteString("\n")
	}

	if streaming && response != "" {
		sb.WriteString(response)
		sb.WriteString("\n")
	}

	modeIndicator := "[NORMAL]"
	if mode == "insert" {
		modeIndicator = "[INSERT]"
	}
	sb.WriteString(modeIndicator)
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintf("> %s", input))

	return sb.String()
}

type ResultSend struct {
	Message string
}

type ResultCancel struct{}
