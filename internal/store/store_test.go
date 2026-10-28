package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustSession(t *testing.T, s *Store) string {
	t.Helper()
	id := fmt.Sprintf("sid-%d", time.Now().UnixNano())
	if _, err := s.CreateSession(context.Background(), id, "zai", "glm-4.6", "test-harness", "cfgsha"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return id
}

func TestOpenAppliesPragmasAndSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ws.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	for pragma, want := range map[string]string{
		"journal_mode": "wal",
		"synchronous":  "1", // NORMAL
		"foreign_keys": "1",
	} {
		var got string
		if err := s.db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil {
			t.Fatalf("pragma %s: %v", pragma, err)
		}
		if got != want {
			t.Errorf("pragma %s = %q, want %q", pragma, got, want)
		}
	}
	for _, table := range []string{"schema_migrations", "sessions", "events", "receipts"} {
		var name string
		err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			t.Errorf("table %s missing", table)
		} else if err != nil {
			t.Fatalf("check table: %v", err)
		}
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatalf("open1: %v", err)
	}
	v1, err := s1.SchemaVersion()
	s1.Close()
	if err != nil {
		t.Fatalf("version1: %v", err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen (reapply) failed: %v", err)
	}
	defer s2.Close()
	v2, err := s2.SchemaVersion()
	if err != nil || v2 != v1 || v1 == 0 {
		t.Fatalf("reapply not a no-op: v1=%d v2=%d err=%v", v1, v2, err)
	}
}

func TestFutureSchemaRejected(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO schema_migrations (version, applied_at_utc) VALUES (999, 'x')`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(filepath.Join(t.TempDir(), "other.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Simulate: point at a db whose max version is in the future.
	s2.Close()
	path := filepath.Join(t.TempDir(), "future.db")
	db, _ := sql.Open("sqlite", "file:"+path)
	db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at_utc TEXT NOT NULL)`)
	db.Exec(`INSERT INTO schema_migrations VALUES (999,'x')`)
	db.Close()
	if _, err := Open(path); !errors.Is(err, ErrFutureSchema) {
		t.Fatalf("want ErrFutureSchema, got %v", err)
	}
}

func TestAppendAndReplayContiguous(t *testing.T) {
	s := newTestStore(t)
	sid := mustSession(t, s)
	w, err := s.NewEventWriter(context.Background(), sid)
	if err != nil {
		t.Fatalf("writer: %v", err)
	}
	defer w.Close()
	for i := 0; i < 5; i++ {
		seq, err := w.Append(context.Background(), "user", "turn.start", map[string]any{"i": i, "z": true, "a": "x"})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if seq != int64(i) {
			t.Fatalf("seq = %d, want %d", seq, i)
		}
	}
	evs, err := s.Replay(context.Background(), sid)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(evs) != 5 {
		t.Fatalf("len = %d", len(evs))
	}
	for i, e := range evs {
		if e.Seq != int64(i) {
			t.Errorf("event %d seq %d", i, e.Seq)
		}
	}
}

func TestPayloadCanonicalSortedKeys(t *testing.T) {
	s := newTestStore(t)
	sid := mustSession(t, s)
	w, _ := s.NewEventWriter(context.Background(), sid)
	defer w.Close()
	if _, err := w.Append(context.Background(), "user", "x", map[string]any{"b": 1, "a": 2}); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.Replay(context.Background(), sid)
	if evs[0].PayloadJSON != `{"a":2,"b":1}` {
		t.Fatalf("payload not canonical: %s", evs[0].PayloadJSON)
	}
}

