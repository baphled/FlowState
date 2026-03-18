// Package app provides the root Bubble Tea model for FlowState TUI.
package app

import tea "github.com/charmbracelet/bubbletea"

// Intent defines the contract for workflow screens in the TUI.
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
}
