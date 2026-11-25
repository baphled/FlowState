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
	// the exec-loop `session run` (--allow and the other tool-loop flags) refuses the session with
	// replay_mismatch; the plain text turn never replays the log, so it is unaffected.
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

// Failure reasons of a compaction (CompactionFailure.Reason).
const (
	// CompactReasonNoCut marks a compaction with no eligible cut point: nothing new to compact.
	CompactReasonNoCut = "no_cut"
	// CompactReasonSummaryEmpty marks a summarising call that returned no summary text.
	CompactReasonSummaryEmpty = "summary_empty"
	// CompactReasonSummaryTruncated marks a summary that finished at the token cap (finish length) or ended with no finish event.
	CompactReasonSummaryTruncated = "summary_truncated"
	// CompactReasonSummaryInvalid marks a summary over the cap or holding the fence's closing line.
	CompactReasonSummaryInvalid = "summary_invalid"
	// CompactReasonProviderError marks a failed summarising call.
	CompactReasonProviderError = "provider_error"
	// CompactReasonCancelled marks a context cancelled before the event was appended.
	CompactReasonCancelled = "cancelled"
	// CompactReasonContextTooLarge marks a compaction whose tokens_after is still above the line.
	CompactReasonContextTooLarge = "context_too_large"
	// CompactReasonLogChanged marks a log another writer appended to while the summary was written.
	CompactReasonLogChanged = "log_changed"
)

// CompactKeepRecentDefault is the keep-recent budget in estimated tokens (ADR-010 D7).
const CompactKeepRecentDefault = 20000

// CompactSummaryMaxTokens caps the summarising call's response (ADR-010 D6).
const CompactSummaryMaxTokens int64 = 8192

const (
	// CompactResultClip is the length in characters at which a tool result is cut in the span
	// sent to the summariser.
	CompactResultClip = 2000
	// CompactArgsClip is the length in characters at which a call's arguments are cut there.
	CompactArgsClip = 500
	// CompactTextClip is the length in characters at which a user or assistant text is cut there.
	CompactTextClip = 8192
	// CompactRequestMaxBytes caps the user message of the summarising call (about 100k tokens at
	// four bytes each). Entries of the span beyond it are left out from the old end.
	CompactRequestMaxBytes = 400 * 1024
)

// MaxCompactInstructions is the longest `--instructions` text accepted, in bytes.
const MaxCompactInstructions = 1000

// CompactLease is the exec lease the caller holds for the compaction; Compact fences it
// immediately before the append, so a lease taken over during the summary call appends nothing.
type CompactLease struct {
	// Item and Agent name the lease, Token is the fencing token the caller was given.
	Item  string
	Agent string
	Token int64
}

// CompactOptions configures Compact.
type CompactOptions struct {
	// Streamer and Model are the session's own provider and model (ADR-010 D5).
	Streamer provider.Streamer
	Model    string
	// Instructions is an optional focus line appended to the prompt: one line, at most
	// MaxCompactInstructions bytes, no control characters.
	Instructions string
	// Reason is the compaction reason recorded; empty means CompactionReasonManual.
	Reason string
	// KeepRecent is the keep-recent budget in estimated tokens; <= 0 means CompactKeepRecentDefault.
	KeepRecent int
	// Lease is the exec lease held by the caller; it is required.
	Lease CompactLease
	// OverheadTokens is the estimate of what each request carries besides the history (the
	// system prompt and the tool definitions); it is added to tokens_after, and to tokens_before
	// when that is estimated, so the figures compare with a provider-reported context.
	OverheadTokens int
	// MaxTokensAfter, when positive, fails the compaction with context_too_large if the
	// estimated tokens_after is above it. Nothing is written.
	MaxTokensAfter int
}

// CompactionResult reports an appended compaction.
type CompactionResult struct {
	SessionID    string
	EventSeq     int64
	FirstKeptSeq int64
	TokensBefore int
	TokensAfter  int
	Reason       string
	Model        string
	// Usage is the summarising call's usage; nil when the provider reported none.
	Usage *provider.Usage
}

// CompactLeaseHeld is the refusal of a compaction on a session whose exec lease row is held. It
// matches ErrExecLoopFenced, so its code and exit status are those of `session run`'s refusal.
type CompactLeaseHeld struct {
	// SessionID is the session that is in use.
	SessionID string
	// Holder is the owner of the lease row.
	Holder string
}

// CompactionFailure is the error of a compaction that wrote nothing. It matches
// ErrCompactionFailed and, by reason, ErrProvider (provider_error and the summary_* reasons),
// session.ErrUsage (no_cut) or ErrSessionCancelled (cancelled), so ErrorCode classifies it.
type CompactionFailure struct {
	// Reason is one of the CompactReason constants.
	Reason string
	// Cause is the underlying error, if any.
	Cause error
}
