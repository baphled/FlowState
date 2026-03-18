// Package tui provides terminal user interface components.
package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

// ChunkMsg wraps a stream chunk for Bubble Tea message handling.
type ChunkMsg provider.StreamChunk

// StreamDoneMsg signals that streaming has completed.
type StreamDoneMsg struct{}

// ErrorMsg wraps an error for Bubble Tea message handling.
type ErrorMsg struct{ Err error }

// Model is the Bubble Tea model for the chat interface.
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
	chunks    <-chan provider.StreamChunk
}

// NewModel creates a new chat model with the given engine and agent.
//
// Expected:
//   - eng is a non-nil Engine for handling chat requests.
//   - agentID identifies the agent to converse with.
//   - sessionID is the session identifier for context persistence.
//
// Returns:
//   - A configured Model ready for Bubble Tea initialisation.
//
// Side effects:
//   - None.
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

// Init returns the initial command for the Bubble Tea model.
//
// Returns:
//   - nil, as no initial command is needed.
//
// Side effects:
//   - None.
func (m *Model) Init() tea.Cmd {
	return nil
}

// Update processes a Bubble Tea message and returns the updated model.
//
// Expected:
//   - msg is a tea.Msg to handle (key press, window resize, chunk, etc.).
//
// Returns:
//   - The updated Model and any command to execute next.
//
// Side effects:
//   - May initiate streaming requests or update internal state.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case streamStartedMsg:
		m.chunks = msg.chunks
		return m, waitForChunk(m.chunks)
	case ChunkMsg:
		m.streaming = true
		m.response.WriteString(msg.Content)
		return m, waitForChunk(m.chunks)
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

// handleKeyMsg processes keyboard input and returns appropriate commands.
//
// Expected:
//   - msg is a tea.KeyMsg containing key press information.
//
// Returns:
//   - The updated Model and any command to execute.
//
// Side effects:
//   - May change the input mode, modify input text, or trigger message sending.
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

// handleRunes processes character input in normal and insert modes.
//
// Expected:
//   - runes is a slice of rune characters to process.
//
// Returns:
//   - The updated Model and any command to execute.
//
// Side effects:
//   - May change the input mode or append characters to the input buffer.
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

// sendMessage initiates a streaming request to the engine with the current input.
//
// Returns:
//   - A tea.Cmd that executes the streaming request and returns a streamStartedMsg.
//
// Side effects:
//   - Appends the message to the chat history, clears the input buffer, and sets streaming flag.
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
		return streamStartedMsg{chunks: chunks}
	}
}

// streamStartedMsg signals that a streaming response has begun.
type streamStartedMsg struct {
	chunks <-chan provider.StreamChunk
}

// waitForChunk waits for the next chunk from the streaming channel.
//
// Expected:
//   - chunks is a channel of StreamChunk values to read from.
//
// Returns:
//   - A tea.Cmd that blocks until a chunk arrives or the channel closes.
//
// Side effects:
//   - Reads from the chunks channel; returns ChunkMsg, ErrorMsg, or StreamDoneMsg.
func waitForChunk(chunks <-chan provider.StreamChunk) tea.Cmd {
	return func() tea.Msg {
		chunk, ok := <-chunks
		if !ok {
			return StreamDoneMsg{}
		}
		if chunk.Error != nil {
			return ErrorMsg{Err: chunk.Error}
		}
		return ChunkMsg(chunk)
	}
}

// View renders the chat interface as a string.
//
// Returns:
//   - The rendered string representation of the chat UI.
//
// Side effects:
//   - None.
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

// Mode returns the current input mode.
//
// Returns:
//   - The mode string, either "normal" or "insert".
//
// Side effects:
//   - None.
func (m *Model) Mode() string {
	return m.mode
}

// Input returns the current input text.
//
// Returns:
//   - The current user input string.
//
// Side effects:
//   - None.
func (m *Model) Input() string {
	return m.input
}

// IsStreaming returns whether the model is currently streaming a response.
//
// Returns:
//   - True if a streaming response is in progress.
//
// Side effects:
//   - None.
func (m *Model) IsStreaming() bool {
	return m.streaming
}

// Width returns the current terminal width.
//
// Returns:
//   - The terminal width in columns.
//
// Side effects:
//   - None.
func (m *Model) Width() int {
	return m.width
}

// Height returns the current terminal height.
//
// Returns:
//   - The terminal height in rows.
//
// Side effects:
//   - None.
func (m *Model) Height() int {
	return m.height
}

// ResponseContent returns the current streaming response content.
//
// Returns:
//   - The accumulated response text from the current stream.
//
// Side effects:
//   - None.
func (m *Model) ResponseContent() string {
	return m.response.String()
}

// Messages returns all messages in the chat history.
//
// Returns:
//   - A slice of message strings in chronological order.
//
// Side effects:
//   - None.
func (m *Model) Messages() []string {
	return m.messages
}

// Error returns the last error encountered during streaming.
//
// Returns:
//   - The most recent error, or nil if no error occurred.
//
// Side effects:
//   - None.
func (m *Model) Error() error {
	return m.err
}

// SetChunks sets the chunks channel for testing purposes.
//
// Expected:
//   - chunks is a channel of StreamChunk values to read from.
//
// Side effects:
//   - Replaces the model's internal chunks channel.
func (m *Model) SetChunks(chunks <-chan provider.StreamChunk) {
	m.chunks = chunks
}
