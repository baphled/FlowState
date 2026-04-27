// Package compaction implements RLM Phase A Layer 1: micro-compaction.
//
// The architecture follows the hot/cold split described in the FlowState
// KB note "Claude-Context-Compression-Architecture":
//
//   - Hot tail: the most-recent compactable tool results stay verbatim in
//     the request slice the provider sees.
//   - Cold storage: older compactable tool results are written to disk as
//     plain UTF-8 .txt files at <storeDir>/<message-id>.txt and replaced
//     in the slice by a one-line reference message that names the relpath.
//
// Compaction is view-only: the persisted history (FileContextStore /
// session.Messages) stays full and recoverable; only the in-flight
// provider request gets the rewritten slice. The agent re-reads cold
// content on demand using the existing read tool — no bespoke
// "uncompact" tool is shipped in Phase A.
//
// Tool classification:
//
//   - Compactable (offloaded when older than the hot tail): read, bash,
//     grep, glob, web, websearch, edit, multiedit, ls, apply_patch.
//   - Non-compactable (NEVER offloaded): delegate, skill_load,
//     coordination_store, todowrite, todoread, plan_enter, plan_exit,
//     plan_list, plan_read, plan_write, chain_get_messages,
//     chain_search_context, batch, question.
//
// User and assistant text messages are likewise NEVER touched by Phase A;
// Layer 2 (auto-compaction) and Layer 3 (incremental fact extraction)
// own those concerns.
//
// Out of scope for Phase A:
//
//   - Layer 2 structured-summary auto-compaction.
//   - Layer 3 incremental fact extraction.
//   - Layer 4 server-side context-editing API.
//   - Compaction of non-tool messages.
//   - On-demand rehydration tool (the agent re-reads via existing tools).
package compaction
