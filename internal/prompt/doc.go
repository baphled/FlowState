// Package prompt provides embedded prompt management for FlowState agents.
//
// This package handles the core prompt abstraction, including:
//   - Loading agent prompts from embedded markdown files
//   - Querying available prompts by agent ID
//   - Listing all available agent prompts
//
// Prompts are embedded at compile time using go:embed, ensuring they are
// available in production binaries without external file dependencies.
package prompt
