// Package vaultindex provides Obsidian-vault indexing and retrieval primitives for the FlowState vault MCP server.
//
// This package handles the building blocks behind cmd/flowstate-vault-server, including:
//   - Walking a vault root for markdown files
//   - Token-aware chunking with configurable size and overlap
//   - Persisting incremental-index state in a JSON sidecar keyed on file mtime
//   - Embedding chunks via an injected provider and upserting into Qdrant
//   - Serving a `query_vault` MCP tool that returns the chunk-shape contract
//     consumed by internal/recall/vault.Source
package vaultindex
