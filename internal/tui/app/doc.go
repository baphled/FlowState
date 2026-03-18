// Package app provides the root Bubble Tea application model for FlowState TUI.
//
// The App struct implements the tea.Model interface and delegates all
// Bubble Tea lifecycle methods (Init, Update, View) to the active Intent.
// This enables seamless intent switching without restarting the TUI.
package app
