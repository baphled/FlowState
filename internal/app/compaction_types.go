package app

import "github.com/baphled/flowstate/internal/provider"

// TypeContextCompacted is the event type of a context compaction (ADR-010): its summary
// replaces the conversation before first_kept_seq in the rebuilt history. The actor is
// session.ActorEngine.
const TypeContextCompacted = "context.compacted"

// Compaction reasons recorded on a context.compacted event (ADR-010).
const (
	// CompactionReasonAuto marks a compaction the trigger started.
	CompactionReasonAuto = "auto"
	// CompactionReasonManual marks a compaction an operator asked for.
	CompactionReasonManual = "manual"
	// CompactionReasonOverflow marks a compaction after a provider context-overflow error.
	CompactionReasonOverflow = "overflow"
)

// MaxCompactionSummaryBytes bounds a summary: 8192 tokens at the ADR-010 estimate of four
// bytes per token. The bound is in bytes because the log, not a tokeniser, is what is read back.
const MaxCompactionSummaryBytes = 8192 * 4

// CompactionPayload is the payload of a context.compacted event (ADR-010, "How: the
// event"). The event never rewrites the log: the rebuilt history is the summary followed by
// the events from FirstKeptSeq on.
type CompactionPayload struct {
	// Summary is the summary text, non-empty, at most MaxCompactionSummaryBytes and without
	// the line that closes the summary fence.
	Summary string `json:"summary"`
	// FirstKeptSeq is the seq of the first event the rebuild replays verbatim; at least 1
	// (the first turn.start is at seq 0), less than the seq of the event that carries it and
	// not below the previous compaction's.
	FirstKeptSeq int64 `json:"first_kept_seq"`
	// TokensBefore is the estimated context replaced; never negative.
	TokensBefore int `json:"tokens_before"`
	// TokensAfter is the estimated context of the rebuilt history; never negative.
	TokensAfter int `json:"tokens_after"`
	// Reason is one of the CompactionReason constants.
	Reason string `json:"reason"`
	// Model is the model that wrote the summary.
	Model string `json:"model"`
	// Usage is the summarising call's usage, valid per provider.Usage.Validate; nil, and absent from the payload, when the
	// provider reported none.
	Usage *provider.Usage `json:"usage,omitempty"`
}

// CompactionStats is what the session doctor reports about a log's compactions. The
// summary token totals come from the compaction events' own usage and are separate from the
// turn totals in SessionUsage.
type CompactionStats struct {
	// Count is the number of valid context.compacted events.
	Count int64
	// Invalid is the number of context.compacted events that fail decode or ValidateCompaction,
	// or whose first_kept_seq is below the previous valid one's. Above 0 the rebuild fails, so
	// `session run` refuses the session with replay_mismatch.
	Invalid int64
	// LastTokensAfter is the tokens_after of the last valid event; nil when there is none.
	LastTokensAfter *int64
	// SummariesReported is how many valid events carried usage.
	SummariesReported int64
	// SummaryInputTokens sums the input tokens of the summarising calls (cache tokens are
	// not folded in, as in UsageTotals); meaningful only when SummariesReported is above 0.
	SummaryInputTokens int64
	// SummaryOutputTokens sums the output tokens of the summarising calls.
	SummaryOutputTokens int64
}
