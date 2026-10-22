// Package lsp provides Language Server Protocol (LSP) operations as a FlowState tool.
//
// This package enables code analysis and navigation features by exposing LSP-powered operations:
//   - Diagnostics: Retrieve errors, warnings, and hints from language servers
//   - Symbols: List document or workspace symbols for code navigation
//   - Goto Definition: Jump to symbol definitions
//   - Find References: Locate all usages of a symbol
//
// Responsibilities:
//   - Translate FlowState tool requests into LSP queries
//   - Marshal and unmarshal LSP-compatible data structures
//   - Integrate with the FlowState tool interface for discoverability and invocation
//   - Ensure robust error handling and clear, actionable responses
//
// This package does not implement a language server itself; it acts as a bridge to existing LSP servers.
package lsp
