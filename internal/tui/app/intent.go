// Package app provides the root Bubble Tea model for FlowState TUI.
package app

import tea "github.com/charmbracelet/bubbletea"

// Intent defines the contract for workflow screens in the TUI.
//
// Unlike tea.Model, Intent.Update returns only tea.Cmd. The App shell
// manages the tea.Model contract and delegates to the active Intent.
type Intent interface {
	Init() tea.Cmd
	Update(msg tea.Msg) tea.Cmd
	View() string
}
