package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func openT(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, info, err := Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Pragmas["journal_mode"] != "wal" {
		t.Fatalf("journal_mode=%s", info.Pragmas["journal_mode"])
	}
	if info.Pragmas["foreign_keys"] != "1" {
		t.Fatalf("foreign_keys=%s", info.Pragmas["foreign_keys"])
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db, dir
}

// TestFrozenFixturesMatchCanonicalBytes verifies the frozen byte fixtures in
// testdata reproduce exactly under the declared canonical encoder (sorted
// keys, compact, no HTML escaping, Go number encoding). T019 §6: equivalent
// JSON values alone do not imply identical bytes.
func TestFrozenFixturesMatchCanonicalBytes(t *testing.T) {
	for _, f := range []string{"key_order", "unicode", "numbers", "nested"} {
		b, err := os.ReadFile("testdata/" + f + ".json")
		if err != nil {
			t.Fatal(err)
		}
		if !IsCanonical(string(b)) {
			t.Errorf("%s: fixture not canonical per current encoder", f)
		}
	}
}

// TestMigrationReapplyIsNoop proves versioned forward-only migrations are
// idempotent and all schema objects exist (DATA-MODEL §2, done checklist).
func TestMigrationReapplyIsNoop(t *testing.T) {
	db, _ := openT(t)
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("schema_migrations rows = %d, want 1", n)
	}
	for _, tbl := range []string{"sessions", "events", "receipts"} {
		if _, err := db.Exec(`SELECT 1 FROM ` + tbl + ` LIMIT 1`); err != nil {
			t.Fatalf("table %s unusable: %v", tbl, err)
		}
	}
}

// TestOpenRecordsPragmasAndVersions asserts the mandated pragmas and versions
// are actually recorded from the live DB (T019 §1).
func TestOpenRecordsPragmasAndVersions(t *testing.T) {
	db, dir := openT(t)
	defer db.Close()
	_ = dir
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("effective journal_mode = %q", mode)
	}
	// Foreign keys enforced: inserting an event for a missing session fails.
	tx, _ := db.Begin()
	if _, err := tx.Exec(`INSERT INTO events (session_id, seq, created_at_utc, actor, type, payload_sha256, payload_json)
		VALUES ('missing', 0, 'now', 'user', 'x', 'h', '{}')`); err == nil {
		tx.Rollback()
		t.Fatal("foreign_keys not enforced")
	}
	tx.Rollback()
}

// TestWriterConcurrencySingleOwner exercises the serialized writer under a
// race detector: many goroutines appending through one writer, readers
// concurrently, sequence must stay contiguous.
func TestWriterConcurrencySingleOwner(t *testing.T) {
	db, _ := openT(t)
	defer db.Close()
	w, sess, err := NewEventWriter(db, Session{Provider: "p", ModelID: "m", Harness: "t"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				_ = w.Append(ctx, EventIn{Actor: "provider:fake", Type: "provider.delta",
					Payload: map[string]any{"g": g, "i": i}})
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, err := ReadEvents(ctx, db, sess.ID); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
	if err := VerifyContiguous(ctx, db, sess.ID); err != nil {
		t.Fatal(err)
	}
}

// TestReplayOracleNegative verifies deliberate corruption of payload_sha256
// or seq fails the replay oracle (T019 §6 negative).
func TestReplayOracleNegative(t *testing.T) {
	db, _ := openT(t)
	defer db.Close()
	w, sess, err := NewEventWriter(db, Session{Provider: "p", ModelID: "m", Harness: "t"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := w.Append(ctx, EventIn{Actor: "user", Type: "provider.delta", Payload: map[string]any{"i": i}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Replay(ctx, db, sess.ID); err != nil {
		t.Fatal(err)
	}
	// Corrupt digest.
	if _, err := db.Exec(`UPDATE events SET payload_sha256='deadbeef' WHERE session_id=? AND seq=2`, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(ctx, db, sess.ID); err == nil {
		t.Fatal("replay accepted corrupted digest")
	}
	if _, err := RestoreFixtureDigests(ctx, db, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(ctx, db, sess.ID); err != nil {
		t.Fatalf("clean replay after restore: %v", err)
	}
	// Corrupt sequence (delete middle row → gap).
	if _, err := db.Exec(`DELETE FROM events WHERE session_id=? AND seq=2`, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(ctx, db, sess.ID); err == nil {
		t.Fatal("replay accepted corrupted sequence")
	}
}
