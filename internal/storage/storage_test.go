package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStorage(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Storage Suite")
}

// openT is arrange-only: any setup failure trips the exempt err-check pattern.
func openT() (*sql.DB, string, OpenInfo) {
	dir := GinkgoT().TempDir()
	db, info, err := Open(filepath.Join(dir, "t.db"))
	Expect(err).NotTo(HaveOccurred())
	err = Migrate(db)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = db.Close() })
	return db, dir, info
}

func appliedRows(db *sql.DB) []int {
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(rows.Close)
	var out []int
	for rows.Next() {
		var v int
		err := rows.Scan(&v)
		Expect(err).NotTo(HaveOccurred())
		out = append(out, v)
	}
	return out
}

var _ = Describe("Canonical fixtures", func() {
	for _, f := range []string{"key_order", "unicode", "numbers", "nested"} {
		fixture := f
		It("fixture "+fixture+" matches canonical bytes under the declared encoder", func() {
			b, err := os.ReadFile("testdata/" + fixture + ".json")
			Expect(err).NotTo(HaveOccurred())
			Expect(IsCanonical(string(b))).To(BeTrue(), "%s: fixture not canonical per current encoder", fixture)
		})
	}
})

var _ = Describe("Migrations", func() {
	It("reapply is a no-op recording no additional migrations", func() {
		db, _, _ := openT()
		before := len(appliedRows(db))
		err := Migrate(db)
		Expect(err).NotTo(HaveOccurred())
		Expect(appliedRows(db)).To(HaveLen(before))
	})

	It("records exactly one migration row", func() {
		db, _, _ := openT()
		Expect(appliedRows(db)).To(HaveLen(1))
	})

	for _, tbl := range []string{"sessions", "events", "receipts"} {
		table := tbl
		It("creates the "+table+" table", func() {
			db, _, _ := openT()
			var name string
			err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
			Expect(err).NotTo(HaveOccurred())
			Expect(name).To(Equal(table))
		})
	}
})

var _ = Describe("Open qualification", func() {
	It("records the mandated journal_mode pragma", func() {
		_, _, info := openT()
		Expect(info.Pragmas["journal_mode"]).To(Equal("wal"))
	})

	It("records the mandated foreign_keys pragma", func() {
		_, _, info := openT()
		Expect(info.Pragmas["foreign_keys"]).To(Equal("1"))
	})

	It("enforces journal_mode=wal on the live connection", func() {
		db, _, _ := openT()
		var mode string
		err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
		Expect(err).NotTo(HaveOccurred())
		Expect(mode).To(Equal("wal"))
	})

	It("runs a sqlite version at or above the qualified floor 3.40", func() {
		db, _, _ := openT()
		var sv string
		err := db.QueryRow(`SELECT sqlite_version()`).Scan(&sv)
		Expect(err).NotTo(HaveOccurred())
		var major, minor int
		_, err = fmt.Sscanf(sv, "%d.%d", &major, &minor)
		Expect(err).NotTo(HaveOccurred())
		Expect(major > 3 || (major == 3 && minor >= 40)).To(BeTrue(), "sqlite_version %q below qualified floor 3.40", sv)
	})

	It("records a non-empty driver version", func() {
		_, _, info := openT()
		Expect(info.DriverVersion).NotTo(BeEmpty())
	})

	It("records the modernc.org/sqlite driver name", func() {
		_, _, info := openT()
		Expect(info.DriverName).To(Equal("modernc.org/sqlite"))
	})

	It("enforces foreign keys by rejecting dangling inserts", func() {
		db, _, _ := openT()
		tx, err := db.Begin()
		Expect(err).NotTo(HaveOccurred())
		_, err = tx.Exec(`INSERT INTO events (session_id, seq, created_at_utc, actor, type, payload_sha256, payload_json)
		VALUES ('missing', 0, 'now', 'user', 'x', 'h', '{}')`)
		Expect(err).To(HaveOccurred(), "foreign_keys not enforced")
		err = tx.Rollback()
		Expect(err).NotTo(HaveOccurred())
	})
})

// concurrentWriters arranges the race scenario shared by the writer
// concurrency specs; it asserts nothing behavioural.
func concurrentWriters(db *sql.DB) (context.Context, string, []error) {
	w, sess, err := NewEventWriter(db, Session{Provider: "p", ModelID: "m", Harness: "t"})
	Expect(err).NotTo(HaveOccurred())
	ctx := context.Background()
	var mu sync.Mutex
	var failures []error
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				err := w.Append(ctx, EventIn{Actor: "provider:fake", Type: "provider.delta",
					Payload: map[string]any{"g": g, "i": i}})
				if err != nil {
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, err := ReadEvents(ctx, db, sess.ID); err != nil {
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
				return
			}
		}
	}()
	wg.Wait()
	return ctx, sess.ID, failures
}

