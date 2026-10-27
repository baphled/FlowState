// Package store implements the FlowState M1 SQLite-backed event store.
//
// It owns workspace database lifecycle (open, pragmas, forward-only
// versioned migrations), the single serialized event writer per session
// (0-based contiguous seq, canonical sorted-key JSON payloads with
// payload_sha256 enforcement), verified replay, and the derived JSONL
// export. SQLite is the source of truth per specs/DATA-MODEL.md §5.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // CGO-free SQLite driver (T019-qualified)
)

// Store wraps a workspace SQLite database.
type Store struct {
	db *sql.DB
}

func openDSN(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Force a single connection so all access shares one serialization
	// domain; the event writer is serialized by design.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Open opens (creating if needed) the workspace database at path and
// applies pragmas: WAL journal mode, synchronous=NORMAL and
// foreign_keys=ON, per DATA-MODEL §1. Callers must Close when done.
func Open(path string) (*Store, error) {
	return openDSN("file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
}

// OpenMemory opens an in-memory database using the same pragmas and
// migrations; useful for tests.
func OpenMemory() (*Store, error) {
	return openDSN("file::memory:?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)")
}

// DB exposes the underlying *sql.DB for read-only projections.
// Callers must not insert into events directly.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the underlying database handle. Operations after Close
// fail with ErrClosed.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return ErrClosed
	}
	err := s.db.Close()
	s.db = nil
	return err
}
