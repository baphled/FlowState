// Package recall provides Recall Memory for FlowState agents — per-session and per-chain
// semantic search over conversation history.
//
// This package implements the Recall tier of the Letta/MemGPT memory taxonomy
// (Core → Recall → Archival), providing:
//   - File-backed message storage with embedding vectors (FileContextStore)
//   - Semantic search over conversation history (cosine similarity)
//   - Context query tools: search_context, get_messages, summarize_context
//   - Per-chain shared store for multi-agent delegation chains (ChainContextStore)
package recall
