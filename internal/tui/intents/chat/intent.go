// Package chat provides the chat intent for FlowState TUI.
package chat

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/engine"
)

// Intent handles chat interactions in the TUI.
type Intent struct {
	engine    *engine.Engine
	agentID   string
	sessionID string
	messages  []string
	input     string
	mode      string
	streaming bool
	response  strings.Builder
	width     int
	height    int
}

// NewIntent creates a new chat intent with the given engine and agent.
func NewIntent(eng *engine.Engine, agentID string, sessionID string) *Intent {
	return &Intent{
		engine:    eng,
		agentID:   agentID,
		sessionID: sessionID,
		messages:  []string{},
		input:     "",
		mode:      "normal",
		streaming: false,
		width:     80,
		height:    24,
	}
}

// Init returns the initial command for the intent.
func (i *Intent) Init() tea.Cmd {
	return nil
}

// Update processes a Bubble Tea message and returns any command to execute.
func (i *Intent) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return i.handleKeyMsg(msg)
	case tea.WindowSizeMsg:
		i.width = msg.Width
		i.height = msg.Height
		return nil
	}
	return nil
}

func (i *Intent) handleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyCtrlC:
		return tea.Quit
	case tea.KeyEscape:
		if i.mode == "insert" {
			i.mode = "normal"
		}
		return nil
	case tea.KeyBackspace:
		if i.mode == "insert" && i.input != "" {
			i.input = i.input[:len(i.input)-1]
		}
		return nil
	case tea.KeyEnter:
		if i.mode == "insert" && i.input != "" {
			return i.sendMessage()
		}
		return nil
	case tea.KeyRunes:
		return i.handleRunes(msg.Runes)
	}
	return nil
}

func (i *Intent) handleRunes(runes []rune) tea.Cmd {
	if i.mode == "normal" {
		if len(runes) == 1 {
			switch runes[0] {
			case 'i':
				i.mode = "insert"
				return nil
			case 'q':
				return tea.Quit
			}
		}
		return nil
	}

	i.input += string(runes)
	return nil
}

func (i *Intent) sendMessage() tea.Cmd {
	i.messages = append(i.messages, "> "+i.input)
	i.input = ""
	i.streaming = true
	return nil
}

// View renders the chat interface as a string.
func (i *Intent) View() string {
	var builder strings.Builder

	for _, msg := range i.messages {
		builder.WriteString(msg)
		builder.WriteString("\n")
	}

	if i.response.Len() > 0 {
		builder.WriteString(i.response.String())
		builder.WriteString("\n")
	}

	builder.WriteString("\n")
	builder.WriteString("> ")
	builder.WriteString(i.input)
	builder.WriteString("\n\n")

	if i.mode == "normal" {
		builder.WriteString("[NORMAL] q: quit | i: insert mode")
	} else {
		builder.WriteString("[INSERT] Esc: normal mode | Enter: send")
	}

	return builder.String()
}

// Mode returns the current input mode.
func (i *Intent) Mode() string {
	return i.mode
}

// Input returns the current input text.
func (i *Intent) Input() string {
	return i.input
}

// Messages returns all messages in the chat history.
func (i *Intent) Messages() []string {
	return i.messages
}

// IsStreaming returns whether the intent is currently streaming a response.
func (i *Intent) IsStreaming() bool {
	return i.streaming
}

// Width returns the current terminal width.
func (i *Intent) Width() int {
	return i.width
}

// Height returns the current terminal height.
func (i *Intent) Height() int {
	return i.height
}
