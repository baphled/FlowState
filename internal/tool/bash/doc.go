// Package bash provides a tool for executing bash commands with timeout.
//
// This package implements a tool that:
//   - Executes bash commands in a controlled environment
//   - Enforces timeouts to prevent runaway processes
//   - Captures stdout and stderr output
//   - Returns structured results for agent consumption
//
// Security note: This tool executes arbitrary commands; use with caution
// and appropriate permission controls.
package bash
