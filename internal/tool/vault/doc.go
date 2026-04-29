// Package vault provides the mcp_vault-rag_query_vault native tool for querying
// the indexed Obsidian vault without an external MCP server round-trip.
//
// The tool wraps vaultindex.QueryHandler directly so agents can list
// "mcp_vault-rag_query_vault" in capabilities.tools and receive local Qdrant
// search results from the vault collection in the same naming convention used
// by OpenCode's vault-rag MCP server.
package vault
