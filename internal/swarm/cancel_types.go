package swarm

// Cancellation and checkpoint for the M2 runtime: a parent cancels a
// subtree of child sessions and records a checkpoint that preserves
// the residual state recovery needs (remaining budget, lease fences).

// CancelTree records cancellation of a parent session and cascades it
// to every linked child session. Cancelling is idempotent per
// session; CancelTree fails closed on an empty session id.
type CancelTree struct {
	cancelled map[string]bool
}

// Checkpoint preserves the resumable state of one session at the
// moment of cancellation or compaction: remaining budget and the
// fencing token of the lease the session must reacquire.
type Checkpoint struct {
	// SessionID is the checkpointed session.
	SessionID string
	// RemainingBudget is the session's unspent budget.
	RemainingBudget int64
	// LeaseFence is the fencing token held at checkpoint time.
	LeaseFence int64
}