func TestSingleWriterEnforced(t *testing.T) {
	s := newTestStore(t)
	sid := mustSession(t, s)
	w, err := s.NewEventWriter(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.NewEventWriter(context.Background(), sid); !errors.Is(err, ErrWriterExists) {
		t.Fatalf("want ErrWriterExists, got %v", err)
	}
	w.Close()
	w2, err := s.NewEventWriter(context.Background(), sid)
	if err != nil {
		t.Fatalf("re-register after close: %v", err)
	}
	w2.Close()
}

func TestWriterResumesFromCommittedSeq(t *testing.T) {
	s := newTestStore(t)
	sid := mustSession(t, s)
	w, _ := s.NewEventWriter(context.Background(), sid)
	w.Append(context.Background(), "user", "a", nil)
	w.Append(context.Background(), "user", "b", nil)
	w.Close()
	w2, _ := s.NewEventWriter(context.Background(), sid)
	defer w2.Close()
	if got := w2.NextSeq(); got != 2 {
		t.Fatalf("next seq = %d, want 2", got)
	}
}

func TestAppendValidation(t *testing.T) {
	s := newTestStore(t)
	sid := mustSession(t, s)
	w, _ := s.NewEventWriter(context.Background(), sid)
	defer w.Close()
	if _, err := w.Append(context.Background(), "", "t", nil); !errors.Is(err, ErrEmptyActor) {
		t.Errorf("empty actor: %v", err)
	}
	if _, err := w.Append(context.Background(), "user", "", nil); !errors.Is(err, ErrEmptyType) {
		t.Errorf("empty type: %v", err)
	}
}

func TestUnknownSessionRejected(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.NewEventWriter(context.Background(), "nope"); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("want ErrUnknownSession, got %v", err)
	}
}

func TestClosedDBAndWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	s, _ := Open(path)
	sid := mustSession(t, s)
	w, _ := s.NewEventWriter(context.Background(), sid)
	defer w.Close()
	s.Close()
	if _, err := w.Append(context.Background(), "user", "t", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("append on closed store: %v", err)
	}
	if _, err := s.Replay(context.Background(), sid); !errors.Is(err, ErrClosed) {
		t.Fatalf("replay on closed store: %v", err)
	}
	if _, err := Open(filepath.Join(path, "not-a-dir", "x.db")); err == nil {
		t.Fatal("open in bad dir should fail")
	}
}

func TestCorruptHashFailsReplay(t *testing.T) {
	s := newTestStore(t)
	sid := mustSession(t, s)
	w, _ := s.NewEventWriter(context.Background(), sid)
	w.Append(context.Background(), "user", "t", map[string]int{"n": 1})
	w.Close()
	if _, err := s.db.Exec(`UPDATE events SET payload_json='{"tampered":1}' WHERE session_id=? AND seq=0`, sid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Replay(context.Background(), sid); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("want ErrHashMismatch, got %v", err)
	}
}

