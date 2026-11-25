package store

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	lockChildEnv  = "FLOWSTATE_LOCK_CHILD"
	lockChildDB   = "FLOWSTATE_LOCK_DB"
	lockChildItem = "FLOWSTATE_LOCK_ITEM"
	childHeld     = 7
)

// init turns this test binary into a lock helper when re-invoked by the
// specs below: "try" exits 0 when it took the lock and 7 when it was held;
// "hold" takes the lock, prints "locked" and waits for stdin to close.
func init() {
	verb := os.Getenv(lockChildEnv)
	if verb == "" {
		return
	}
	l, err := AcquireItemLock(NewLockTable(), os.Getenv(lockChildDB), os.Getenv(lockChildItem))
	if errors.Is(err, ErrLockHeld) {
		os.Exit(childHeld)
	}
	if err != nil {
		os.Exit(1)
	}
	if verb == "hold" {
		_, _ = os.Stdout.WriteString("locked\n")
		_, _ = os.Stdin.Read(make([]byte, 1))
	}
	_ = l.Release()
	os.Exit(0)
}

func lockChild(verb, db, item string) *exec.Cmd {
	cmd := exec.CommandContext(context.Background(), os.Args[0])
	cmd.Env = append(os.Environ(), lockChildEnv+"="+verb, lockChildDB+"="+db, lockChildItem+"="+item)
	return cmd
}

func lockChildExit(verb, db, item string) int {
	err := lockChild(verb, db, item).Run()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	panic(err)
}

func newLockDB() string {
	p := filepath.Join(GinkgoT().TempDir(), "f.db")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		panic(err)
	}
	return p
}

func mustTableLock(table *LockTable, item string) *LeaseLock {
	l, err := AcquireItemLock(table, "", item)
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = l.Release() })
	return l
}

func mustLock(db, item string) *LeaseLock {
	l, err := AcquireItemLock(nil, db, item)
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = l.Release() })
	return l
}

