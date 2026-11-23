package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	sqlite "github.com/libtnb/sqlite"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// keyedTables are the tables that reference a parent.
var keyedTables = []string{"sessions", "events", "budget_reservations", "session_messages",
	"swarm_run_packets", "swarm_run_children"}

// dataTables are compared before and after; schema_migrations is not,
// because the migration appends a version row.
var dataTables = append([]string{"swarm_runs"}, keyedTables...)

// seedV8 writes a v8 database as origin/next before #421 created it: the
// models migrated with no foreign key constraint, versions 1..8 recorded,
// and rows in every table. blank stores ” as a root's parent.
func seedV8(blank bool) string {
	path := filepath.Join(GinkgoT().TempDir(), "v8.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		panic(err)
	}
	defer func() { _ = raw.Close() }()
	g, err := gorm.Open(sqlite.New(sqlite.Config{Conn: raw}),
		&gorm.Config{Logger: logger.Discard, DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		panic(err)
	}
	if err := g.AutoMigrate(migrateModels()...); err != nil {
		panic(err)
	}
	stmts := []string{
		`INSERT INTO schema_migrations (version, applied_at_utc) VALUES (1,'t'),(2,'t'),(3,'t'),(4,'t'),(5,'t'),(6,'t'),(7,'t'),(8,'t')`,
		`INSERT INTO sessions VALUES ('root','c','u','active','zai','m','pi','sha',NULL,100),
			('child','c','u','paused','zai','m','pi','sha','root',NULL)`,
		`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i<599)
			INSERT INTO events SELECT 'root', i, 't', 'user', 'tick', 'h', '{"i":' || i || '}' FROM n`,
		`INSERT INTO events VALUES ('child',0,'t','user','tick','h','{}')`,
		`INSERT INTO budget_reservations VALUES ('r1','root','child',10,2,'reserved','t')`,
		`INSERT INTO session_messages VALUES ('m1','child','root','go','t',NULL)`,
		`INSERT INTO swarm_runs VALUES ('run','swarm','sha','ok','t')`,
		`INSERT INTO swarm_run_packets VALUES ('p1','run','pk',1,'fine','t')`,
		`INSERT INTO swarm_run_children VALUES ('c1','run','pk','child','t')`,
	}
	if blank {
		stmts = append(stmts, `INSERT INTO sessions VALUES ('blank','c','u','active','zai','m','pi','sha','',NULL)`)
	}
	for _, q := range stmts {
		if _, err := raw.Exec(q); err != nil {
			panic(err)
		}
	}
	return path
}

// withRaw runs f on a plain handle to the database file at path.
func withRaw(path string, f func(db *sql.DB)) {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		panic(err)
	}
	defer func() { _ = db.Close() }()
	f(db)
}

// rawExec runs one statement against the database file at path.
func rawExec(path, q string) {
	withRaw(path, func(db *sql.DB) {
		if _, err := db.Exec(q); err != nil {
			panic(err)
		}
	})
}

// rawInt reads one integer from the database file at path.
func rawInt(path, q string) (n int64) {
	withRaw(path, func(db *sql.DB) {
		if err := db.QueryRow(q).Scan(&n); err != nil {
			panic(err)
		}
	})
	return n
}

// dumpTables renders every row of every data table, in primary key order.
func dumpTables(path string) (out map[string]string) {
	out = map[string]string{}
	withRaw(path, func(db *sql.DB) {
		for _, t := range dataTables {
			rows, err := db.Query(`SELECT * FROM ` + t + ` ORDER BY 1, 2`)
			if err != nil {
				panic(err)
			}
			cols, _ := rows.Columns()
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					panic(err)
				}
				out[t] += fmt.Sprintf("%#v\n", vals)
			}
			_ = rows.Close()
		}
	})
	return out
}

// keyCounts reports how many foreign keys each keyed table declares.
func keyCounts(path string) (out []int64) {
	for _, t := range keyedTables {
		out = append(out, rawInt(path, `SELECT count(*) FROM pragma_foreign_key_list('`+t+`')`))
	}
	return out
}

