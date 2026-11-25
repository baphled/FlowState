package app

// SessionDiagnosis is the read-only diagnosis of one session's
// durable state for the `session doctor` CLI surface (ADR-006
// follow-up): the raw row status, the crash classification, whether
// the event log shows run activity, the pending steering-message
// count and the budget snapshot. Values are a point-in-time
// aggregation only; the doctor never mutates the session, and
// recovery stays in the run preamble.
type SessionDiagnosis struct {
	// SessionID echoes the diagnosed session id.
	SessionID string
	// Status is the raw persisted row status, never remapped.
	Status string
	// Crashed is the crash-recovery predicate ANDed with an active
	// row status: only a session the engine could still resume is
	// reported as crashed.
	Crashed bool
	// RunActivity reports provider/tool traffic in the event log —
	// the same evidence DetectCrashedSession uses to separate a
	// crashed run from a freshly created session.
	RunActivity bool
	// PendingMessages counts undelivered steering messages.
	PendingMessages int
	// Reserved is the outstanding reserved budget total.
	Reserved int64
	// Pool is the declared budget pool anchor, nil without a pool.
	Pool *int64
	// Available is pool minus reserved, nil without a pool.
	Available *int64
	// FinishReason is the finish_reason of the session's LAST turn.end
	// event (PR #358), empty when none was recorded or the payload is
	// unparseable. Informational: it is not gated on the row status.
	FinishReason string
	// Truncated is true exactly when the last turn.end finished with
	// provider.FinishLength (answer cut off at the token limit) and the
	// session is neither failed, cancelled, paused nor crashed.
	Truncated bool
	// Usage is the provider usage the event log records; see SessionUsage.
	Usage SessionUsage
	// Compactions summarises the log's context.compacted events; see
	// CompactionStats.
	Compactions CompactionStats
}
