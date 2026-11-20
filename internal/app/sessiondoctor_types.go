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
}
