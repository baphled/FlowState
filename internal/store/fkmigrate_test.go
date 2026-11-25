package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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

// rawText reads one string from the database file at path.
func rawText(path, q string) (out string) {
	withRaw(path, func(db *sql.DB) {
		if err := db.QueryRow(q).Scan(&out); err != nil {
			panic(err)
		}
	})
	return out
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

	for _, fragile := range []struct{ name, plant string }{
		{"a default containing the word REFERENCES", `DROP TABLE session_messages;
			CREATE TABLE session_messages (id TEXT PRIMARY KEY, session_id TEXT NOT NULL DEFAULT 'REFERENCES',
				sender TEXT NOT NULL, body TEXT NOT NULL, created_at_utc TEXT NOT NULL, delivered_at_utc TEXT);
			INSERT INTO session_messages VALUES ('m9','ghost','root','x','t',NULL)`},
		{"a key only on child_id", `DROP TABLE budget_reservations;
			CREATE TABLE budget_reservations (id TEXT PRIMARY KEY, parent_id TEXT NOT NULL,
				child_id TEXT NOT NULL REFERENCES sessions(id), granted INTEGER NOT NULL,
				consumed INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL, created_at_utc TEXT NOT NULL);
			INSERT INTO budget_reservations VALUES ('r9','ghost','child',1,0,'reserved','t')`},
	} {
		It("refuses its orphan before any backup with "+fragile.name, func() {
			path = seedV8(false)
			rawExec(path, fragile.plant)
			if _, err := Open(path); !errors.Is(err, ErrReferentialIntegrity) {
				panic(err)
			}
			_, err := os.Stat(path + ".pre-v9.bak")
			Expect(os.IsNotExist(err)).To(BeTrue())
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

var _ = Describe("Foreign-key migration of a v8 database", func() {
	var path string

	Context("with rows in every table", func() {
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

		It("records schema version 10", func() {
			v, err := s.SchemaVersion()
			Expect(err).NotTo(HaveOccurred())
			Expect(v).To(Equal(int64(10)))
		})

		It("preserves every row of every table", func() {
			Expect(dumpTables(path)).To(Equal(before))
		})

		It("gives every table exactly one foreign key", func() {
			Expect(keyCounts(path)).To(Equal([]int64{1, 1, 1, 1, 1, 1}))
		})

		It("leaves foreign_key_check clean", func() {
			Expect(rawInt(path, `SELECT count(*) FROM pragma_foreign_key_check`)).To(BeZero())
		})

		It("leaves foreign_keys ON on the store handle", func() {
			var on int
			if err := s.db.QueryRow(`SELECT foreign_keys FROM pragma_foreign_keys`).Scan(&on); err != nil {
				panic(err)
			}
			Expect(on).To(Equal(1))
		})

		It("keeps the events index the models declare", func() {
			Expect(rawInt(path, `SELECT count(*) FROM pragma_index_list('events') WHERE name='idx_events_session_type'`)).
				To(Equal(int64(1)))
		})

		It("leaves no temporary table behind", func() {
			Expect(rawInt(path, `SELECT count(*) FROM sqlite_master WHERE name LIKE '%\_\_old' ESCAPE '\'`)).To(BeZero())
		})

		It("writes a backup holding the pre-migration rows", func() {
			Expect(dumpTables(path + ".pre-v9.bak")).To(Equal(before))
		})

		It("writes the backup at its pre-migration schema version", func() {
			Expect(rawInt(path+".pre-v9.bak", `SELECT max(version) FROM schema_migrations`)).To(Equal(int64(8)))
		})

		It("does not run again on a second open", func() {
			if err := os.Remove(path + ".pre-v9.bak"); err != nil {
				panic(err)
			}
			if err := s.Close(); err != nil {
				panic(err)
			}
			again, err := Open(path)
			if err != nil {
				panic(err)
			}
			DeferCleanup(func() { _ = again.Close() })
			_, statErr := os.Stat(path + ".pre-v9.bak")
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		})

		It("enforces the new key on the store handle", func() {
			_, err := s.db.Exec(`INSERT INTO events VALUES ('ghost',0,'t','a','t','h','{}')`)
			Expect(err).To(HaveOccurred())
		})
	})

	Context("with '' stored as a root's parent", func() {
		var s *Store

		BeforeEach(func() {
			path = seedV8(true)
			var err error
			if s, err = Open(path); err != nil {
				panic(err)
			}
			DeferCleanup(func() { _ = s.Close() })
		})

		It("stores NULL instead", func() {
			Expect(rawInt(path, `SELECT parent_id IS NULL FROM sessions WHERE id='blank'`)).To(Equal(int64(1)))
		})

		It("still reports an empty ParentID through the Session API", func() {
			sess, err := s.GetSession(GinkgoT().Context(), "blank")
			Expect(err).NotTo(HaveOccurred())
			Expect(sess.ParentID).To(BeEmpty())
		})
	})

	Context("when the migration fails after rebuilding the first table", func() {
		var before map[string]string
		var openErr error

		BeforeEach(func() {
			path = seedV8(false)
			// A table the second rebuild cannot rename onto makes it fail.
			rawExec(path, `CREATE TABLE events__old (x TEXT)`)
			before = dumpTables(path)
			var s *Store
			s, openErr = Open(path)
			if s != nil {
				_ = s.Close()
			}
		})

		It("fails the open", func() {
			Expect(openErr).To(HaveOccurred())
		})

		It("rolls back every row", func() {
			Expect(dumpTables(path)).To(Equal(before))
		})

		It("rolls back the first table's key", func() {
			Expect(keyCounts(path)[0]).To(BeZero())
		})

		It("does not record version 9", func() {
			Expect(rawInt(path, `SELECT max(version) FROM schema_migrations`)).To(Equal(int64(8)))
		})

		It("leaves the backup in place", func() {
			Expect(dumpTables(path + ".pre-v9.bak")).To(Equal(before))
		})
	})

	It("refuses to overwrite an existing backup", func() {
		path = seedV8(false)
		if err := os.WriteFile(path+".pre-v9.bak", []byte("keep"), 0o600); err != nil {
			panic(err)
		}
		_, err := Open(path)
		Expect(err).To(MatchError(ErrBackupExists))
	})

	It("rejects a rebuilt table whose row count differs", func() {
		Expect(verifyRowCount("events", 3, 2)).To(MatchError(ErrRowCountMismatch))
	})

	It("writes no pre-v9 backup of a v8 database that already declares every key", func() {
		path = filepath.Join(GinkgoT().TempDir(), "keyed.db")
		fresh, err := Open(path)
		if err != nil {
			panic(err)
		}
		if err := fresh.Close(); err != nil {
			panic(err)
		}
		rawExec(path, `DELETE FROM schema_migrations WHERE version >= 9`)
		again, err := Open(path)
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = again.Close() })
		_, statErr := os.Stat(path + ".pre-v9.bak")
		Expect(os.IsNotExist(statErr)).To(BeTrue())
	})

	It("backs up a v8 database that already declares every key before v10", func() {
		path = seedV9("keyed.db", 8)
		again, err := Open(path)
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = again.Close() })
		Expect(rawInt(path+".pre-v10.bak", `SELECT max(version) FROM schema_migrations`)).To(Equal(int64(8)))
	})

	It("keeps the rowid order of the rows it copies", func() {
		path = seedV8(false)
		rawExec(path, `INSERT INTO swarm_run_packets VALUES ('b','run','pk',2,'r','t'), ('a','run','pk',3,'r','t')`)
		s, err := Open(path)
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = s.Close() })
		Expect(rawText(path, `SELECT group_concat(id) FROM (SELECT id FROM swarm_run_packets ORDER BY rowid)`)).
			To(Equal("p1,b,a"))
	})

	It("accepts on an upgraded database a status a fresh one accepts", func() {
		path = seedV8(false)
		s, err := Open(path)
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = s.Close() })
		Expect(s.UpdateSessionStatus(GinkgoT().Context(), "root", "archived")).To(Succeed())
	})

	It("writes no backup when an orphan refuses the open", func() {
		path = seedV8(false)
		rawExec(path, `INSERT INTO events VALUES ('ghost',0,'t','a','t','h','{}')`)
		if _, err := Open(path); err == nil {
			panic("open should refuse")
		}
		_, err := os.Stat(path + ".pre-v9.bak")
		Expect(os.IsNotExist(err)).To(BeTrue())
	})
})

