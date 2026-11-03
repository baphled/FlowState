package swarm

// Recovery of cancelled sessions: a checkpoint is only resumable
// when its lease fence is still current, and recovery never widens
// the scope the session held before cancellation.

// RecoveryResult reports the outcome of a recovery attempt.
type RecoveryResult struct {
	// Resumed is true when the session may continue from the checkpoint.
	Resumed bool
	// Reason explains a refusal (fail-closed only).
	Reason string
}

// Recover decides whether a session may resume from a checkpoint.
// It fails closed: recovery is refused when the checkpoint's session
// id is empty, when the checkpoint was not taken for that session,
// or when the lease fence moved on (another writer fenced the old
// lease, so stale state must not resume).
type Recover struct{}
