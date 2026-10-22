// Package orchestrator provides the canonical user-input to event-stream
// pipeline shared by every FlowState access method.
//
// This package handles:
//   - Processing user input through Orchestrator.ProcessUserInput
//   - Keeping API and TUI surfaces as thin wrappers over the orchestrator
//   - Preventing import cycles between internal/app and surface packages
package orchestrator
