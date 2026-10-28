// Package storage implements FlowState's qualified SQLite storage layer:
// driver opening with mandated pragmas, versioned forward-only migrations,
// a single serialized event writer per session, canonical JSON payloads and
// a replay oracle.
//
// T019 qualification choice: pure-Go driver modernc.org/sqlite (no CGO).
package storage

import (
	"database/sql"
	"fmt"
	"runtime"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (CGO-free)
)

// OpenInfo records the effective runtime configuration observed after opening
// a database. T019 requires these to be recorded, not assumed.
type OpenInfo struct {
	DriverName    string            `json:"driver_name"`
	DriverVersion string            `json:"driver_version"`
	GoVersion     string            `json:"go_version"`
	SQLiteVersion string            `json:"sqlite_version"`
	PragmaTimeUTC string            `json:"pragma_time_utc"`
	Pragmas       map[string]string `json:"pragmas"`
}

// Open opens (creating if necessary) the database at path with the mandated
// pragmas: journal_mode=wal, foreign_keys=on, synchronous=normal. Because
// modernc.org/sqlite applies per-connection settings, a file DSN is used that
// embeds the pragmas so every pooled connection gets them; the returned info
// is read back from the live database.
func Open(path string) (*sql.DB, OpenInfo, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(1)&_pragma=busy_timeout(10000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, OpenInfo{}, fmt.Errorf("open sqlite: %w", err)
	}
	// Single write connection is fine (the serialized writer owns writes anyway);
	// allow concurrent readers.
	db.SetMaxOpenConns(8)

	info := OpenInfo{
		DriverName:    "modernc.org/sqlite",
		DriverVersion: moderncVersion,
		GoVersion:     runtime.Version(),
		PragmaTimeUTC: time.Now().UTC().Format(time.RFC3339),
		Pragmas:       map[string]string{},
	}
	for _, p := range []string{"journal_mode", "foreign_keys", "synchronous", "busy_timeout"} {
		var v string
		if err := db.QueryRow("PRAGMA " + p).Scan(&v); err != nil {
			_ = db.Close()
			return nil, OpenInfo{}, fmt.Errorf("read pragma %s: %w", p, err)
		}
		info.Pragmas[p] = v
	}
	var sv string
	if err := db.QueryRow("SELECT sqlite_version()").Scan(&sv); err != nil {
		_ = db.Close()
		return nil, OpenInfo{}, fmt.Errorf("read sqlite_version: %w", err)
	}
	info.SQLiteVersion = sv
	return db, info, nil
}

// migrations is the forward-only, versioned migration set. Version N must
// only ever be appended; existing versions are immutable.
var migrations = []struct {
	Version int
	SQL     string
}{
	{
		Version: 1,
		// Schema from specs/DATA-MODEL.md §2.
		SQL: `
CREATE TABLE sessions (
  id            TEXT PRIMARY KEY,
  created_at_utc TEXT NOT NULL,
  updated_at_utc TEXT NOT NULL,
  status        TEXT NOT NULL CHECK (status IN ('active','paused','completed','cancelled','failed')),
  provider      TEXT NOT NULL,
  model_id      TEXT NOT NULL,
  harness       TEXT NOT NULL,
  config_sha256 TEXT NOT NULL
);

CREATE TABLE events (
  session_id    TEXT NOT NULL REFERENCES sessions(id),
  seq           INTEGER NOT NULL,
  created_at_utc TEXT NOT NULL,
  actor         TEXT NOT NULL,
  type          TEXT NOT NULL,
  payload_sha256 TEXT NOT NULL,
  payload_json  TEXT NOT NULL,
  PRIMARY KEY (session_id, seq)
) WITHOUT ROWID;

CREATE TABLE receipts (
  id            TEXT PRIMARY KEY,
  capability_id TEXT NOT NULL,
  commit_sha    TEXT NOT NULL,
  receipt_sha256 TEXT NOT NULL,
  gate_outcome  TEXT NOT NULL CHECK (gate_outcome IN ('pass','fail')),
  recorded_at_utc TEXT NOT NULL
);

CREATE INDEX idx_events_session_type ON events(session_id, type);
CREATE INDEX idx_sessions_status ON sessions(status);
`,
	},
}

// Migrate applies all pending forward migrations inside one transaction and
// records applied versions in schema_migrations. Reapplying is a no-op.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at_utc TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("bootstrap schema_migrations: %w", err)
	}
	applied := map[int]bool{}
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			_ = rows.Close()
			return err
		}
		applied[v] = true
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(m.SQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", m.Version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at_utc) VALUES (?, ?)`,
			m.Version, time.Now().UTC().Format(time.RFC3339)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.Version, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
