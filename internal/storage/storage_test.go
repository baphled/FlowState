package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStorage(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Storage Suite")
}

// openT is arrange-only: any setup failure panics.
func openT() (*sql.DB, string, OpenInfo) {
	dir := GinkgoT().TempDir()
	db, info, err := Open(filepath.Join(dir, "t.db"))
	if err != nil {
		panic(err)
	}
	if err := Migrate(db); err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = db.Close() })
	return db, dir, info
}

func appliedRows(db *sql.DB) []int {
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		panic(err)
	}
	defer func() { _ = rows.Close() }()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			panic(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		panic(err)
	}
	return out
}

var _ = Describe("Migrations", func() {
	var db *sql.DB

	BeforeEach(func() {
		db, _, _ = openT()
	})

	It("reapply is a no-op recording no additional migrations", func() {
		before := len(appliedRows(db))
		if err := Migrate(db); err != nil {
			panic(err)
		}
		Expect(appliedRows(db)).To(HaveLen(before))
	})

	It("records exactly the two migration rows", func() {
		Expect(appliedRows(db)).To(HaveLen(2))
	})

	DescribeTable("creates the table",
		func(table string) {
			var name string
			err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
			if err != nil {
				panic(err)
			}
			Expect(name).To(Equal(table))
		},
		Entry("sessions", "sessions"),
		Entry("events", "events"),
		Entry("receipts", "receipts"),
	)
})

var _ = Describe("Open qualification", func() {
	var (
		db   *sql.DB
		info OpenInfo
	)

	BeforeEach(func() {
		db, _, info = openT()
	})

	It("records the mandated journal_mode pragma", func() {
		Expect(info.Pragmas["journal_mode"]).To(Equal("wal"))
	})

	It("records the mandated foreign_keys pragma", func() {
		Expect(info.Pragmas["foreign_keys"]).To(Equal("1"))
	})

	It("enforces journal_mode=wal on the live connection", func() {
		var mode string
		if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
			panic(err)
		}
		Expect(mode).To(Equal("wal"))
	})

	It("runs a sqlite version at or above the qualified floor 3.40", func() {
		var sv string
		if err := db.QueryRow(`SELECT sqlite_version()`).Scan(&sv); err != nil {
			panic(err)
		}
		var major, minor int
		if _, err := fmt.Sscanf(sv, "%d.%d", &major, &minor); err != nil {
			panic(err)
		}
		Expect(major > 3 || (major == 3 && minor >= 40)).To(BeTrue(), "sqlite_version %q below qualified floor 3.40", sv)
	})

	It("records a non-empty driver version", func() {
		Expect(info.DriverVersion).NotTo(BeEmpty())
	})

	It("records the modernc.org/sqlite driver name", func() {
		Expect(info.DriverName).To(Equal("modernc.org/sqlite"))
	})

	It("enforces foreign keys by rejecting dangling inserts", func() {
		tx, err := db.Begin()
		if err != nil {
			panic(err)
		}
		defer func() { _ = tx.Rollback() }()
		_, err = tx.Exec(`INSERT INTO events (session_id, seq, created_at_utc, actor, type, payload_sha256, payload_json)
		VALUES ('missing', 0, 'now', 'user', 'x', 'h', '{}')`)
		Expect(err).To(HaveOccurred(), "foreign_keys not enforced")
	})
})

// captureQualification writes the T019 bundle to an explicit destination. It
// retains the capture capability without environment-gated passing specs or
// writes to shared source fixtures.
func captureQualification(db *sql.DB, info OpenInfo, out string) error {
	rows := func() ([]int, error) {
		result, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
		if err != nil {
			return nil, err
		}
		defer result.Close()
		versions := []int{}
		for result.Next() {
			var v int
			if err := result.Scan(&v); err != nil {
				return nil, err
			}
			versions = append(versions, v)
		}
		return versions, result.Err()
	}
	first, err := rows()
	if err != nil {
		return err
	}
	if err := Migrate(db); err != nil {
		return err
	}
	second, err := rows()
	if err != nil {
		return err
	}
	pragmas := map[string]string{}
	for _, key := range []string{"journal_mode", "foreign_keys", "synchronous", "busy_timeout"} {
		var value string
		if err := db.QueryRow("PRAGMA " + key).Scan(&value); err != nil {
			return err
		}
		pragmas[key] = value
	}
	b, err := json.MarshalIndent(map[string]any{
		"open_info":                      info,
		"effective_pragmas":              pragmas,
		"applied_migrations_first_apply": first,
		"applied_migrations_reapply":     second,
		"reapply_is_noop":                len(first) == len(second),
		"go_version":                     runtime.Version(),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(out, b, 0o644)
}

var _ = Describe("T019 qualification capture", func() {
	var captured map[string]json.RawMessage
	BeforeEach(func() {
		db, dir, info := openT()
		out := filepath.Join(dir, "qualification.json")
		if err := captureQualification(db, info, out); err != nil {
			panic(err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			panic(err)
		}
		if err := json.Unmarshal(b, &captured); err != nil {
			panic(err)
		}
	})
	It("captures the modernc driver identity", func() {
		var info OpenInfo
		if err := json.Unmarshal(captured["open_info"], &info); err != nil {
			panic(err)
		}
		Expect(info.DriverName).To(Equal("modernc.org/sqlite"))
	})
	DescribeTable("captures the effective pragma", func(key, want string) {
		var pragmas map[string]string
		if err := json.Unmarshal(captured["effective_pragmas"], &pragmas); err != nil {
			panic(err)
		}
		Expect(pragmas[key]).To(Equal(want))
	},
		Entry("journal mode", "journal_mode", "wal"),
		Entry("foreign keys", "foreign_keys", "1"),
		Entry("synchronous", "synchronous", "1"),
		Entry("busy timeout", "busy_timeout", "10000"),
	)
	for _, key := range []string{"applied_migrations_first_apply", "applied_migrations_reapply"} {
		field := key
		It("captures "+field, func() {
			var versions []int
			if err := json.Unmarshal(captured[field], &versions); err != nil {
				panic(err)
			}
			Expect(versions).To(Equal([]int{1, 2}))
		})
	}
	It("captures the migration no-op result", func() {
		var noop bool
		if err := json.Unmarshal(captured["reapply_is_noop"], &noop); err != nil {
			panic(err)
		}
		Expect(noop).To(BeTrue())
	})
	It("captures the executing Go version", func() {
		var version string
		if err := json.Unmarshal(captured["go_version"], &version); err != nil {
			panic(err)
		}
		Expect(version).To(Equal(runtime.Version()))
	})
})
