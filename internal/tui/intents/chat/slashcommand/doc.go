// Package slashcommand wires the chat-input slash-command surface for the
// FlowState TUI. The package owns three concepts:
//
//   - Command: a single registered slash command with optional sub-picker
//     items and a handler.
//   - Registry: a concurrency-safe collection of commands with
//     case-insensitive prefix filtering.
//   - CommandContext: the narrow set of handles command handlers consume
//     when they execute (chat view writes, app shell access, registries).
//
// The reusable Picker widget lives in internal/tui/uikit/widgets and has
// no slash-command-specific knowledge — this package builds widgets.Item
// values from registered Commands and dispatches PickerEvents back to
// handlers.
package slashcommand
