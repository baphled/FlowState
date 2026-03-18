package app

import tea "github.com/charmbracelet/bubbletea"

// ChatModel defines the interface for the chat model that ChatAdapter wraps.
// This allows the adapter to work with any model that implements these methods.
type ChatModel interface {
	// Init initialises the chat model.
	Init() tea.Cmd
	// Update handles messages and returns the updated model and command.
	Update(msg tea.Msg) (tea.Model, tea.Cmd)
	// View renders the chat model as a string.
	View() string
}

// ChatAdapter wraps a tea.Model (chat.Model) to satisfy the Intent interface.
type ChatAdapter struct {
	model ChatModel
}

// NewChatAdapter creates a ChatAdapter wrapping the given chat model.
func NewChatAdapter(model ChatModel) *ChatAdapter {
	return &ChatAdapter{model: model}
}

// Init delegates to the wrapped model.
func (a *ChatAdapter) Init() tea.Cmd {
	return a.model.Init()
}

// Update delegates to the wrapped model, discarding the returned model.
func (a *ChatAdapter) Update(msg tea.Msg) tea.Cmd {
	newModel, cmd := a.model.Update(msg)
	if m, ok := newModel.(ChatModel); ok {
		a.model = m
	}
	return cmd
}

// View delegates to the wrapped model.
func (a *ChatAdapter) View() string {
	return a.model.View()
}
