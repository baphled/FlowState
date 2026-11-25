package store

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// freshDB creates a current database file and returns its path.
func freshDB() string {
	path := filepath.Join(GinkgoT().TempDir(), "fresh.db")
	st, err := Open(path)
	if err != nil {
		panic(err)
	}
	if err := st.Close(); err != nil {
		panic(err)
	}
	return path
}

// infoOf opens path read-only and returns its Info, panicking on error.
func infoOf(path string) Info {
	st, err := OpenReadOnly(path)
	if err != nil {
		panic(err)
	}
	defer func() { _ = st.Close() }()
	info, err := st.Info()
	if err != nil {
		panic(err)
	}
	return info
}

// tableKeys returns the foreign key count of the named table, -1 when absent.
func tableKeys(info Info, name string) int {
	for _, t := range info.Tables {
		if t.Name == name {
			return t.ForeignKeys
		}
	}
	return -1
}

var _ = Describe("Store.Info", func() {
	var path string
	BeforeEach(func() {
		path = freshDB()
	})

	It("reports the current schema version", func() {
		Expect(infoOf(path).SchemaVersion).To(Equal(knownSchemaVersion))
	})

	It("reports the file's journal mode", func() {
		Expect(infoOf(path).JournalMode).To(Equal("wal"))
	})

	It("reports that the handle enforces foreign keys", func() {
		Expect(infoOf(path).ForeignKeysEnforced).To(BeTrue())
	})

	It("reports the engine version", func() {
		Expect(infoOf(path).SQLiteVersion).To(MatchRegexp(`^3\.\d+\.\d+`))
	})

	It("counts the foreign keys the events table carries", func() {
		Expect(tableKeys(infoOf(path), "events")).To(Equal(1))
	})

	It("lists a table that declares no foreign key with none", func() {
		Expect(tableKeys(infoOf(path), "receipts")).To(Equal(0))
	})

	It("reports the version the engine itself answers", func() {
		var engine string
		withRaw(path, func(db *sql.DB) {
			if err := db.QueryRow("SELECT sqlite_version()").Scan(&engine); err != nil {
				panic(err)
			}
		})
		Expect(infoOf(path).SQLiteVersion).To(Equal(engine))
	})

	It("lists the tables in name order", func() {
		tables := infoOf(path).Tables
		Expect(sort.SliceIsSorted(tables, func(i, j int) bool { return tables[i].Name < tables[j].Name })).To(BeTrue())
	})

	It("hides SQLite's internal tables", func() {
		rawExec(path, "CREATE TABLE counted (id INTEGER PRIMARY KEY AUTOINCREMENT)")
		Expect(tableKeys(infoOf(path), "sqlite_sequence")).To(Equal(-1))
	})

	It("refuses a write on the read-only handle", func() {
		st, err := OpenReadOnly(path)
		if err != nil {
			panic(err)
		}
		defer func() { _ = st.Close() }()
		_, err = st.DB().Exec("CREATE TABLE intruder (id INTEGER)")
		Expect(err).To(MatchError(ContainSubstring("readonly")))
	})

	It("leaves the database and its WAL byte for byte unchanged while a writer holds it open", func() {
		live := filepath.Join(GinkgoT().TempDir(), "live.db")
		writer, err := Open(live)
		if err != nil {
			panic(err)
		}
		defer func() { _ = writer.Close() }()
		if fi, err := os.Stat(live + "-wal"); err != nil || fi.Size() == 0 {
			panic("expected a non-empty -wal beside the open database")
		}
		before := [2][32]byte{mustDigest(live), mustDigest(live + "-wal")}
		infoOf(live)
		Expect([2][32]byte{mustDigest(live), mustDigest(live + "-wal")}).To(Equal(before))
	})

	It("refuses a missing database", func() {
		_, err := OpenReadOnly(filepath.Join(GinkgoT().TempDir(), "absent.db"))
		Expect(err).To(MatchError(ErrNoDatabase))
	})

	It("creates no file for a missing database", func() {
		missing := filepath.Join(GinkgoT().TempDir(), "absent.db")
		if _, err := OpenReadOnly(missing); !errors.Is(err, ErrNoDatabase) {
			panic("expected ErrNoDatabase")
		}
		_, err := os.Stat(missing)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("reports a v8 database at version 8", func() {
		Expect(infoOf(seedV8(false)).SchemaVersion).To(Equal(int64(8)))
	})

	It("reports no foreign keys on a v8 events table", func() {
		Expect(tableKeys(infoOf(seedV8(false)), "events")).To(Equal(0))
	})

	It("leaves a v8 database file byte for byte unchanged", func() {
		v8 := seedV8(false)
		before := mustDigest(v8)
		infoOf(v8)
		Expect(mustDigest(v8)).To(Equal(before))
	})

	It("creates no backup beside a v8 database", func() {
		v8 := seedV8(false)
		infoOf(v8)
		entries, err := os.ReadDir(filepath.Dir(v8))
		if err != nil {
			panic(err)
		}
		Expect(entries).To(HaveLen(1))
	})
})

// siblingNames lists the entries of dir, minus SQLite's -wal and -shm sidecars.
func siblingNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		panic(err)
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); !strings.HasSuffix(n, "-wal") && !strings.HasSuffix(n, "-shm") {
			names = append(names, n)
		}
	}
	return names
}

