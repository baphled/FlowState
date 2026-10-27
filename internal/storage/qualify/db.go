package qualify

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // CGO-free SQLite driver, T019 candidate
)

// DriverInfo records driver and library identity observed at runtime.
type DriverInfo struct {
	DriverName     string // database/sql driver name
	DriverVersion  string // modernc.org/sqlite module version (pinned constant)
	LibraryVersion string // SQLite library version reported by sqlite_version()
	SourceID       string // sqlite_source_id()
}

// sqliteVersion is the modernc.org/sqlite module version of the candidate
// driver; reported as the compiled candidate, cross-checked against the
// module graph at qualification time.
const sqliteVersion = "modernc.org/sqlite v1.61.0"

// Open opens (creating if needed) an isolated SQLite database at path with the
// T019-required pragmas applied per connection via the DSN: WAL journal mode,
// synchronous=NORMAL, foreign_keys=ON. It returns the effective pragma values
// and library versions read back from the runtime, never assumed.
func Open(path string) (*sql.DB, map[string]string, DriverInfo, error) {
	// journal_mode is persistent; synchronous and foreign_keys are
	// per-connection, so they must ride in the DSN.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, DriverInfo{}, fmt.Errorf("open %s: %w", path, err)
	}
	// One connection: the single serialized event writer plus modernc's
	// internal serialization is the simplest correct configuration.
	db.SetMaxOpenConns(1)

	pragmas, err := ReadPragmas(db)
	if err != nil {
		db.Close()
		return nil, nil, DriverInfo{}, err
	}
	info := DriverInfo{
		DriverName:     "sqlite (modernc.org/sqlite)",
		DriverVersion:  sqliteVersion,
		LibraryVersion: pragmas["sqlite_version"],
		SourceID:       pragmas["sqlite_source_id"],
	}
	return db, pragmas, info, nil
}

// pragmaFn returns the SQL expression yielding pragma p's value; version
// functions are scalars while the others are PRAGMA statements.
func pragmaFn(p string) string {
	switch p {
	case "sqlite_version", "sqlite_source_id":
		return "SELECT " + p + "()"
	default:
		return "PRAGMA " + p
	}
}

// ReadPragmas reads the effective runtime values of the qualification pragmas
// plus the SQLite library version strings.
func ReadPragmas(db *sql.DB) (map[string]string, error) {
	names := []string{"journal_mode", "synchronous", "foreign_keys", "sqlite_version", "sqlite_source_id"}
	out := map[string]string{}
	for _, p := range names {
		var v any
		if err := db.QueryRow(pragmaFn(p)).Scan(&v); err != nil {
			return nil, fmt.Errorf("pragma %s: %w", p, err)
		}
		switch t := v.(type) {
		case []byte:
			out[p] = string(t)
		case string:
			out[p] = t
		default:
			out[p] = fmt.Sprintf("%v", t)
		}
	}
	return out, nil
}

const schemaMigrationsOnly = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version     INTEGER PRIMARY KEY,
  applied_at_utc TEXT NOT NULL
);
`

// migrations are the forward-only versioned migrations implementing
// DATA-MODEL.md §2 verbatim; version 1 is the full M1 schema.
var migrations = []struct {
	Version int
	SQL     string
}{{
	Version: 1,
	SQL:     schemaV1,
}}

const schemaV1 = schemaMigrationsOnly + `
CREATE TABLE IF NOT EXISTS sessions (
  id            TEXT PRIMARY KEY,
  created_at_utc TEXT NOT NULL,
  updated_at_utc TEXT NOT NULL,
  status        TEXT NOT NULL CHECK (status IN ('active','paused','completed','cancelled','failed')),
  provider      TEXT NOT NULL,
  model_id      TEXT NOT NULL,
  harness       TEXT NOT NULL,
  config_sha256 TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
  session_id    TEXT NOT NULL REFERENCES sessions(id),
  seq           INTEGER NOT NULL,
  created_at_utc TEXT NOT NULL,
  actor         TEXT NOT NULL,
  type          TEXT NOT NULL,
  payload_sha256 TEXT NOT NULL,
  payload_json  TEXT NOT NULL,
  PRIMARY KEY (session_id, seq)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS receipts (
  id            TEXT PRIMARY KEY,
  capability_id TEXT NOT NULL,
  commit_sha    TEXT NOT NULL,
  receipt_ref   TEXT NOT NULL,
  receipt_sha256 TEXT NOT NULL,
  gate_outcome  TEXT NOT NULL CHECK (gate_outcome IN ('pass','fail')),
  recorded_at_utc TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_session_type ON events(session_id, type);
CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
`

// Migrate applies pending forward-only migrations, each inside a transaction
// that also records the version in schema_migrations with its UTC timestamp.
// Reapplying is a no-op: already-recorded versions are skipped.
func Migrate(ctx context.Context, db *sql.DB) (applied []int, err error) {
	if _, err := db.ExecContext(ctx, schemaMigrationsOnly); err != nil {
		return nil, fmt.Errorf("ensure schema_migrations: %w", err)
	}
	for _, m := range migrations {
		var exists int
		if err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM schema_migrations WHERE version = ?", m.Version).Scan(&exists); err != nil {
			return nil, err
		}
		if exists > 0 {
			continue // no-op on reapply
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			return nil, fmt.Errorf("migration %d: %w", m.Version, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, applied_at_utc) VALUES (?, ?)",
			m.Version, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		applied = append(applied, m.Version)
	}
	return applied, nil
}

// CreateSession inserts a session row with the given attribution fields.
func CreateSession(ctx context.Context, db *sql.DB, id string, attribution map[string]string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.ExecContext(ctx, `INSERT INTO sessions
		(id, created_at_utc, updated_at_utc, status, provider, model_id, harness, config_sha256)
		VALUES (?,?,?,?,?,?,?,?)`,
		id, now, now, "active",
		attribution["provider"], attribution["model_id"], attribution["harness"], attribution["config_sha256"])
	return err
}

// SessionStatus returns the current status of session id.
func SessionStatus(ctx context.Context, db *sql.DB, id string) (string, error) {
	var s string
	err := db.QueryRowContext(ctx, "SELECT status FROM sessions WHERE id = ?", id).Scan(&s)
	return s, err
}

// SetSessionStatusTx updates the session status within tx.
func SetSessionStatusTx(ctx context.Context, tx *sql.Tx, id, status string) error {
	_, err := tx.ExecContext(ctx,
		"UPDATE sessions SET status = ?, updated_at_utc = ? WHERE id = ?",
		status, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// ErrWriterClosed is returned by Append after the session reached a terminal
// commit or an explicit Close: late effects are rejected by design.
var ErrWriterClosed = fmt.Errorf("qualify: session writer closed, late effect rejected")

// IsLateEffect reports whether err wraps or equals ErrWriterClosed.
func IsLateEffect(err error) bool {
	for err != nil {
		if err == ErrWriterClosed || strings.Contains(err.Error(), ErrWriterClosed.Error()) {
			return true
		}
		if u, ok := err.(interface{ Unwrap() error }); ok {
			err = u.Unwrap()
			continue
		}
		return false
	}
	return false
}
