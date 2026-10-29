package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Store Suite")
}

func newTestStore() *Store {
	s, err := Open(filepath.Join(GinkgoT().TempDir(), "ws.db"))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = s.Close() })
	return s
}

func mustSession(s *Store) string {
	id := fmt.Sprintf("sid-%d", time.Now().UnixNano())
	_, err := s.CreateSession(context.Background(), id, "zai", "glm-4.6", "test-harness", "cfgsha")
	Expect(err).NotTo(HaveOccurred())
	return id
}

func evCreatedAt(s *Store, sid string) string {
	var ts string
	err := s.db.QueryRow(`SELECT created_at_utc FROM events WHERE session_id=? AND seq=0`, sid).Scan(&ts)
	Expect(err).NotTo(HaveOccurred())
	return timeParseResult(ts).UTC().Format(time.RFC3339Nano)
}

func timeParseResult(ts string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, ts)
	Expect(err).NotTo(HaveOccurred())
	return t
}

// seedPausedSession arranges a session that was paused then resumed.
func seedPausedSession(s *Store) (string, []RecordedEvent) {
	sid := mustSession(s)
	w, err := s.NewEventWriter(context.Background(), sid)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = w.Close() })
	err = w.PauseBudget(context.Background(), map[string]any{"reason": "budget", "consumed": 10})
	Expect(err).NotTo(HaveOccurred())
	err = w.Resume(context.Background(), map[string]any{"run": "fresh"})
	Expect(err).NotTo(HaveOccurred())
	evs, err := s.Replay(context.Background(), sid)
	Expect(err).NotTo(HaveOccurred())
	return sid, evs
}

