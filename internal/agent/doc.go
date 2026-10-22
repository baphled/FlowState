// Package agent provides agent manifest loading, validation, and registry management.
//
// This package handles the core agent abstraction for FlowState, including:
//   - Loading agent manifests from JSON or Markdown frontmatter files
//   - Validating manifest structure and required fields
//   - Maintaining a registry of available agents for discovery
//
// An agent manifest defines the complete configuration for a FlowState agent,
// including model preferences, capabilities, context management rules, and
// delegation policies.
package agent