func TestSeqGapFailsReplay(t *testing.T) {
	s := newTestStore(t)
	sid := mustSession(t, s)
	w, _ := s.NewEventWriter(context.Background(), sid)
	w.Append(context.Background(), "user", "a", nil)
	w.Append(context.Background(), "user", "b", nil)
	w.Close()
	if _, err := s.db.Exec(`DELETE FROM events WHERE session_id=? AND seq=0`, sid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Replay(context.Background(), sid); !errors.Is(err, ErrSeqGap) {
		t.Fatalf("want ErrSeqGap, got %v", err)
	}
}

func TestSeqCollisionRejected(t *testing.T) {
	s := newTestStore(t)
	sid := mustSession(t, s)
	w, _ := s.NewEventWriter(context.Background(), sid)
	defer w.Close()
	w.Append(context.Background(), "user", "a", nil)
	// Simulate a racing writer by forging the same seq out-of-band.
	if _, err := s.db.Exec(`INSERT INTO events (session_id,seq,created_at_utc,actor,type,payload_sha256,payload_json)
		VALUES (?,1,'t','user','forged',?,?)`, sid, sha256HexStr(`{}`), `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(context.Background(), "user", "b", nil); !errors.Is(err, ErrSeqCollision) {
		t.Fatalf("want ErrSeqCollision, got %v", err)
	}
}

func sha256HexStr(s string) string {
	sum := sha256Sum([]byte(s))
	return hexEncode(sum)
}

func TestConcurrentAppendsStayContiguous(t *testing.T) {
	s := newTestStore(t)
	sid := mustSession(t, s)
	w, _ := s.NewEventWriter(context.Background(), sid)
	defer w.Close()
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := w.Append(context.Background(), "engine", "provider.delta", map[string]int{"i": i}); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	evs, err := s.Replay(context.Background(), sid)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(evs) != n {
		t.Fatalf("len = %d, want %d", len(evs), n)
	}
}

func TestJSONLExport(t *testing.T) {
	s := newTestStore(t)
	sid := mustSession(t, s)
	w, _ := s.NewEventWriter(context.Background(), sid)
	w.Append(context.Background(), "user", "turn.start", map[string]any{"b": 2, "a": 1})
	w.Close()
	b, err := s.ExportJSONL(context.Background(), sid)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	want := fmt.Sprintf(`{"actor":"user","created_at_utc":%q,"payload":{"a":1,"b":2},"payload_sha256":"`+
		sha256HexStr(`{"a":1,"b":2}`)+`","seq":0,"session_id":%q,"type":"turn.start"}`+"\n",
		evCreatedAt(t, s, sid), sid)
	if string(b) != want {
		t.Fatalf("jsonl mismatch:\n got %s\nwant %s", b, want)
	}
}

func evCreatedAt(t *testing.T, s *Store, sid string) string {
	t.Helper()
	var ts string
	if err := s.db.QueryRow(`SELECT created_at_utc FROM events WHERE session_id=? AND seq=0`, sid).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func TestPauseResumeEvents(t *testing.T) {
	s := newTestStore(t)
	sid := mustSession(t, s)
	w, _ := s.NewEventWriter(context.Background(), sid)
	defer w.Close()
	if err := w.PauseBudget(context.Background(), map[string]any{"reason": "budget", "consumed": 10}); err != nil {
		t.Fatalf("pause: %v", err)
	}
	sess, _ := s.GetSession(context.Background(), sid)
	if sess.Status != "paused" {
		t.Fatalf("status = %s", sess.Status)
	}
	if err := w.Resume(context.Background(), map[string]any{"run": "fresh"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	sess, _ = s.GetSession(context.Background(), sid)
	if sess.Status != "active" {
		t.Fatalf("status after resume = %s", sess.Status)
	}
	evs, _ := s.Replay(context.Background(), sid)
	if evs[0].Type != "session.paused" || evs[1].Type != "session.resumed" {
		t.Fatalf("unexpected events %+v", evs)
	}
}

func TestFileDBPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	s, _ := Open(path)
	sid := mustSession(t, s)
	w, _ := s.NewEventWriter(context.Background(), sid)
	w.Append(context.Background(), "user", "a", nil)
	w.Close()
	s.Close()
	s2, _ := Open(path)
	defer s2.Close()
	evs, err := s2.Replay(context.Background(), sid)
	if err != nil || len(evs) != 1 {
		t.Fatalf("persisted replay: %v %d", err, len(evs))
	}
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Logf("no wal file after close (checkpointed): %v", err)
	}
}

func TestListSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	first, err := s.CreateSession(ctx, "sid-1", "p1", "m1", "h", "sha")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateSession(ctx, "sid-2", "p2", "m2", "h", "sha")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSessionStatus(ctx, "sid-2", "completed"); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListSessions(ctx, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("list all len = %d, want 2", len(all))
	}
	if all[0].ID != first.ID || all[1].ID != second.ID {
		t.Errorf("order = %s,%s want %s,%s", all[0].ID, all[1].ID, first.ID, second.ID)
	}
	if all[1].Status != "completed" || all[1].UpdatedAt.Before(all[1].CreatedAt) {
		t.Errorf("updated session = %+v", all[1])
	}

	active, err := s.ListSessions(ctx, "active")
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 1 || active[0].ID != "sid-1" {
		t.Fatalf("list active = %+v", active)
	}

	done, err := s.ListSessions(ctx, "completed")
	if err != nil {
		t.Fatalf("list completed: %v", err)
	}
	if len(done) != 1 || done[0].ID != "sid-2" {
		t.Fatalf("list completed = %+v", done)
	}
}

func TestUpdateSessionStatusUnknownID(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpdateSessionStatus(context.Background(), "nope", "cancelled"); err == nil {
		t.Fatal("want error for unknown session id")
	}
}

func TestListSessionsClosed(t *testing.T) {
	s := newTestStore(t)
	s.Close()
	if _, err := s.ListSessions(context.Background(), ""); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}