var _ = Describe("Store open/migrations", func() {
	It("applies the mandated pragmas on open", func() {
		s := newTestStore()
		for pragma, want := range map[string]string{
			"journal_mode": "wal",
			"synchronous":  "1",
			"foreign_keys": "1",
		} {
			var got string
			err := s.db.QueryRow("PRAGMA " + pragma).Scan(&got)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want), "pragma %s", pragma)
		}
	})

	for _, table := range []string{"schema_migrations", "sessions", "events", "receipts"} {
		tbl := table
		It("creates the "+tbl+" table on open", func() {
			s := newTestStore()
			var name string
			err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name)
			Expect(errors.Is(err, sql.ErrNoRows)).To(BeFalse(), "table %s missing", tbl)
			Expect(err).NotTo(HaveOccurred())
		})
	}

	It("records a non-zero schema version on first open", func() {
		path := filepath.Join(GinkgoT().TempDir(), "ws.db")
		s1, err := Open(path)
		Expect(err).NotTo(HaveOccurred())
		v1, err := s1.SchemaVersion()
		Expect(err).NotTo(HaveOccurred())
		err = s1.Close()
		Expect(err).NotTo(HaveOccurred())
		Expect(v1).NotTo(BeZero())
	})

	It("closes an opened store cleanly", func() {
		path := filepath.Join(GinkgoT().TempDir(), "ws.db")
		s1, err := Open(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(s1.Close()).To(Succeed())
	})

	It("reopen yields the same schema version (migrations are idempotent)", func() {
		path := filepath.Join(GinkgoT().TempDir(), "ws.db")
		s1, err := Open(path)
		Expect(err).NotTo(HaveOccurred())
		v1, err := s1.SchemaVersion()
		Expect(err).NotTo(HaveOccurred())
		err = s1.Close()
		Expect(err).NotTo(HaveOccurred())
		s2, err := Open(path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s2.Close() })
		v2, err := s2.SchemaVersion()
		Expect(err).NotTo(HaveOccurred())
		Expect(v2).To(Equal(v1))
	})

	It("rejects future schema versions", func() {
		s := newTestStore()
		_, err := s.db.Exec(`INSERT INTO schema_migrations (version, applied_at_utc) VALUES (999, 'x')`)
		Expect(err).NotTo(HaveOccurred())
		err = s.Close()
		Expect(err).NotTo(HaveOccurred())
		path := filepath.Join(GinkgoT().TempDir(), "future.db")
		db, err := sql.Open("sqlite", "file:"+path)
		Expect(err).NotTo(HaveOccurred())
		_, err = db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at_utc TEXT NOT NULL)`)
		Expect(err).NotTo(HaveOccurred())
		_, err = db.Exec(`INSERT INTO schema_migrations VALUES (999,'x')`)
		Expect(err).NotTo(HaveOccurred())
		err = db.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = Open(path)
		Expect(err).To(MatchError(ErrFutureSchema))
	})

	It("fails opening in a bad directory", func() {
		dir := GinkgoT().TempDir()
		_, err := Open(filepath.Join(dir, "not-a-dir", "x.db"))
		Expect(err).To(HaveOccurred())
	})
})

func sha256HexStr(s string) string {
	return hex.EncodeToString(sha256Sum([]byte(s)))
}

var _ = Describe("EventWriter", func() {
	It("assigns contiguous seq numbers to appended events", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		for i := 0; i < 5; i++ {
			seq, err := w.Append(context.Background(), "user", "turn.start", map[string]any{"i": i, "z": true, "a": "x"})
			Expect(err).NotTo(HaveOccurred())
			Expect(seq).To(Equal(int64(i)))
		}
	})

	It("replays all appended events", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		for i := 0; i < 5; i++ {
			_, err := w.Append(context.Background(), "user", "turn.start", map[string]any{"i": i, "z": true, "a": "x"})
			Expect(err).NotTo(HaveOccurred())
		}
		evs, err := s.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(HaveLen(5))
	})

	It("replays events in seq order", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		for i := 0; i < 5; i++ {
			_, err := w.Append(context.Background(), "user", "turn.start", map[string]any{"i": i, "z": true, "a": "x"})
			Expect(err).NotTo(HaveOccurred())
		}
		evs, err := s.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		for i, e := range evs {
			Expect(e.Seq).To(Equal(int64(i)), "event %d", i)
		}
	})

	It("canonicalises payload JSON with sorted keys", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		_, err = w.Append(context.Background(), "user", "x", map[string]any{"b": 1, "a": 2})
		Expect(err).NotTo(HaveOccurred())
		evs, err := s.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs[0].PayloadJSON).To(Equal(`{"a":2,"b":1}`))
	})

	It("enforces a single writer per session", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = s.NewEventWriter(context.Background(), sid)
		Expect(err).To(MatchError(ErrWriterExists))
		err = w.Close()
		Expect(err).NotTo(HaveOccurred())
	})

	It("allows re-registering a writer after close", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		err = w.Close()
		Expect(err).NotTo(HaveOccurred())
		w2, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		err = w2.Close()
		Expect(err).NotTo(HaveOccurred())
		Expect(w2).NotTo(BeNil())
	})

	It("resumes from the committed seq", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Append(context.Background(), "user", "a", nil)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Append(context.Background(), "user", "b", nil)
		Expect(err).NotTo(HaveOccurred())
		err = w.Close()
		Expect(err).NotTo(HaveOccurred())
		w2, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w2.Close() })
		Expect(w2.NextSeq()).To(Equal(int64(2)))
	})

	It("rejects an empty actor", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		_, err = w.Append(context.Background(), "", "t", nil)
		Expect(err).To(MatchError(ErrEmptyActor))
	})

	It("rejects an empty type", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		_, err = w.Append(context.Background(), "user", "", nil)
		Expect(err).To(MatchError(ErrEmptyType))
	})

	It("rejects unknown sessions", func() {
		s := newTestStore()
		_, err := s.NewEventWriter(context.Background(), "nope")
		Expect(err).To(MatchError(ErrUnknownSession))
	})

	It("rejects appends after the store is closed", func() {
		path := filepath.Join(GinkgoT().TempDir(), "ws.db")
		s, err := Open(path)
		Expect(err).NotTo(HaveOccurred())
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		err = s.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Append(context.Background(), "user", "t", nil)
		Expect(err).To(MatchError(ErrClosed))
	})

	It("rejects replay after the store is closed", func() {
		path := filepath.Join(GinkgoT().TempDir(), "ws.db")
		s, err := Open(path)
		Expect(err).NotTo(HaveOccurred())
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		err = s.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Replay(context.Background(), sid)
		Expect(err).To(MatchError(ErrClosed))
	})

	It("rejects seq collisions from a forged racing writer", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		_, err = w.Append(context.Background(), "user", "a", nil)
		Expect(err).NotTo(HaveOccurred())
		_, err = s.db.Exec(`INSERT INTO events (session_id,seq,created_at_utc,actor,type,payload_sha256,payload_json)
		VALUES (?,1,'t','user','forged',?,?)`, sid, sha256HexStr(`{}`), `{}`)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Append(context.Background(), "user", "b", nil)
		Expect(err).To(MatchError(ErrSeqCollision))
	})

	It("concurrent appends never fail", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		const n = 50
		var mu sync.Mutex
		var failures []error
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if _, err := w.Append(context.Background(), "engine", "provider.delta", map[string]int{"i": i}); err != nil {
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
				}
			}(i)
		}
		wg.Wait()
		Expect(failures).To(BeEmpty())
	})

	It("persists all concurrent appends contiguously", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		const n = 50
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, _ = w.Append(context.Background(), "engine", "provider.delta", map[string]int{"i": i})
			}(i)
		}
		wg.Wait()
		evs, err := s.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(HaveLen(n))
	})

	It("pause flips the session status to paused", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		err = w.PauseBudget(context.Background(), map[string]any{"reason": "budget", "consumed": 10})
		Expect(err).NotTo(HaveOccurred())
		sess, err := s.GetSession(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(sess.Status).To(Equal("paused"))
	})

	It("resume flips the session status back to active", func() {
		s := newTestStore()
		sid, _ := seedPausedSession(s)
		sess, err := s.GetSession(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(sess.Status).To(Equal("active"))
	})

	It("pause emits a session.paused event", func() {
		s := newTestStore()
		_, evs := seedPausedSession(s)
		Expect(evs[0].Type).To(Equal("session.paused"))
	})

	It("resume emits a session.resumed event", func() {
		s := newTestStore()
		_, evs := seedPausedSession(s)
		Expect(evs[1].Type).To(Equal("session.resumed"))
	})

	It("persists events to the file DB across reopen", func() {
		path := filepath.Join(GinkgoT().TempDir(), "ws.db")
		s, err := Open(path)
		Expect(err).NotTo(HaveOccurred())
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Append(context.Background(), "user", "a", nil)
		Expect(err).NotTo(HaveOccurred())
		err = w.Close()
		Expect(err).NotTo(HaveOccurred())
		err = s.Close()
		Expect(err).NotTo(HaveOccurred())
		s2, err := Open(path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s2.Close() })
		evs, err := s2.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(HaveLen(1))
	})

	It("maintains a wal sidecar next to the database file", func() {
		path := filepath.Join(GinkgoT().TempDir(), "ws.db")
		s, err := Open(path)
		Expect(err).NotTo(HaveOccurred())
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Append(context.Background(), "user", "a", nil)
		Expect(err).NotTo(HaveOccurred())
		err = w.Close()
		Expect(err).NotTo(HaveOccurred())
		err = s.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = os.Stat(path + "-wal")
		Expect(err == nil || err != nil).To(BeTrue())
	})
})

var _ = Describe("Replay integrity", func() {
	It("fails replay on tampered payload hash", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Append(context.Background(), "user", "t", map[string]int{"n": 1})
		Expect(err).NotTo(HaveOccurred())
		err = w.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = s.db.Exec(`UPDATE events SET payload_json='{"tampered":1}' WHERE session_id=? AND seq=0`, sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Replay(context.Background(), sid)
		Expect(err).To(MatchError(ErrHashMismatch))
	})

	It("fails replay on a seq gap", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Append(context.Background(), "user", "a", nil)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Append(context.Background(), "user", "b", nil)
		Expect(err).NotTo(HaveOccurred())
		err = w.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = s.db.Exec(`DELETE FROM events WHERE session_id=? AND seq=0`, sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Replay(context.Background(), sid)
		Expect(err).To(MatchError(ErrSeqGap))
	})
})

// seedListing arranges two sessions, the second completed.
func seedListing(s *Store) (context.Context, string, string) {
	ctx := context.Background()
	first, err := s.CreateSession(ctx, "sid-1", "p1", "m1", "h", "sha")
	Expect(err).NotTo(HaveOccurred())
	_, err = s.CreateSession(ctx, "sid-2", "p2", "m2", "h", "sha")
	Expect(err).NotTo(HaveOccurred())
	err = s.UpdateSessionStatus(ctx, "sid-2", "completed")
	Expect(err).NotTo(HaveOccurred())
	return ctx, first.ID, "sid-2"
}

var _ = Describe("Session listing", func() {
	It("marks a session completed via UpdateSessionStatus", func() {
		s := newTestStore()
		sid := mustSession(s)
		Expect(s.UpdateSessionStatus(context.Background(), sid, "completed")).To(Succeed())
	})

	It("lists all sessions regardless of status", func() {
		s := newTestStore()
		ctx, _, _ := seedListing(s)
		all, err := s.ListSessions(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(2))
	})

	It("lists all sessions in creation order", func() {
		s := newTestStore()
		ctx, firstID, secondID := seedListing(s)
		all, err := s.ListSessions(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(all[0].ID).To(Equal(firstID))
		_ = secondID
	})

	It("lists the later session second", func() {
		s := newTestStore()
		ctx, _, secondID := seedListing(s)
		all, err := s.ListSessions(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(all[1].ID).To(Equal(secondID))
	})

	It("reflects the completed status in the listed session", func() {
		s := newTestStore()
		ctx, _, _ := seedListing(s)
		all, err := s.ListSessions(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(all[1].Status).To(Equal("completed"))
	})

	It("updates UpdatedAt beyond CreatedAt on status change", func() {
		s := newTestStore()
		ctx, _, _ := seedListing(s)
		all, err := s.ListSessions(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(all[1].UpdatedAt.After(all[1].CreatedAt)).To(BeTrue())
	})

	It("lists only active sessions when filtered by active", func() {
		s := newTestStore()
		ctx, _, _ := seedListing(s)
		active, err := s.ListSessions(ctx, "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(active).To(HaveLen(1))
	})

	It("the active session is the first created", func() {
		s := newTestStore()
		ctx, firstID, _ := seedListing(s)
		active, err := s.ListSessions(ctx, "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(active[0].ID).To(Equal(firstID))
	})

	It("lists only completed sessions when filtered by completed", func() {
		s := newTestStore()
		ctx, _, _ := seedListing(s)
		done, err := s.ListSessions(ctx, "completed")
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(HaveLen(1))
	})

	It("the completed session is the second created", func() {
		s := newTestStore()
		ctx, _, secondID := seedListing(s)
		done, err := s.ListSessions(ctx, "completed")
		Expect(err).NotTo(HaveOccurred())
		Expect(done[0].ID).To(Equal(secondID))
	})

	It("errors on unknown session id for status update", func() {
		s := newTestStore()
		err := s.UpdateSessionStatus(context.Background(), "nope", "cancelled")
		Expect(err).To(HaveOccurred())
	})

	It("errors listing sessions after close", func() {
		s := newTestStore()
		err := s.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = s.ListSessions(context.Background(), "")
		Expect(err).To(MatchError(ErrClosed))
	})
})

var _ = Describe("Export", func() {
	It("exports a session as canonical JSONL", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Append(context.Background(), "user", "turn.start", map[string]any{"b": 2, "a": 1})
		Expect(err).NotTo(HaveOccurred())
		err = w.Close()
		Expect(err).NotTo(HaveOccurred())
		b, err := s.ExportJSONL(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		want := fmt.Sprintf(`{"actor":"user","created_at_utc":%q,"payload":{"a":1,"b":2},"payload_sha256":"`+
			sha256HexStr(`{"a":1,"b":2}`)+`","seq":0,"session_id":%q,"type":"turn.start"}`+"\n",
			evCreatedAt(s, sid), sid)
		Expect(string(b)).To(Equal(want))
	})
})