var _ = Describe("Pre-v9 backup file", func() {
	var garbage *Store

	BeforeEach(func() {
		bad := filepath.Join(GinkgoT().TempDir(), "bad.db")
		if err := os.WriteFile(bad, []byte(strings.Repeat("x", 8192)), 0o600); err != nil {
			panic(err)
		}
		garbage = &Store{path: bad}
		if err := garbage.backupBeforeRebuild(); err == nil {
			panic("copying a file that is no database should fail")
		}
	})

	It("is absent under the final name when the copy fails", func() {
		_, err := os.Stat(garbage.backupPath())
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("leaves no temporary file when the copy fails", func() {
		left, err := filepath.Glob(garbage.backupPath() + ".tmp-*")
		if err != nil {
			panic(err)
		}
		Expect(left).To(BeEmpty())
	})

	Context("when another opener's temporary file is in progress", func() {
		var path, live string
		var before map[string]string

		BeforeEach(func() {
			path = seedV8(false)
			before = dumpTables(path)
			live = path + ".pre-v9.bak.tmp-0123456789abcdef"
			if err := os.WriteFile(live, []byte("in progress"), 0o600); err != nil {
				panic(err)
			}
			s, err := Open(path)
			if err != nil {
				panic(err)
			}
			DeferCleanup(func() { _ = s.Close() })
		})

		It("writes a complete backup of its own", func() {
			Expect(dumpTables(path + ".pre-v9.bak")).To(Equal(before))
		})

		It("leaves the other opener's file untouched", func() {
			body, err := os.ReadFile(live)
			if err != nil {
				panic(err)
			}
			Expect(string(body)).To(Equal("in progress"))
		})

		It("keeps the backup private to its owner", func() {
			want := os.FileMode(0o600)
			if runtime.GOOS == "windows" {
				want = 0o666 // Windows reports no POSIX permission bits
			}
			info, err := os.Stat(path + ".pre-v9.bak")
			if err != nil {
				panic(err)
			}
			Expect(info.Mode().Perm()).To(Equal(want))
		})
	})

	It("reports another process's migration when v9 is recorded and the backup exists", func() {
		path := filepath.Join(GinkgoT().TempDir(), "other.db")
		s, err := Open(path)
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = s.Close() })
		if err := os.WriteFile(path+".pre-v9.bak", []byte("theirs"), 0o600); err != nil {
			panic(err)
		}
		Expect(s.backupBeforeRebuild()).To(MatchError(ErrMigratedElsewhere))
	})

	It("keeps refusing with ErrBackupExists when v9 is not recorded", func() {
		path := seedV8(false)
		s, err := Open(path)
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = s.Close() })
		rawExec(path, `DELETE FROM schema_migrations WHERE version >= 9`)
		Expect(s.backupBeforeRebuild()).To(MatchError(ErrBackupExists))
	})
})

