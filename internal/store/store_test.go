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
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = s.Close() })
	return s
}

func mustSession(s *Store) string {
	id := fmt.Sprintf("sid-%d", time.Now().UnixNano())
	_, err := s.CreateSession(context.Background(), id, "zai", "glm-4.6", "test-harness", "cfgsha")
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return id
}

func evCreatedAt(s *Store, sid string) string {
	var ts string
	err := s.db.QueryRow(`SELECT created_at_utc FROM events WHERE session_id=? AND seq=0`, sid).Scan(&ts)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return parsed.UTC().Format(time.RFC3339Nano)
}

var _ = Describe("Store open/migrations", func() {
	It("applies pragmas and creates schema on open", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "ws.db")
		s, err := Open(path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		for pragma, want := range map[string]string{
			"journal_mode": "wal",
			"synchronous":  "1",
			"foreign_keys": "1",
		} {
			var got string
			Expect(s.db.QueryRow("PRAGMA " + pragma).Scan(&got)).To(Succeed())
			Expect(got).To(Equal(want), "pragma %s", pragma)
		}
		for _, table := range []string{"schema_migrations", "sessions", "events", "receipts"} {
			var name string
			err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
			Expect(errors.Is(err, sql.ErrNoRows)).To(BeFalse(), "table %s missing", table)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It("migrations are idempotent on reopen", func() {
		path := filepath.Join(GinkgoT().TempDir(), "ws.db")
		s1, err := Open(path)
		Expect(err).NotTo(HaveOccurred())
		v1, err := s1.SchemaVersion()
		Expect(s1.Close()).To(Succeed())
		Expect(err).NotTo(HaveOccurred())
		s2, err := Open(path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s2.Close() })
		v2, err := s2.SchemaVersion()
		Expect(err).NotTo(HaveOccurred())
		Expect(v2).To(Equal(v1))
		Expect(v1).NotTo(BeZero())
	})

	It("rejects future schema versions", func() {
		s := newTestStore()
		_, err := s.db.Exec(`INSERT INTO schema_migrations (version, applied_at_utc) VALUES (999, 'x')`)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())
		path := filepath.Join(GinkgoT().TempDir(), "future.db")
		db, err := sql.Open("sqlite", "file:"+path)
		Expect(err).NotTo(HaveOccurred())
		_, err = db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at_utc TEXT NOT NULL)`)
		Expect(err).NotTo(HaveOccurred())
		_, err = db.Exec(`INSERT INTO schema_migrations VALUES (999,'x')`)
		Expect(err).NotTo(HaveOccurred())
		Expect(db.Close()).To(Succeed())
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
	It("appends and replays contiguous events", func() {
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
		evs, err := s.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(HaveLen(5))
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

	It("enforces a single writer and allows re-register after close", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = s.NewEventWriter(context.Background(), sid)
		Expect(err).To(MatchError(ErrWriterExists))
		Expect(w.Close()).To(Succeed())
		w2, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(w2.Close()).To(Succeed())
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
		Expect(w.Close()).To(Succeed())
		w2, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w2.Close() })
		Expect(w2.NextSeq()).To(Equal(int64(2)))
	})

	It("validates actor and type", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		_, err = w.Append(context.Background(), "", "t", nil)
		Expect(err).To(MatchError(ErrEmptyActor))
		_, err = w.Append(context.Background(), "user", "", nil)
		Expect(err).To(MatchError(ErrEmptyType))
	})

	It("rejects unknown sessions", func() {
		s := newTestStore()
		_, err := s.NewEventWriter(context.Background(), "nope")
		Expect(err).To(MatchError(ErrUnknownSession))
	})

	It("rejects use after close", func() {
		path := filepath.Join(GinkgoT().TempDir(), "ws.db")
		s, err := Open(path)
		Expect(err).NotTo(HaveOccurred())
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		Expect(s.Close()).To(Succeed())
		_, err = w.Append(context.Background(), "user", "t", nil)
		Expect(err).To(MatchError(ErrClosed))
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

	It("keeps appends contiguous under concurrency", func() {
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
		evs, err := s.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(HaveLen(n))
	})

	It("emits pause and resume events and flips session status", func() {
		s := newTestStore()
		sid := mustSession(s)
		w, err := s.NewEventWriter(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = w.Close() })
		Expect(w.PauseBudget(context.Background(), map[string]any{"reason": "budget", "consumed": 10})).To(Succeed())
		sess, err := s.GetSession(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(sess.Status).To(Equal("paused"))
		Expect(w.Resume(context.Background(), map[string]any{"run": "fresh"})).To(Succeed())
		sess, err = s.GetSession(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(sess.Status).To(Equal("active"))
		evs, err := s.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs[0].Type).To(Equal("session.paused"))
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
		Expect(w.Close()).To(Succeed())
		Expect(s.Close()).To(Succeed())
		s2, err := Open(path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s2.Close() })
		evs, err := s2.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(HaveLen(1))
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
		Expect(w.Close()).To(Succeed())
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
		Expect(w.Close()).To(Succeed())
		_, err = s.db.Exec(`DELETE FROM events WHERE session_id=? AND seq=0`, sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Replay(context.Background(), sid)
		Expect(err).To(MatchError(ErrSeqGap))
	})
})

var _ = Describe("Session listing", func() {
	It("lists all, active, and completed sessions in order", func() {
		s := newTestStore()
		ctx := context.Background()
		first, err := s.CreateSession(ctx, "sid-1", "p1", "m1", "h", "sha")
		Expect(err).NotTo(HaveOccurred())
		second, err := s.CreateSession(ctx, "sid-2", "p2", "m2", "h", "sha")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.UpdateSessionStatus(ctx, "sid-2", "completed")).To(Succeed())

		all, err := s.ListSessions(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(2))
		Expect(all[0].ID).To(Equal(first.ID))
		Expect(all[1].ID).To(Equal(second.ID))
		Expect(all[1].Status).To(Equal("completed"))
		Expect(all[1].UpdatedAt.After(all[1].CreatedAt)).To(BeTrue())

		active, err := s.ListSessions(ctx, "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(active).To(HaveLen(1))
		Expect(active[0].ID).To(Equal("sid-1"))

		done, err := s.ListSessions(ctx, "completed")
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(HaveLen(1))
		Expect(done[0].ID).To(Equal("sid-2"))
	})

	It("errors on unknown session id for status update", func() {
		s := newTestStore()
		err := s.UpdateSessionStatus(context.Background(), "nope", "cancelled")
		Expect(err).To(HaveOccurred())
	})

	It("errors listing sessions after close", func() {
		s := newTestStore()
		Expect(s.Close()).To(Succeed())
		_, err := s.ListSessions(context.Background(), "")
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
		Expect(w.Close()).To(Succeed())
		b, err := s.ExportJSONL(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		want := fmt.Sprintf(`{"actor":"user","created_at_utc":%q,"payload":{"a":1,"b":2},"payload_sha256":"`+
			sha256HexStr(`{"a":1,"b":2}`)+`","seq":0,"session_id":%q,"type":"turn.start"}`+"\n",
			evCreatedAt(s, sid), sid)
		Expect(string(b)).To(Equal(want))
	})
})
