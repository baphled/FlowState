package app

// UnlockOutcome says what UnlockSession did to the session's lease row.
type UnlockOutcome string

// The outcomes of UnlockSession.
const (
	// UnlockNoLease means the session has no exec lease row: nothing to do.
	UnlockNoLease UnlockOutcome = "no_lease"
	// UnlockReported means a row exists and nothing was deleted (no force).
	UnlockReported UnlockOutcome = "reported"
	// UnlockDeleted means force deleted the row it had read.
	UnlockDeleted UnlockOutcome = "deleted"
)

// UnlockHolder is the lease row as UnlockSession read it.
type UnlockHolder struct {
	// Owner is the lease owner, for example "run:<uuid>" or the legacy "cli".
	Owner string `json:"owner"`
	// Host is the recorded host name, display only.
	Host string `json:"host"`
	// Pid is the recorded process id, display only.
	Pid int64 `json:"pid"`
	// Token is the fencing token read.
	Token int64 `json:"token"`
	// AcquiredAt is the stored acquisition time.
	AcquiredAt string `json:"acquired_at"`
}

// UnlockReport is the typed result of UnlockSession.
type UnlockReport struct {
	// SessionID is the session asked about.
	SessionID string
	// Item is the lease item, "exec:<session id>".
	Item string
	// Outcome says whether there was no row, a report only, or a deletion.
	Outcome UnlockOutcome
	// Holder is the row read; zero when Outcome is UnlockNoLease.
	Holder UnlockHolder
	// MachineMatches is true when the row's machine identity is this machine's.
	MachineMatches bool
	// LegacyRow is true when the row has no machine: it was written before
	// ADR-011 or by the legacy acquire, holds no lock and cannot be judged.
	LegacyRow bool
	// EventAppended is true when a session.unlocked event was written. It is
	// false for a terminal session, whose log is not appended to (ADR-011).
	EventAppended bool
	// Message is the operator-facing text, including the warning that force
	// cannot tell whether a row it did not lock is still running.
	Message string
}

// UnlockedPayload is the payload of the session.unlocked event (ADR-011).
type UnlockedPayload struct {
	Owner          string `json:"owner"`
	Host           string `json:"host"`
	Pid            int64  `json:"pid"`
	Token          int64  `json:"token"`
	Forced         bool   `json:"forced"`
	MachineMatches bool   `json:"machine_matches"`
	LegacyRow      bool   `json:"legacy_row"`
}

// LeaseLiveError is returned when the lease's lock is held on this machine: a
// live holder, which unlock never overrides, with or without force. It matches
// ErrLeaseLive under errors.Is.
type LeaseLiveError struct {
	// SessionID is the session whose lease is live.
	SessionID string
	// Holder is the row of the live holder.
	Holder UnlockHolder
}
