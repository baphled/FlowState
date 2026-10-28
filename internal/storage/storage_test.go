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

func openT() (*sql.DB, string, OpenInfo) {
	dir := GinkgoT().TempDir()
	db, info, err := Open(filepath.Join(dir, "t.db"))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	Expect(info.Pragmas["journal_mode"]).To(Equal("wal"))
	Expect(info.Pragmas["foreign_keys"]).To(Equal("1"))
	ExpectWithOffset(1, Migrate(db)).To(Succeed())
	return db, dir, info
}

func appliedRows(db *sql.DB) []int {
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	DeferCleanup(rows.Close)
	var out []int
	for rows.Next() {
		var v int
		ExpectWithOffset(1, rows.Scan(&v)).To(Succeed())
		out = append(out, v)
	}
	return out
}

var _ = Describe("Canonical fixtures", func() {
	It("frozen fixtures match canonical bytes under the declared encoder", func() {
		for _, f := range []string{"key_order", "unicode", "numbers", "nested"} {
			b, err := os.ReadFile("testdata/" + f + ".json")
			Expect(err).NotTo(HaveOccurred())
			Expect(IsCanonical(string(b))).To(BeTrue(), "%s: fixture not canonical per current encoder", f)
		}
	})
})

var _ = Describe("Migrations", func() {
	It("reapply is a no-op and all schema objects exist", func() {
		db, _, _ := openT()
		DeferCleanup(func() { _ = db.Close() })
		Expect(Migrate(db)).To(Succeed())
		var n int
		Expect(db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n)).To(Succeed())
		Expect(n).To(Equal(1))
		for _, tbl := range []string{"sessions", "events", "receipts"} {
			_, err := db.Exec(`SELECT 1 FROM ` + tbl + ` LIMIT 1`)
			Expect(err).NotTo(HaveOccurred(), "table %s unusable", tbl)
		}
	})
})

var _ = Describe("Open qualification", func() {
	It("records mandated pragmas and versions", func() {
		db, _, info := openT()
		DeferCleanup(func() { _ = db.Close() })
		var mode string
		Expect(db.QueryRow(`PRAGMA journal_mode`).Scan(&mode)).To(Succeed())
		Expect(mode).To(Equal("wal"))
		var sv string
		Expect(db.QueryRow(`SELECT sqlite_version()`).Scan(&sv)).To(Succeed())
		var major, minor int
		_, err := fmt.Sscanf(sv, "%d.%d", &major, &minor)
		Expect(err).NotTo(HaveOccurred())
		Expect(major > 3 || (major == 3 && minor >= 40)).To(BeTrue(), "sqlite_version %q below qualified floor 3.40", sv)
		Expect(info.DriverVersion).NotTo(BeEmpty())
		Expect(info.DriverName).To(Equal("modernc.org/sqlite"))
		tx, err := db.Begin()
		Expect(err).NotTo(HaveOccurred())
		_, err = tx.Exec(`INSERT INTO events (session_id, seq, created_at_utc, actor, type, payload_sha256, payload_json)
		VALUES ('missing', 0, 'now', 'user', 'x', 'h', '{}')`)
		Expect(err).To(HaveOccurred(), "foreign_keys not enforced")
		Expect(tx.Rollback()).To(Succeed())
	})
})

var _ = Describe("Writer concurrency", func() {
	It("single owner keeps sequence contiguous under race", func() {
		db, _, _ := openT()
		DeferCleanup(func() { _ = db.Close() })
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
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
					return
				}
			}
		}()
		wg.Wait()
		Expect(failures).To(BeEmpty())
		Expect(VerifyContiguous(ctx, db, sess.ID)).To(Succeed())
	})
})

var _ = Describe("Replay oracle", func() {
	It("fails on corrupted digest or sequence", func() {
		db, _, _ := openT()
		DeferCleanup(func() { _ = db.Close() })
		w, sess, err := NewEventWriter(db, Session{Provider: "p", ModelID: "m", Harness: "t"})
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		for i := 0; i < 5; i++ {
			Expect(w.Append(ctx, EventIn{Actor: "user", Type: "provider.delta", Payload: map[string]any{"i": i}})).To(Succeed())
		}
		_, err = Replay(ctx, db, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = db.Exec(`UPDATE events SET payload_sha256='deadbeef' WHERE session_id=? AND seq=2`, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = Replay(ctx, db, sess.ID)
		Expect(err).To(HaveOccurred(), "replay accepted corrupted digest")
		_, err = RestoreFixtureDigests(ctx, db, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = Replay(ctx, db, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = db.Exec(`DELETE FROM events WHERE session_id=? AND seq=2`, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = Replay(ctx, db, sess.ID)
		Expect(err).To(HaveOccurred(), "replay accepted corrupted sequence")
	})
})

var _ = Describe("Fixture generator", func() {
	It("writes frozen canonical-JSON fixtures when GEN_FIXTURES is set", func() {
		if os.Getenv("GEN_FIXTURES") == "" {
			Skip("set GEN_FIXTURES=1 to regenerate fixtures")
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
			Skip("FLOWSTATE_T019_CAPTURE not set")
		}
		dir := GinkgoT().TempDir()
		db, info, err := Open(dir + "/t019.db")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = db.Close() })
		Expect(Migrate(db)).To(Succeed())
		rows1 := appliedRows(db)
		Expect(Migrate(db)).To(Succeed())
		rows2 := appliedRows(db)
		pragma := func(k string) string {
			var s string
			Expect(db.QueryRow("PRAGMA " + k).Scan(&s)).To(Succeed())
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
