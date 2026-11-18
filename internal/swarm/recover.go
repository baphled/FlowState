package swarm

import "fmt"

// Resume decides whether a session may resume from cp given the
// lease fence currently in force for it (currentFence). Recovery is
// granted only when the checkpoint matches the session and its fence
// is still current; anything else is refused with a reason.
func (Recover) Resume(sessionID string, cp Checkpoint, currentFence int64) (RecoveryResult, error) {
	if sessionID == "" {
		return RecoveryResult{}, fmt.Errorf("swarm: session id must not be empty")
	}
	if cp.SessionID != sessionID {
		return RecoveryResult{Reason: "checkpoint belongs to another session"}, nil
	}
	if cp.LeaseFence != currentFence {
		return RecoveryResult{Reason: "lease fence moved on"}, nil
	}
	return RecoveryResult{Resumed: true}, nil
}