// questionMarkEntries is the table entry for a file named with a "?", which
// Windows cannot create; there the pure fileDSN table covers the character.
func questionMarkEntries() []TableEntry {
	if runtime.GOOS == "windows" {
		return nil
	}
	return []TableEntry{Entry("a question mark", "q?x.db")}
}

// doubleSlashed puts a second leading slash on an absolute path; Windows
// has no such path form, so it is returned unchanged there.
func doubleSlashed(path string) string {
	if runtime.GOOS == "windows" {
		return path
	}
	return "/" + path
}

// moveTo renames the file at from to name in the same directory and returns the new path.
func moveTo(from, name string) string {
	to := filepath.Join(filepath.Dir(from), name)
	if err := os.Rename(from, to); err != nil {
		panic(err)
	}
	return to
}

var _ = Describe("Store.Info on a path SQLite would read as a URI", func() {
	DescribeTable("reports the named file's data",
		func(name string) {
			Expect(infoOf(moveTo(freshDB(), name)).SchemaVersion).To(Equal(knownSchemaVersion))
		},
		questionMarkEntries(),
		Entry("a hash", "h#x.db"),
		Entry("a percent escape", "p%25.db"),
		Entry("an ampersand", "a&b.db"),
	)

	DescribeTable("creates no sibling file",
		func(name string) {
			path := moveTo(freshDB(), name)
			infoOf(path)
			Expect(siblingNames(filepath.Dir(path))).To(ConsistOf(name))
		},
		questionMarkEntries(),
		Entry("a hash", "h#x.db"),
		Entry("a percent escape", "p%25.db"),
		Entry("an ampersand", "a&b.db"),
	)

	It("reads a path that begins with two slashes as the file it names", func() {
		Expect(infoOf(doubleSlashed(freshDB())).SchemaVersion).To(Equal(knownSchemaVersion))
	})

	DescribeTable("writes a relative path as a file name, never as a special database",
		func(path, want string) {
			Expect(fileDSN(path, "")).To(Equal(want))
		},
		Entry("the in-memory name", ":memory:", "file:./:memory:"),
		Entry("a plain name", "x.db", "file:./x.db"),
		Entry("a rooted path", "/x.db", "file:///x.db"),
		Entry("a dotted name", "./x.db", "file:./x.db"),
		Entry("a parent name", "../x.db", "file:../x.db"),
	)

	if runtime.GOOS != "windows" {
		It("opens only the crafted file and leaves the neighbouring database byte for byte unchanged", func() {
			neighbour := seedV8(false)
			rawExec(neighbour, "PRAGMA journal_mode=WAL")
			crafted := filepath.Join(filepath.Dir(neighbour), "v8.db?mode=rw&_pragma=journal_mode(DELETE)&x=")
			if err := os.WriteFile(crafted, nil, 0o600); err != nil {
				panic(err)
			}
			before := mustDigest(neighbour)
			infoOf(crafted)
			Expect(mustDigest(neighbour)).To(Equal(before))
		})

		It("creates no sibling when the crafted file is read", func() {
			neighbour := seedV8(false)
			crafted := filepath.Join(filepath.Dir(neighbour), "v8.db?mode=rw&x=")
			if err := os.WriteFile(crafted, nil, 0o600); err != nil {
				panic(err)
			}
			infoOf(crafted)
			Expect(siblingNames(filepath.Dir(neighbour))).To(ConsistOf("v8.db", "v8.db?mode=rw&x="))
		})

		It("opens a v8 database at a question mark path, migrates it and backs it up beside that name", func() {
			path := moveTo(seedV8(false), "q?x.db")
			st, err := Open(path)
			if err != nil {
				panic(err)
			}
			if err := st.Close(); err != nil {
				panic(err)
			}
			Expect(siblingNames(filepath.Dir(path))).To(ConsistOf("q?x.db", "q?x.db.pre-v9.bak"))
		})

		It("migrates the data of a v8 database at a percent path", func() {
			path := moveTo(seedV8(false), "p%25.db")
			st, err := Open(path)
			if err != nil {
				panic(err)
			}
			if err := st.Close(); err != nil {
				panic(err)
			}
			Expect(infoOf(path).SchemaVersion).To(Equal(knownSchemaVersion))
		})
	}
})

// mustDigest returns the sha256 of the file at path.
func mustDigest(path string) [32]byte {
	b, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return sha256.Sum256(b)
}
