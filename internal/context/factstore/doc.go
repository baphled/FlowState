// Package factstore implements RLM Phase B Layer 3: incremental fact
// extraction.
//
// The architecture follows the third tier of the FlowState KB note
// "Claude-Context-Compression-Architecture": durable, single-sentence
// claims are pulled from session text and stored per-session in a
// JSONL file. The engine recalls the top-K query-relevant facts and
// prepends them to the provider request as a "[recalled facts]" system
// block, displacing verbose history.
//
// Storage layout:
//
//   - <storeRoot>/<sessionID>/facts.jsonl — append-only JSONL.
//   - File mode 0o600; parent directory 0o700.
//
// Fact shape: {id, text, source_message_id, session_id, created_at}.
// Embeddings are deferred to Phase C; Phase B's recall ranker is pure
// keyword overlap with a recency tie-breaker.
//
// The two load-bearing interfaces (FactExtractor and FactStore) are
// narrow on purpose so Phase C can plug in an LLM-driven extractor and
// a vector-backed store without touching the engine wire-in.
//
// Out of scope for Phase B:
//
//   - LLM-driven extraction (Phase C).
//   - Vector embeddings or Qdrant integration (Phase C/D).
//   - Cross-session fact sharing.
//   - Re-extraction from cold-storage payloads (the regex extractor
//     intentionally only walks user/assistant text).
package factstore
