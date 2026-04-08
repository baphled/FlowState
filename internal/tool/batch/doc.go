// Package batch provides concurrent execution of independent tool calls.
//
// This package handles:
//   - Looking up registered tools by name
//   - Executing tool calls in parallel
//   - Preserving individual tool outputs and failures
//   - Returning aggregated results for batch workflows
package batch
