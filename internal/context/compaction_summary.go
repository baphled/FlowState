// Package context — Layer 2 (L2) auto-compaction summary type.
//
// This file defines the CompactionSummary value object used by Phase 2
// auto-compaction. It is the structured result produced by the summariser
// (see T9a SummariserResolver) from the LLM response to the T8 summary
// prompt, and consumed by downstream tasks (T10a persistence, T11 restore).
//
// The type is deliberately minimal: no methods, no validation, no helpers.
// Callers in later tasks will layer behaviour on top as concrete needs
// emerge. Until then, CompactionSummary is a plain data carrier whose
// sole contract is stable JSON round-trip.
//
// Constrained by:
//
//   - ADR - View-Only Context Compaction — summaries are artefacts; they
//     never mutate session.Messages.
//   - ADR - Tool-Call Atomicity in Context Compaction — summary token
//     accounting is unit-aligned (OriginalTokenCount reflects the
//     unit-aligned range that was summarised).
package context

import "time"

// CompactionSummary is the structured output of L2 auto-compaction for a
// contiguous range of compacted messages. It records both the semantic
// distillation (intent, decisions, errors, next steps, files) and the
// accounting required to report compression savings.
//
// JSON tags are explicit and lower_snake_case to match the on-disk format
// used by L1 artefacts (see CompactedMessage) and to keep the wire format
// stable across Go refactors.
type CompactionSummary struct {
	// Intent is a one-paragraph restatement of what the user was trying to
	// achieve across the summarised range.
	Intent string `json:"intent"`
	// KeyDecisions lists the substantive choices made — architectural
	// commitments, trade-offs accepted, approaches rejected.
	KeyDecisions []string `json:"key_decisions"`
	// Errors records failures, blockers, or corrections encountered in the
	// summarised range. Preserved verbatim so later turns can reason about
	// what has already gone wrong.
	Errors []string `json:"errors"`
	// NextSteps captures outstanding TODOs and planned follow-ups at the
	// moment of compaction. Used by restore (T11) to seed continuation.
	NextSteps []string `json:"next_steps"`
	// FilesToRestore is the allow-list of file paths that must be re-read
	// on rehydration. Paths are absolute, as emitted by earlier turns.
	FilesToRestore []string `json:"files_to_restore"`
	// CompactedAt is the wall-clock time the summary was produced.
	//
	// Server-only: the JSON tag is `-` so the field is never emitted on
	// marshal and never read on unmarshal. AutoCompactor.Compact stamps
	// it from time.Now().UTC() after a successful summariser response
	// parse. The earlier schema shipped `json:"compacted_at"` without
	// `omitempty`, which meant a drifting summariser could emit a
	// legitimately-shaped RFC3339 value that silently populated the
	// struct before being overwritten — the overwrite was correct but
	// the field's provenance was not visible in the type. Making the
	// field wire-invisible is the honest contract: callers must treat
	// it as server-authoritative.
	CompactedAt time.Time `json:"-"`
	// OriginalTokenCount is the total token count of the pre-summary range.
	OriginalTokenCount int `json:"original_token_count"`
	// SummaryTokenCount is the token count of the produced summary text
	// (intent + decisions + errors + next_steps + files serialised).
	SummaryTokenCount int `json:"summary_token_count"`
}