var _ = Describe("Writer concurrency", func() {
	It("concurrent reads never fail under writer contention", func() {
		db, _, _ := openT()
		_, _, failures := concurrentWriters(db)
		Expect(failures).To(BeEmpty())
	})

	It("single owner keeps the sequence contiguous under race", func() {
		db, _, _ := openT()
		ctx, sid, _ := concurrentWriters(db)
		Expect(VerifyContiguous(ctx, db, sid)).To(Succeed())
	})
})

// replayableSession arranges a session with five appended events.
func replayableSession(db *sql.DB) (context.Context, string) {
	w, sess, err := NewEventWriter(db, Session{Provider: "p", ModelID: "m", Harness: "t"})
	Expect(err).NotTo(HaveOccurred())
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		err := w.Append(ctx, EventIn{Actor: "user", Type: "provider.delta", Payload: map[string]any{"i": i}})
		Expect(err).NotTo(HaveOccurred())
	}
	return ctx, sess.ID
}

var _ = Describe("Replay oracle", func() {
	It("replay fails on a corrupted digest", func() {
		db, _, _ := openT()
		ctx, sid := replayableSession(db)
		_, err := Replay(ctx, db, sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = db.Exec(`UPDATE events SET payload_sha256='deadbeef' WHERE session_id=? AND seq=2`, sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = Replay(ctx, db, sid)
		Expect(err).To(HaveOccurred(), "replay accepted corrupted digest")
	})

	It("restored fixture digests make replay succeed again", func() {
		db, _, _ := openT()
		ctx, sid := replayableSession(db)
		_, err := db.Exec(`UPDATE events SET payload_sha256='deadbeef' WHERE session_id=? AND seq=2`, sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = RestoreFixtureDigests(ctx, db, sid)
		Expect(err).NotTo(HaveOccurred())
		ok := func() bool { _, err := Replay(ctx, db, sid); return err == nil }()
		Expect(ok).To(BeTrue(), "replay rejected after digest restore")
	})

	It("replay fails on a corrupted sequence", func() {
		db, _, _ := openT()
		ctx, sid := replayableSession(db)
		_, err := db.Exec(`DELETE FROM events WHERE session_id=? AND seq=2`, sid)
		Expect(err).NotTo(HaveOccurred())
		_, err = Replay(ctx, db, sid)
		Expect(err).To(HaveOccurred(), "replay accepted corrupted sequence")
	})
})

var _ = Describe("Fixture generator", func() {
	It("writes frozen canonical-JSON fixtures when GEN_FIXTURES is set", func() {
		if os.Getenv("GEN_FIXTURES") == "" {
			return
		}
		dir := "testdata"
		cases := []struct {
			name string
			v    map[string]any
		}{
			{"key_order", map[string]any{"zeta": 1, "alpha": 2, "Mid": 3, "_under": 4}},
			{"unicode", map[string]any{"text": "héllo → 世界", "emoji": "🚀"}},
			{"numbers", map[string]any{"int": 3, "float": 1.5, "whole": 2.0, "big": 9007199254740993.0}},
			{"nested", map[string]any{"b": 1, "a": []any{1.25, 2, "x"}, "o": map[string]any{"y": true, "x": nil}}},
		}
		for _, c := range cases {
			canon, err := CanonicalJSON(c.v)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(dir, c.name+".json"), []byte(canon), 0o644)).To(Succeed())
		}
	})
})

var _ = Describe("T019 qualification capture", func() {
	It("emits the qualification evidence bundle when FLOWSTATE_T019_CAPTURE is set", func() {
		out := os.Getenv("FLOWSTATE_T019_CAPTURE")
		if out == "" {
			return
		}
		dir := GinkgoT().TempDir()
		db, info, err := Open(dir + "/t019.db")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = db.Close() })
		err = Migrate(db)
		Expect(err).NotTo(HaveOccurred())
		rows1 := appliedRows(db)
		err = Migrate(db)
		Expect(err).NotTo(HaveOccurred())
		rows2 := appliedRows(db)
		pragma := func(k string) string {
			var s string
			err := db.QueryRow("PRAGMA " + k).Scan(&s)
			Expect(err).NotTo(HaveOccurred())
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
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(out, b, 0o644)).To(Succeed())
	})
})
