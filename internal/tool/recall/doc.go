// Package recall provides tools for querying per-chain shared Recall Memory context.
//
// This package implements the tool.Tool interface to expose chain-scoped context
// query operations to agents participating in a delegation chain. It provides:
//   - chain_search_context: semantic search over the full chain's conversational history
//   - chain_get_messages: retrieval of messages from a specific agent in the chain
//
// Both tools delegate to ChainContextStore from internal/recall, following the
// graceful degradation policy: if no embedding provider is configured, semantic search
// falls back to recency-ordered retrieval.
package recall
