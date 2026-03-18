package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

type ChunkMsg provider.StreamChunk

type StreamDoneMsg struct{}

type ErrorMsg struct{ Err error }

type Model struct {
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
	err       error
}

func NewModel(eng *engine.Engine, agentID string, sessionID string) *Model {
	return &Model{
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

func (m *Model) Init() tea.Cmd {
	return nil
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case ChunkMsg:
		m.streaming = true
		m.response.WriteString(msg.Content)
		return m, nil
	case StreamDoneMsg:
		m.streaming = false
		if m.response.Len() > 0 {
			m.messages = append(m.messages, m.response.String())
			m.response.Reset()
		}
		return m, nil
	case ErrorMsg:
		m.err = msg.Err
		return m, nil
	}
	return m, nil
}

func (m *Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEscape:
		if m.mode == "insert" {
			m.mode = "normal"
		}
		return m, nil
	case tea.KeyBackspace:
		if m.mode == "insert" && m.input != "" {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	case tea.KeyEnter:
		if m.mode == "insert" && m.input != "" {
			cmd := m.sendMessage()
			return m, cmd
		}
		return m, nil
	case tea.KeyRunes:
		return m.handleRunes(msg.Runes)
	}
	return m, nil
}

func (m *Model) handleRunes(runes []rune) (tea.Model, tea.Cmd) {
	if m.mode == "normal" {
		if len(runes) == 1 {
			switch runes[0] {
			case 'i':
				m.mode = "insert"
				return m, nil
			case 'q':
				return m, tea.Quit
			}
		}
		return m, nil
	}

	m.input += string(runes)
	return m, nil
}

func (m *Model) sendMessage() tea.Cmd {
	message := m.input
	m.messages = append(m.messages, "> "+message)
	m.input = ""
	m.streaming = true

	return func() tea.Msg {
		ctx := context.Background()
		chunks, err := m.engine.Stream(ctx, m.agentID, message)
		if err != nil {
			return ErrorMsg{Err: err}
		}

		go func() {
			for chunk := range chunks {
				if chunk.Error != nil {
					return
				}
			}
		}()

		return nil
	}
}

func (m *Model) View() string {
	var builder strings.Builder

	for _, msg := range m.messages {
		builder.WriteString(msg)
		builder.WriteString("\n")
	}

	if m.response.Len() > 0 {
		builder.WriteString(m.response.String())
		builder.WriteString("\n")
	}

	builder.WriteString("\n")
	builder.WriteString("> ")
	builder.WriteString(m.input)
	builder.WriteString("\n\n")

	if m.mode == "normal" {
		builder.WriteString("[NORMAL] q: quit | i: insert mode")
	} else {
		builder.WriteString("[INSERT] Esc: normal mode | Enter: send")
	}

	return builder.String()
}

func (m *Model) Mode() string {
	return m.mode
}

func (m *Model) Input() string {
	return m.input
}

func (m *Model) IsStreaming() bool {
	return m.streaming
}

func (m *Model) Width() int {
	return m.width
}

func (m *Model) Height() int {
	return m.height
}

func (m *Model) ResponseContent() string {
	return m.response.String()
}

func (m *Model) Messages() []string {
	return m.messages
}

func (m *Model) Error() error {
	return m.err
}
