// Package intents provides shared types and interfaces for FlowState TUI intents.
package intents

import tea "github.com/charmbracelet/bubbletea"

// IntentResult represents the outcome of an intent's operations.
//
// Intents use IntentResult to communicate outcomes (data, actions, state changes)
// to the application shell without mutating shared state directly.
type IntentResult struct {
	Data   interface{}
	Action string
	Error  error
}

// Intent defines the contract for workflow orchestrators in the TUI.
//
// Unlike tea.Model, Intent.Update returns only tea.Cmd. The App shell
// manages the tea.Model contract and delegates to the active Intent.
type Intent interface {
	// Init initialises the intent.
	Init() tea.Cmd
	// Update handles messages and returns a command.
	Update(msg tea.Msg) tea.Cmd
	// View renders the intent as a string.
	View() string
	// Result returns the current outcome state of the intent.
	Result() *IntentResult
}
