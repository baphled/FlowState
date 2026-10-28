package storage

import (
	"database/sql"
	"encoding/json"
	"os"
	"runtime"
	"testing"
)

// TestT019QualificationCapture emits the qualification evidence bundle
// (effective pragmas, versions, applied migrations, no-op reapply) as JSON
// to the path in FLOWSTATE_T019_CAPTURE for Dropbox retention.
// Skipped unless the env var is set.
func TestT019QualificationCapture(t *testing.T) {
	out := os.Getenv("FLOWSTATE_T019_CAPTURE")
	if out == "" {
		t.Skip("FLOWSTATE_T019_CAPTURE not set")
	}
	dir := t.TempDir()
	db, info, err := Open(dir + "/t019.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	rows1 := appliedRows(t, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migration reapply: %v", err)
	}
	rows2 := appliedRows(t, db)
	if err != nil {
		t.Fatal(err)
	}
	pragma := func(k string) string {
		var s string
		if err := db.QueryRow("PRAGMA " + k).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	b, err := json.MarshalIndent(map[string]any{
		"open_info":                      info,
		"effective_pragmas":              map[string]string{"journal_mode": pragma("journal_mode"), "foreign_keys": pragma("foreign_keys"), "synchronous": pragma("synchronous"), "busy_timeout": pragma("busy_timeout")},
		"applied_migrations_first_apply": rows1,
		"applied_migrations_reapply":     rows2,
		"reapply_is_noop":                len(rows1) == len(rows2),
		"go_version":                     runtime.Version(),
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func appliedRows(t *testing.T, db *sql.DB) []int {
	t.Helper()
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}