var _ = Describe("lease lock primitive", func() {
	var db string

	BeforeEach(func() { db = newLockDB() })

	Describe("on one database file", func() {
		It("refuses a second acquire of the same item in one process", func() {
			mustLock(db, "exec:1")
			_, err := AcquireItemLock(nil, db, "exec:1")
			Expect(err).To(MatchError(ErrLockHeld))
		})

		It("allows a different item", func() {
			mustLock(db, "exec:1")
			l, err := AcquireItemLock(nil, db, "exec:2")
			if err == nil {
				DeferCleanup(func() { _ = l.Release() })
			}
			Expect(l).NotTo(BeNil())
		})

		It("allows the item again after release", func() {
			l := mustLock(db, "exec:1")
			if err := l.Release(); err != nil {
				panic(err)
			}
			again, err := AcquireItemLock(nil, db, "exec:1")
			if err == nil {
				DeferCleanup(func() { _ = again.Release() })
			}
			Expect(again).NotTo(BeNil())
		})

		It("makes a repeated release a no-op", func() {
			l := mustLock(db, "exec:1")
			if err := l.Release(); err != nil {
				panic(err)
			}
			Expect(l.Release()).To(Succeed())
		})

		It("refuses a child process while held", func() {
			mustLock(db, "exec:1")
			Expect(lockChildExit("try", db, "exec:1")).To(Equal(childHeld))
		})

		It("lets a child process take a free item", func() {
			Expect(lockChildExit("try", db, "exec:1")).To(Equal(0))
		})

		It("lets a child process take the item after release", func() {
			l := mustLock(db, "exec:1")
			if err := l.Release(); err != nil {
				panic(err)
			}
			Expect(lockChildExit("try", db, "exec:1")).To(Equal(0))
		})

		It("refuses the same file named by a relative path", func() {
			mustLock(db, "exec:1")
			rel := inDirOf(db)
			_, err := AcquireItemLock(nil, "./"+rel, "exec:1")
			Expect(err).To(MatchError(ErrLockHeld))
		})

		It("refuses a child that names the file by a relative path", func() {
			mustLock(db, "exec:1")
			rel := inDirOf(db)
			Expect(lockChildExit("try", rel, "exec:1")).To(Equal(childHeld))
		})
	})

	Describe("lock identity", func() {
		It("keeps the lock files in a directory beside the resolved file", func() {
			id, err := ResolveLockIdentity(db)
			if err != nil {
				panic(err)
			}
			dir, _, _ := LockPath(id, "exec:1")
			Expect(dir).To(Equal(id.Path + ".locks"))
		})

		It("fans the lock file out by the first two hex digits of its name", func() {
			id, err := ResolveLockIdentity(db)
			if err != nil {
				panic(err)
			}
			_, fan, name := LockPath(id, "exec:1")
			Expect(name[:2]).To(Equal(fan))
		})

		It("keeps an item that looks like a path inside the lock directory", func() {
			id, err := ResolveLockIdentity(db)
			if err != nil {
				panic(err)
			}
			_, _, name := LockPath(id, "../../escape")
			Expect(filepath.Base(name)).To(Equal(name))
		})

		It("separates a database recreated at the same path", func() {
			id, err := ResolveLockIdentity(db)
			if err != nil {
				panic(err)
			}
			keep := db + ".moved"
			if err := os.Rename(db, keep); err != nil {
				panic(err)
			}
			if err := os.WriteFile(db, nil, 0o600); err != nil {
				panic(err)
			}
			next, err := ResolveLockIdentity(db)
			if err != nil {
				panic(err)
			}
			_, _, before := LockPath(id, "exec:1")
			_, _, after := LockPath(next, "exec:1")
			Expect(after).NotTo(Equal(before))
		})

		It("makes the resolved path absolute", func() {
			rel := inDirOf(db)
			id, err := ResolveLockIdentity(rel)
			if err != nil {
				panic(err)
			}
			Expect(filepath.IsAbs(id.Path)).To(BeTrue())
		})

		It("reports a missing database", func() {
			_, err := ResolveLockIdentity(db + ".missing")
			Expect(err).To(MatchError(os.ErrNotExist))
		})
	})

	Describe("the descriptor-versus-path check", func() {
		var (
			root *os.Root
			f    *os.File
		)

		BeforeEach(func() {
			var err error
			if root, err = os.OpenRoot(filepath.Dir(db)); err != nil {
				panic(err)
			}
			DeferCleanup(func() { _ = root.Close() })
			if f, err = openLockFile(root, "x.lock"); err != nil {
				panic(err)
			}
			DeferCleanup(func() { _ = f.Close() })
		})

		It("accepts an unchanged lock file", func() {
			same, err := sameAsPath(f, root, "x.lock")
			if err != nil {
				panic(err)
			}
			Expect(same).To(BeTrue())
		})

		// An open file cannot be deleted on Windows, so the replacement exists on other systems only.
		if runtime.GOOS != "windows" {
			It("detects a lock file removed and recreated after open", func() {
				if err := root.Remove("x.lock"); err != nil {
					panic(err)
				}
				g, err := openLockFile(root, "x.lock")
				if err != nil {
					panic(err)
				}
				DeferCleanup(func() { _ = g.Close() })
				same, err := sameAsPath(f, root, "x.lock")
				if err != nil {
					panic(err)
				}
				Expect(same).To(BeFalse())
			})
		}
	})

	Describe("a store without a path", func() {
		var table *LockTable

		BeforeEach(func() { table = NewLockTable() })

		It("refuses a second acquire of the same item on one table", func() {
			mustTableLock(table, "exec:mem")
			_, err := AcquireItemLock(table, "", "exec:mem")
			Expect(err).To(MatchError(ErrLockHeld))
		})

		It("does not conflict across two tables for the same item", func() {
			mustTableLock(table, "exec:mem")
			l, err := AcquireItemLock(NewLockTable(), "", "exec:mem")
			if err == nil {
				DeferCleanup(func() { _ = l.Release() })
			}
			Expect(l).NotTo(BeNil())
		})

		It("allows the item again after release", func() {
			l := mustTableLock(table, "exec:mem")
			if err := l.Release(); err != nil {
				panic(err)
			}
			again, err := AcquireItemLock(table, "", "exec:mem")
			if err == nil {
				DeferCleanup(func() { _ = again.Release() })
			}
			Expect(again).NotTo(BeNil())
		})

		It("does not exclude a child process: the table is process-local", func() {
			mustTableLock(table, "exec:mem")
			Expect(lockChildExit("try", "", "exec:mem")).To(Equal(0))
		})

		It("refuses a missing table", func() {
			_, err := AcquireItemLock(nil, "", "exec:mem")
			Expect(err).To(MatchError(errLockNoTable))
		})

		It("does not let the table affect a file lock: two tables still conflict on one file", func() {
			l, err := AcquireItemLock(table, db, "exec:1")
			if err != nil {
				panic(err)
			}
			DeferCleanup(func() { _ = l.Release() })
			_, err = AcquireItemLock(NewLockTable(), db, "exec:1")
			Expect(err).To(MatchError(ErrLockHeld))
		})
	})

	Describe("lock path derivation", func() {
		It("separates identities with different devices and the same inode", func() {
			a := LockIdentity{Path: "/d/f.db", Device: 1, Index: 5}
			b := LockIdentity{Path: "/d/f.db", Device: 2, Index: 5}
			_, _, nameA := LockPath(a, "exec:1")
			_, _, nameB := LockPath(b, "exec:1")
			Expect(nameA).NotTo(Equal(nameB))
		})

		It("separates identities with the same device and different inodes", func() {
			a := LockIdentity{Path: "/d/f.db", Device: 1, Index: 5}
			b := LockIdentity{Path: "/d/f.db", Device: 1, Index: 6}
			_, _, nameA := LockPath(a, "exec:1")
			_, _, nameB := LockPath(b, "exec:1")
			Expect(nameA).NotTo(Equal(nameB))
		})
	})

	// Hard links are unsupported (ADR-011 rule 1): the lock directory comes
	// from the path, so two names for one inode do not exclude each other.
	if runtime.GOOS != "windows" {
		It("does not exclude a hard link to the database: hard links are unsupported", func() {
			mustLock(db, "exec:1")
			link := db + ".hard"
			if err := os.Link(db, link); err != nil {
				panic(err)
			}
			l, err := AcquireItemLock(nil, link, "exec:1")
			if err == nil {
				DeferCleanup(func() { _ = l.Release() })
			}
			Expect(l).NotTo(BeNil())
		})
	}
})

// inDirOf makes the directory of path the working directory until the spec ends
// and returns the file's name, a relative path that names it from there. It
// avoids filepath.Rel, which fails when the temporary directory and the package
// directory are on different Windows volumes.
func inDirOf(path string) string {
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	if err := os.Chdir(filepath.Dir(path)); err != nil {
		panic(err)
	}
	DeferCleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			panic(err)
		}
	})
	return filepath.Base(path)
}
