// Package tooldisplay provides shared formatting logic for tool call display.
//
// This package centralises the mapping from tool name to its primary argument
// key and the construction of human-readable tool call summaries. It deduplicates
// three near-identical implementations previously found across the codebase:
//
//   - internal/tui/intents/chat/intent.go (toolCallArgKey, toolCallSummary)
//   - internal/session/manager.go (extractPrimaryArg)
//
// Key functions:
//   - PrimaryArgKey maps a tool name to its primary display argument key.
//   - Summary formats a tool call as "name: primaryArg" for display.
package tooldisplay
