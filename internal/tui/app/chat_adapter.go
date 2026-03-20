package app

import (
	tea "github.com/charmbracelet/bubbletea"

	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
)

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
//
// Expected:
//   - model is a non-nil ChatModel implementation.
//
// Returns:
//   - An initialised ChatAdapter wrapping the given model.
//
// Side effects:
//   - None.
func NewChatAdapter(model ChatModel) *ChatAdapter {
	return &ChatAdapter{model: model}
}

// Init delegates to the wrapped model.
//
// Returns:
//   - A tea.Cmd from the wrapped model's Init method.
//
// Side effects:
//   - Delegates to the wrapped model's Init method.
func (a *ChatAdapter) Init() tea.Cmd {
	return a.model.Init()
}

// Update delegates to the wrapped model, discarding the returned model.
//
// Expected:
//   - msg is a tea.Msg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd from the wrapped model's Update method.
//
// Side effects:
//   - Updates the wrapped model if the returned model implements ChatModel.
//   - Delegates to the wrapped model's Update method.
func (a *ChatAdapter) Update(msg tea.Msg) tea.Cmd {
	newModel, cmd := a.model.Update(msg)
	if m, ok := newModel.(ChatModel); ok {
		a.model = m
	}
	return cmd
}

// View delegates to the wrapped model.
//
// Returns:
//   - The rendered view string from the wrapped model.
//
// Side effects:
//   - None.
func (a *ChatAdapter) View() string {
	return a.model.View()
}

// Result returns the current outcome state of the adapter.
//
// Returns:
//   - nil (the wrapped ChatModel does not provide result information).
//
// Side effects:
//   - None.
func (a *ChatAdapter) Result() *tuiintents.IntentResult {
	return nil
}
