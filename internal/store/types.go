package store

import (
	"sync"
	"time"
)

// Session is the persisted session record per DATA-MODEL §2.
type Session struct {
	ID        string
	CreatedAt time.Time
	UpdatedAt time.Time
	Status    string
	Provider  string
	ModelID   string
	Harness   string
	ConfigSHA string
}

// EventWriter is the single serialized event writer for one session
// (DATA-MODEL §5). It owns the next-seq allocation and every events
// insert inside a transaction. Release it with Close so a later writer
// may be registered.
type EventWriter struct {
	store   *Store
	session string
	mu      sync.Mutex
	next    int64
	paused  bool
	closed  bool
}

// Event is one attributable ordered event as appended by the writer.
// Payload is any JSON-serialisable value; it is canonicalised on write.
type Event struct {
	SessionID string
	Seq       int64
	CreatedAt time.Time
	Actor     string
	Type      string
	Payload   any
}

// PauseInfo describes the engine-side pause context recorded in the
// session.paused event payload.
type PauseInfo struct {
	Reason         string
	ConsumedBudget int64
	NextSafeOp     string
}

// RecordedEvent is an event row read back from the store. PayloadJSON
// is the stored canonical bytes; the digest is re-verified on read.
type RecordedEvent struct {
	SessionID   string
	Seq         int64
	CreatedAt   time.Time
	Actor       string
	Type        string
	PayloadSHA  string
	PayloadJSON string
}
