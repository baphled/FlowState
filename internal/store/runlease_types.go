package store

import "sync"

// SessionBusyError is the refusal of AcquireRunLease: the item is held, or its
// row cannot be judged, so nothing was taken. errors.Is(err, ErrSessionBusy)
// matches it and errors.As recovers the holder fields. The holder fields come
// from the lease row and are empty or zero when no row was read.
type SessionBusyError struct {
	// Item is the lease item that was refused (for example "exec:<id>").
	Item string
	// Owner is the holder recorded in the lease row, "" when there is none.
	Owner string
	// Host is the holder's host name, display only, never compared.
	Host string
	// Pid is the holder's process id, display only, never compared.
	Pid int64
	// AcquiredAtUTC is when the row was written.
	AcquiredAtUTC string
	// Unjudgeable is true when the row cannot be judged alive or dead (another
	// machine, or an empty machine from the legacy acquire) rather than held.
	Unjudgeable bool
}

// RunLease is the handle AcquireRunLease returns: it holds the item's advisory
// lock and the lease row. The caller owns it until Release; there is no
// finalizer, so a dropped handle keeps the lock until the process exits or the
// garbage collector closes the underlying file, which silently frees it. Keep
// it reachable for the whole run, and for the writes that follow the loop.
type RunLease struct {
	store   *Store
	lock    *LeaseLock
	item    string
	owner   string
	token   int64
	machine string
	host    string
	pid     int64

	mu       sync.Mutex
	released bool
}
