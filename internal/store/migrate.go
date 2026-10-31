package store

import (
	"database/sql"
	"fmt"
)

// migration is one forward-only schema step. Migrations must never be
// edited once shipped; append new ones with an incremented Version.
type migration struct {
	version int64
	stmts   []string
}

var migrations = []migration{
	{
		// Version 1 is the schema_migrations bootstrap itself; the table
		// is created idempotently above, so this entry only records it.
		version: 1,
	},
	{
		version: 2,
		stmts: []string{
			`CREATE TABLE sessions (
				id            TEXT PRIMARY KEY,
				created_at_utc TEXT NOT NULL,
				updated_at_utc TEXT NOT NULL,
				status        TEXT NOT NULL CHECK (status IN ('active','paused','completed','cancelled','failed')),
				provider      TEXT NOT NULL,
				model_id      TEXT NOT NULL,
				harness       TEXT NOT NULL,
				config_sha256 TEXT NOT NULL
			)`,
			`CREATE TABLE events (
				session_id    TEXT NOT NULL REFERENCES sessions(id),
				seq           INTEGER NOT NULL,
				created_at_utc TEXT NOT NULL,
				actor         TEXT NOT NULL,
				type          TEXT NOT NULL,
				payload_sha256 TEXT NOT NULL,
				payload_json  TEXT NOT NULL,
				PRIMARY KEY (session_id, seq)
			) WITHOUT ROWID`,
			`CREATE TABLE receipts (
				id            TEXT PRIMARY KEY,
				capability_id TEXT NOT NULL,
				commit_sha    TEXT NOT NULL,
				receipt_ref   TEXT NOT NULL,
				receipt_sha256 TEXT NOT NULL,
				gate_outcome  TEXT NOT NULL CHECK (gate_outcome IN ('pass','fail')),
				recorded_at_utc TEXT NOT NULL
			)`,
			`CREATE INDEX idx_events_session_type ON events(session_id, type)`,
			`CREATE INDEX idx_sessions_status ON sessions(status)`,
		},
	},
}

// migrate applies forward-only versioned migrations to the database.
// Applying to an already-migrated database is a no-op. A database whose
// schema_migrations contains a version greater than the highest known
// version fails with ErrFutureSchema (downgrades are forbidden).
func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version     INTEGER PRIMARY KEY,
		applied_at_utc TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("bootstrap schema_migrations: %w", err)
	}
	var maxApplied sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&maxApplied); err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for _, m := range migrations {
		if maxApplied.Valid && m.version <= maxApplied.Int64 {
			continue
		}
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("migration %d begin: %w", m.version, err)
		}
		for _, stmt := range m.stmts {
			if _, err := tx.Exec(stmt); err != nil {
				_ = tx.Rollback() // best effort; original error takes precedence
				return fmt.Errorf("migration %d: %w", m.version, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at_utc) VALUES (?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, m.version); err != nil {
			_ = tx.Rollback() // best effort; original error takes precedence
			return fmt.Errorf("migration %d record: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d commit: %w", m.version, err)
		}
	}
	var top int64
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&top); err != nil {
		return err
	}
	if len(migrations) > 0 && top > migrations[len(migrations)-1].version {
		return fmt.Errorf("%w: db at %d, code knows %d", ErrFutureSchema, top, migrations[len(migrations)-1].version)
	}
	return nil
}

// SchemaVersion returns the highest applied migration version.
func (s *Store) SchemaVersion() (int64, error) {
	if err := s.checkOpen(); err != nil {
		return 0, err
	}
	var v sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, err
	}
	return v.Int64, nil
}
