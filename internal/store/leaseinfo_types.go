package store

// LeaseInfo is a lease row as ReadLease reports it: who holds the item, the
// fencing token, the machine identity and the display-only host and pid that
// ADR-011 records, and when the lease was acquired. A row written by the legacy
// AcquireLease has an empty Machine, Host and zero Pid.
type LeaseInfo struct {
	// Item is the leased item, for example "exec:<session id>".
	Item string
	// Owner is the agent that holds the lease.
	Owner string
	// Token is the fencing token.
	Token int64
	// Machine is the hashed machine identity, empty for a legacy row.
	Machine string
	// Host is the host name, for display only.
	Host string
	// Pid is the holder's process id, for display only.
	Pid int64
	// AcquiredAt is the acquisition time as stored (UTC, RFC 3339).
	AcquiredAt string
}