var _ = Describe("An in-memory store", func() {
	It("declares all six foreign keys", func() {
		s, err := OpenMemory()
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = s.Close() })
		var counts []int64
		for _, t := range keyedTables {
			var n int64
			if err := s.db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_list('` + t + `')`).Scan(&n); err != nil {
				panic(err)
			}
			counts = append(counts, n)
		}
		Expect(counts).To(Equal([]int64{1, 1, 1, 1, 1, 1}))
	})
})

// seedV9 writes a database as the build before v10 left it: a fresh database
// whose leases table lacks the liveness columns, one legacy lease row, and
// versions recorded up to top (8 or 9).
func seedV9(name string, top int64) string {
	path := filepath.Join(GinkgoT().TempDir(), name)
	fresh, err := Open(path)
	if err != nil {
		panic(err)
	}
	if err := fresh.Close(); err != nil {
		panic(err)
	}
	for _, q := range []string{
		fmt.Sprintf(`DELETE FROM schema_migrations WHERE version > %d`, top),
		`ALTER TABLE leases DROP COLUMN machine`,
		`ALTER TABLE leases DROP COLUMN host`,
		`ALTER TABLE leases DROP COLUMN pid`,
		`INSERT INTO leases (item, agent, token, acquired_at_utc) VALUES ('exec:old','cli',3,'t')`,
	} {
		rawExec(path, q)
	}
	return path
}

var _ = Describe("The v10 lease schema", func() {
	var path string

	openAt := func(p string) *Store {
		s, err := Open(p)
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = s.Close() })
		return s
	}
	exists := func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}

	Describe("a v9 database", func() {
		BeforeEach(func() {
			path = seedV9("v9.db", 9)
			openAt(path)
		})

		It("opens at version 10", func() {
			Expect(rawInt(path, `SELECT max(version) FROM schema_migrations`)).To(Equal(int64(10)))
		})

		It("gets a .pre-v10.bak", func() {
			Expect(exists(path + ".pre-v10.bak")).To(BeTrue())
		})

		It("writes that backup at version 9", func() {
			Expect(rawInt(path+".pre-v10.bak", `SELECT max(version) FROM schema_migrations`)).To(Equal(int64(9)))
		})

		It("writes that backup before the columns exist", func() {
			Expect(rawInt(path+".pre-v10.bak", `SELECT count(*) FROM pragma_table_info('leases') WHERE name='machine'`)).
				To(BeZero())
		})

		It("gets no .pre-v9.bak", func() {
			Expect(exists(path + ".pre-v9.bak")).To(BeFalse())
		})

		It("gains the three lease columns", func() {
			Expect(rawInt(path, `SELECT count(*) FROM pragma_table_info('leases') WHERE name IN ('machine','host','pid')`)).
				To(Equal(int64(3)))
		})

		It("declares them not null", func() {
			Expect(rawInt(path, `SELECT count(*) FROM pragma_table_info('leases')
				WHERE name IN ('machine','host','pid') AND "notnull" = 1`)).To(Equal(int64(3)))
		})

		It("keeps the lease row it had", func() {
			Expect(rawText(path, `SELECT agent || ':' || token FROM leases WHERE item='exec:old'`)).To(Equal("cli:3"))
		})

		It("reads the old row with no machine", func() {
			Expect(rawText(path, `SELECT machine FROM leases WHERE item='exec:old'`)).To(BeEmpty())
		})

		It("reads the old row with no host", func() {
			Expect(rawText(path, `SELECT host FROM leases WHERE item='exec:old'`)).To(BeEmpty())
		})

		It("reads the old row with pid 0", func() {
			Expect(rawInt(path, `SELECT pid FROM leases WHERE item='exec:old'`)).To(BeZero())
		})
	})

	It("takes only the pre-v9 backup of an unkeyed v8 database", func() {
		path = seedV8(false)
		openAt(path)
		Expect(exists(path + ".pre-v10.bak")).To(BeFalse())
	})

	It("takes the pre-v9 backup of an unkeyed v8 database", func() {
		path = seedV8(false)
		openAt(path)
		Expect(exists(path + ".pre-v9.bak")).To(BeTrue())
	})

	It("takes no backup of a database already at v10", func() {
		path = filepath.Join(GinkgoT().TempDir(), "v10.db")
		fresh, err := Open(path)
		if err != nil {
			panic(err)
		}
		if err := fresh.Close(); err != nil {
			panic(err)
		}
		openAt(path)
		Expect(exists(path + ".pre-v10.bak")).To(BeFalse())
	})

	It("is refused by a build that knows only version 9", func() {
		path = filepath.Join(GinkgoT().TempDir(), "barrier.db")
		openAt(path)
		Expect(openAt(path).rejectSchemaAbove(foreignKeyVersion)).To(MatchError(ErrFutureSchema))
	})

	It("is the version this build knows", func() {
		Expect(knownSchemaVersion).To(Equal(int64(10)))
	})

	It("keeps version 9 as the foreign-key version", func() {
		Expect(foreignKeyVersion).To(Equal(int64(9)))
	})

	It("reports another process's v10 migration when the v10 backup name is taken", func() {
		path = seedV9("taken.db", 9)
		s := openAt(path)
		if err := os.Remove(path + ".pre-v10.bak"); err != nil {
			panic(err)
		}
		if err := os.WriteFile(path+".pre-v10.bak", []byte("theirs"), 0o600); err != nil {
			panic(err)
		}
		Expect(s.backupBefore(leaseLivenessVersion)).To(MatchError(ErrMigratedElsewhere))
	})

	It("refuses to overwrite a v10 backup while v10 is not recorded", func() {
		path = seedV9("held.db", 9)
		if err := os.WriteFile(path+".pre-v10.bak", []byte("keep"), 0o600); err != nil {
			panic(err)
		}
		_, err := Open(path)
		Expect(err).To(MatchError(ErrBackupExists))
	})
})
