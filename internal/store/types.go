package store

import (
	"database/sql"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Store wraps a workspace SQLite database: db is the single
// serialized connection pool (the event writer is serialized by
// design) and gdb is the GORM session bound onto that same pool; the
// raw handle stays reserved for grandfathered files.
type Store struct {
	db  *sql.DB
	gdb *gorm.DB

	// path is the database file, empty for an in-memory database; the
	// v9 rebuild needs it to open its dedicated connection and backup.
	path string

	// locks is the process-local lock table AcquireRunLease uses when the
	// store has no path; each store owns one, so stores never share state.
	locks *LockTable
}

// Session is the persisted session record per DATA-MODEL §2.
// ParentID links a delegated child session to its parent; roots keep
// the empty string.
type Session struct {
	ID        string
	CreatedAt time.Time
	UpdatedAt time.Time
	Status    string
	Provider  string
	ModelID   string
	Harness   string
	ConfigSHA string
	ParentID  string
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

// BudgetSnapshot reports the durable budget state of one session:
// the declared pool anchor (nil = none declared), the outstanding
// reserved budget held by its children's live reservations, and the
// available budget (Pool - Reserved, nil when no pool is declared).
type BudgetSnapshot struct {
	Pool      *int64
	Reserved  int64
	Available *int64
}

// BudgetReservation is one durable child grant: Granted is the
// reserved amount, Consumed the settled usage (capped at Granted),
// and Status the domain-layer lifecycle (reserved or charged).
type BudgetReservation struct {
	ID        string
	ParentID  string
	ChildID   string
	Granted   int64
	Consumed  int64
	Status    string
	CreatedAt time.Time
}
