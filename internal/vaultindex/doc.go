// Package vaultindex provides Obsidian-vault indexing and retrieval primitives consumed by the in-process vault-rag surface.
//
// The package is consumed in three places:
//   - internal/app/app.go buildVaultQueryHandler — wires the mcp_vault-rag_query_vault read tool.
//   - internal/tool/vault — surfaces the vault_index / vault_sync admin tools and the query_vault read tool through the FlowState tool registry.
//   - internal/cli/vault.go — the `flowstate vault` operator CLI.
//
// It handles the building blocks each of those callers compose:
//   - Walking a vault root for markdown files
//   - Token-aware chunking with configurable size and overlap
//   - Persisting incremental-index state in a JSON sidecar keyed on file mtime
//   - Embedding chunks via an injected provider and upserting into Qdrant
//   - Serving a `query_vault` MCP tool that returns the chunk-shape contract
//     consumed by internal/recall/vault.Source
package vaultindex
