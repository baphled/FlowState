// Package adapters bridges the engine event stream to Bubble Tea messages.
//
// This package provides adapters that convert structured engine events into
// tea.Msg values consumable by the TUI update loop, including:
//   - EventToMsg: converts DelegationEvent, StatusTransitionEvent, and other
//     stream events into typed tea.Msg values
//   - Verbosity-aware filtering before dispatch to the TUI
package adapters