var _ = Describe("Referential-integrity check of a database without foreign keys", func() {
	var path string

	Context("with no orphan rows", func() {
		var before map[string]string
		var s *Store

		BeforeEach(func() {
			path = seedV8(false)
			before = dumpTables(path)
			var err error
			if s, err = Open(path); err != nil {
				panic(err)
			}
			DeferCleanup(func() { _ = s.Close() })
		})

		It("preserves every row of every table", func() {
			Expect(dumpTables(path)).To(Equal(before))
		})

		It("adds no foreign key yet", func() {
			Expect(keyCounts(path)).To(Equal([]int64{0, 0, 0, 0, 0, 0}))
		})

		It("keeps the recorded schema version", func() {
			v, err := s.SchemaVersion()
			Expect(err).NotTo(HaveOccurred())
			Expect(v).To(Equal(int64(8)))
		})
	})

	It("accepts '' stored as a root's parent", func() {
		path = seedV8(true)
		s, err := Open(path)
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.DB()).NotTo(BeNil())
	})

	It("leaves a table that declares its key to SQLite", func() {
		fresh := filepath.Join(GinkgoT().TempDir(), "keyed.db")
		s, err := Open(fresh)
		if err != nil {
			panic(err)
		}
		if err := s.Close(); err != nil {
			panic(err)
		}
		rawExec(fresh, `INSERT INTO events VALUES ('ghost',0,'t','a','t','h','{}')`)
		again, err := Open(fresh)
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = again.Close() })
		Expect(again.DB()).NotTo(BeNil())
	})

	for _, orphan := range []struct{ name, plant string }{
		{"an orphan event", `INSERT INTO events VALUES ('ghost',0,'t','a','t','h','{}')`},
		{"an orphan child session", `INSERT INTO sessions VALUES ('kid','c','u','active','z','m','p','s','ghost',NULL)`},
		{"a dangling reservation", `INSERT INTO budget_reservations VALUES ('r2','ghost','x',1,0,'reserved','t')`},
		{"a session naming itself", `INSERT INTO sessions VALUES ('me','c','u','active','z','m','p','s','me',NULL)`},
		{"an orphan swarm packet", `INSERT INTO swarm_run_packets VALUES ('p9','ghost','pk',1,'r','t')`},
		{"an orphan steering note", `INSERT INTO session_messages VALUES ('m9','ghost','root','x','t',NULL)`},
		{"an orphan swarm child run", `INSERT INTO swarm_run_children VALUES ('c9','ghost','pk','child','t')`},
	} {
		Context("with "+orphan.name, func() {
			var before map[string]string
			var openErr error

			BeforeEach(func() {
				path = seedV8(false)
				rawExec(path, orphan.plant)
				before = dumpTables(path)
				var s *Store
				s, openErr = Open(path)
				if s != nil {
					_ = s.Close()
				}
			})

			It("refuses to open with the typed error", func() {
				Expect(openErr).To(MatchError(ErrReferentialIntegrity))
			})

			It("changes no row", func() {
				Expect(dumpTables(path)).To(Equal(before))
			})

			It("leaves the schema version at 8", func() {
				Expect(rawInt(path, `SELECT max(version) FROM schema_migrations`)).To(Equal(int64(8)))
			})

			It("leaves every table without a key", func() {
				Expect(keyCounts(path)).To(Equal([]int64{0, 0, 0, 0, 0, 0}))
			})
		})
	}

	It("names the table, row and missing parent of an orphan", func() {
		path = seedV8(false)
		rawExec(path, `INSERT INTO events VALUES ('ghost',7,'t','a','t','h','{}')`)
		_, err := Open(path)
		var refusal *ReferentialIntegrityError
		if !errors.As(err, &refusal) {
			panic(err)
		}
		Expect(refusal.Orphans).To(Equal([]OrphanRow{{Table: "events", RowID: "ghost/7",
			Column: "session_id", Missing: "ghost", Reason: "missing parent"}}))
	})

	It("names a session that references itself", func() {
		path = seedV8(false)
		rawExec(path, `INSERT INTO sessions VALUES ('me','c','u','active','z','m','p','s','me',NULL)`)
		_, err := Open(path)
		var refusal *ReferentialIntegrityError
		if !errors.As(err, &refusal) {
			panic(err)
		}
		Expect(refusal.Orphans).To(Equal([]OrphanRow{{Table: "sessions", RowID: "me",
			Column: "parent_id", Missing: "me", Reason: "references itself"}}))
	})

	It("bounds the orphans a refusal names", func() {
		path = seedV8(false)
		rawExec(path, `WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i<29)
			INSERT INTO events SELECT 'ghost', i, 't', 'a', 't', 'h', '{}' FROM n`)
		_, err := Open(path)
		var refusal *ReferentialIntegrityError
		if !errors.As(err, &refusal) {
			panic(err)
		}
		Expect([]int{len(refusal.Orphans), refusal.Total}).To(Equal([]int{maxReportedOrphans, 30}))
	})
})
