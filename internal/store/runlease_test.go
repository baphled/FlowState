package store

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

const (
	runLeaseChildEnv = "FLOWSTATE_RUNLEASE_CHILD_DB"
	runLeaseItem     = "exec:sess-1"
)

// init turns this test binary into a holder when re-invoked by the specs: it
// takes the run lease on the database named by the environment, prints
// "locked" and waits for stdin to close.
func init() {
	db := os.Getenv(runLeaseChildEnv)
	if db == "" {
		return
	}
	s, err := Open(db)
	if err != nil {
		os.Exit(1)
	}
	if _, err := s.AcquireRunLease(context.Background(), runLeaseItem, "run:child"); err != nil {
		os.Exit(2)
	}
	_, _ = os.Stdout.WriteString("locked\n")
	_, _ = os.Stdin.Read(make([]byte, 1))
	os.Exit(0)
}

// busyCodeError mimics the driver error: it carries a SQLite result code.
type busyCodeError struct{ code int }

func (e busyCodeError) Error() string { return "injected sqlite error" }

func (e busyCodeError) Code() int { return e.code }

func openRunLeaseStore(path string) *Store {
	s, err := Open(path)
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = s.Close() })
	return s
}

func mustRunLease(s *Store, item, owner string) *RunLease {
	h, err := s.AcquireRunLease(context.Background(), item, owner)
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = h.Release(context.Background()) })
	return h
}

func readLeaseRow(s *Store, item string) leaseRow {
	var rows []leaseRow
	if err := s.gdb.Where(map[string]any{"item": item}).Find(&rows).Error; err != nil || len(rows) != 1 {
		panic("lease row not readable")
	}
	return rows[0]
}

func countLeaseRows(s *Store, item string) int64 {
	var n int64
	if err := s.gdb.Model(&leaseRow{}).Where(map[string]any{"item": item}).Count(&n).Error; err != nil {
		panic(err)
	}
	return n
}

func countHistory(s *Store, item string) int64 {
	var n int64
	if err := s.gdb.Model(&leaseHistoryRow{}).Where(map[string]any{"item": item}).Count(&n).Error; err != nil {
		panic(err)
	}
	return n
}

func hasHistory(s *Store, item string, token int64) int64 {
	var n int64
	err := s.gdb.Model(&leaseHistoryRow{}).Where(map[string]any{"item": item, "token": token}).Count(&n).Error
	if err != nil {
		panic(err)
	}
	return n
}

func setLeaseRow(s *Store, item string, values map[string]any) {
	if err := s.gdb.Model(&leaseRow{}).Where(map[string]any{"item": item}).Updates(values).Error; err != nil {
		panic(err)
	}
}

// crash simulates a dead holder: the lock is dropped and the row stays.
func crash(h *RunLease) {
	if err := h.lock.Release(); err != nil {
		panic(err)
	}
}

func acquireErr(s *Store, item, owner string) error {
	h, err := s.AcquireRunLease(context.Background(), item, owner)
	if err == nil {
		DeferCleanup(func() { _ = h.Release(context.Background()) })
	}
	return err
}

func busyOf(err error) *SessionBusyError {
	var b *SessionBusyError
	if !errors.As(err, &b) {
		panic("not a SessionBusyError")
	}
	return b
}

var _ = Describe("AcquireRunLease", func() {
	var (
		ctx  context.Context
		path string
		a, b *Store
	)

	BeforeEach(func() {
		ctx = context.Background()
		path = filepath.Join(GinkgoT().TempDir(), "ws.db")
		a = openRunLeaseStore(path)
		b = openRunLeaseStore(path)
	})

	Describe("a free item", func() {
		It("grants token 1", func() {
			h, err := a.AcquireRunLease(ctx, runLeaseItem, "run:a")
			if err != nil {
				panic(err)
			}
			DeferCleanup(func() { _ = h.Release(ctx) })
			Expect(h.Token()).To(Equal(int64(1)))
		})

		It("continues the token history after a release", func() {
			h := mustRunLease(a, runLeaseItem, "run:a")
			if err := h.Release(ctx); err != nil {
				panic(err)
			}
			h2 := mustRunLease(b, runLeaseItem, "run:b")
			Expect(h2.Token()).To(Equal(int64(2)))
		})

		It("records the machine identity of this machine in the row", func() {
			mustRunLease(a, runLeaseItem, "run:a")
			Expect(readLeaseRow(a, runLeaseItem).Machine).To(Equal(MachineIdentity()))
		})

		It("records this process id in the row", func() {
			mustRunLease(a, runLeaseItem, "run:a")
			Expect(readLeaseRow(a, runLeaseItem).Pid).To(Equal(int64(os.Getpid())))
		})

		It("records the host name in the row", func() {
			want, _ := os.Hostname()
			mustRunLease(a, runLeaseItem, "run:a")
			Expect(readLeaseRow(a, runLeaseItem).Host).To(Equal(want))
		})

		It("exposes the machine on the handle", func() {
			Expect(mustRunLease(a, runLeaseItem, "run:a").Machine()).To(Equal(MachineIdentity()))
		})

		It("exposes the pid on the handle", func() {
			Expect(mustRunLease(a, runLeaseItem, "run:a").Pid()).To(Equal(int64(os.Getpid())))
		})

		It("exposes the item on the handle", func() {
			Expect(mustRunLease(a, runLeaseItem, "run:a").Item()).To(Equal(runLeaseItem))
		})

		It("exposes the owner on the handle", func() {
			Expect(mustRunLease(a, runLeaseItem, "run:a").Owner()).To(Equal("run:a"))
		})

		It("refuses an empty owner", func() {
			Expect(acquireErr(a, runLeaseItem, "")).To(MatchError(ContainSubstring("must not be empty")))
		})
	})

	Describe("a row of this machine with a held lock", func() {
		var tok int64

		BeforeEach(func() {
			tok = mustRunLease(a, runLeaseItem, "run:a").Token()
		})

		It("is refused with ErrSessionBusy", func() {
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(MatchError(ErrSessionBusy))
		})

		It("leaves the row owner unchanged", func() {
			_ = acquireErr(b, runLeaseItem, "run:b")
			Expect(readLeaseRow(a, runLeaseItem).Agent).To(Equal("run:a"))
		})

		It("leaves the token unchanged", func() {
			_ = acquireErr(b, runLeaseItem, "run:b")
			Expect(readLeaseRow(a, runLeaseItem).Token).To(Equal(tok))
		})

		It("leaves the lease history unchanged", func() {
			_ = acquireErr(b, runLeaseItem, "run:b")
			Expect(countHistory(a, runLeaseItem)).To(Equal(int64(1)))
		})

		It("names the holder owner in the message", func() {
			Expect(acquireErr(b, runLeaseItem, "run:b").Error()).To(ContainSubstring("run:a"))
		})

		It("names the holder host in the message", func() {
			host, _ := os.Hostname()
			Expect(acquireErr(b, runLeaseItem, "run:b").Error()).To(ContainSubstring("host " + host))
		})

		It("names the holder pid in the message", func() {
			Expect(acquireErr(b, runLeaseItem, "run:b").Error()).To(ContainSubstring("pid "))
		})

		It("names the manual command in the message", func() {
			Expect(acquireErr(b, runLeaseItem, "run:b").Error()).To(ContainSubstring("flowstate session unlock sess-1"))
		})

		It("carries the holder owner for errors.As", func() {
			Expect(busyOf(acquireErr(b, runLeaseItem, "run:b")).Owner).To(Equal("run:a"))
		})

		It("carries the holder pid for errors.As", func() {
			Expect(busyOf(acquireErr(b, runLeaseItem, "run:b")).Pid).To(Equal(int64(os.Getpid())))
		})

		It("is not marked unjudgeable", func() {
			Expect(busyOf(acquireErr(b, runLeaseItem, "run:b")).Unjudgeable).To(BeFalse())
		})
	})

	Describe("a held lock without a row", func() {
		It("is refused saying another process holds it", func() {
			l, err := AcquireItemLock(nil, path, runLeaseItem)
			if err != nil {
				panic(err)
			}
			DeferCleanup(func() { _ = l.Release() })
			Expect(acquireErr(a, runLeaseItem, "run:a").Error()).To(ContainSubstring("held by another process"))
		})
	})

	Describe("a row of this machine with a free lock", func() {
		var old *RunLease

		BeforeEach(func() {
			old = mustRunLease(a, runLeaseItem, "run:a")
			setLeaseRow(a, runLeaseItem, map[string]any{"pid": int64(424242), "host": "elsewhere"})
			crash(old)
		})

		It("is taken over with the token plus one", func() {
			Expect(mustRunLease(b, runLeaseItem, "run:b").Token()).To(Equal(old.Token() + 1))
		})

		It("records the new token in the lease history", func() {
			mustRunLease(b, runLeaseItem, "run:b")
			Expect(hasHistory(a, runLeaseItem, old.Token()+1)).To(Equal(int64(1)))
		})

		It("fences the old handle", func() {
			mustRunLease(b, runLeaseItem, "run:b")
			Expect(old.Fence(ctx)).To(MatchError(ErrLeaseFenced))
		})

		It("records the new owner", func() {
			mustRunLease(b, runLeaseItem, "run:b")
			Expect(readLeaseRow(a, runLeaseItem).Agent).To(Equal("run:b"))
		})

		It("records the new pid", func() {
			mustRunLease(b, runLeaseItem, "run:b")
			Expect(readLeaseRow(a, runLeaseItem).Pid).To(Equal(int64(os.Getpid())))
		})

		It("records the new host", func() {
			want, _ := os.Hostname()
			mustRunLease(b, runLeaseItem, "run:b")
			Expect(readLeaseRow(a, runLeaseItem).Host).To(Equal(want))
		})

		It("lets the new handle fence successfully", func() {
			Expect(mustRunLease(b, runLeaseItem, "run:b").Fence(ctx)).To(Succeed())
		})

		It("refuses when the compare-and-set loses on the token", func() {
			runLeaseBeforeSwapHook = func(tx *gorm.DB, item string) {
				err := tx.Model(&leaseRow{}).Where(map[string]any{"item": item}).Update("token", old.Token()+5).Error
				if err != nil {
					panic(err)
				}
			}
			DeferCleanup(func() { runLeaseBeforeSwapHook = func(*gorm.DB, string) {} })
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(MatchError(ErrSessionBusy))
		})

		It("leaves the row alone when the compare-and-set loses", func() {
			runLeaseBeforeSwapHook = func(tx *gorm.DB, item string) {
				err := tx.Model(&leaseRow{}).Where(map[string]any{"item": item}).Update("token", old.Token()+5).Error
				if err != nil {
					panic(err)
				}
			}
			DeferCleanup(func() { runLeaseBeforeSwapHook = func(*gorm.DB, string) {} })
			_ = acquireErr(b, runLeaseItem, "run:b")
			Expect(readLeaseRow(a, runLeaseItem).Agent).To(Equal("run:a"))
		})

		It("frees the lock when the compare-and-set loses", func() {
			runLeaseBeforeSwapHook = func(tx *gorm.DB, item string) {
				err := tx.Model(&leaseRow{}).Where(map[string]any{"item": item}).Update("token", old.Token()+5).Error
				if err != nil {
					panic(err)
				}
			}
			_ = acquireErr(b, runLeaseItem, "run:b")
			runLeaseBeforeSwapHook = func(*gorm.DB, string) {}
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(Succeed())
		})
	})

	Describe("an owner change in the compare-and-set window", func() {
		var old *RunLease

		BeforeEach(func() {
			old = mustRunLease(a, runLeaseItem, "run:a")
			crash(old)
			runLeaseBeforeSwapHook = func(tx *gorm.DB, item string) {
				err := tx.Model(&leaseRow{}).Where(map[string]any{"item": item}).Update("agent", "run:x").Error
				if err != nil {
					panic(err)
				}
			}
			DeferCleanup(func() { runLeaseBeforeSwapHook = func(*gorm.DB, string) {} })
		})

		It("refuses when only the owner changed", func() {
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(MatchError(ErrSessionBusy))
		})

		It("leaves the original owner in place", func() {
			_ = acquireErr(b, runLeaseItem, "run:b")
			Expect(readLeaseRow(a, runLeaseItem).Agent).To(Equal("run:a"))
		})

		It("leaves the token of the row alone", func() {
			_ = acquireErr(b, runLeaseItem, "run:b")
			Expect(readLeaseRow(a, runLeaseItem).Token).To(Equal(old.Token()))
		})
	})

	Describe("a failure that is not a refusal", func() {
		var injected error

		BeforeEach(func() {
			injected = errors.New("injected failure")
			DeferCleanup(func(orig func(int) error, backoff time.Duration) {
				runLeaseTxHook, runLeaseBackoff = orig, backoff
			}, runLeaseTxHook, runLeaseBackoff)
			runLeaseBackoff = time.Microsecond
		})

		It("returns context.Canceled for a cancelled context", func() {
			cctx, cancel := context.WithCancel(ctx)
			cancel()
			_, err := a.AcquireRunLease(cctx, runLeaseItem, "run:a")
			Expect(err).To(MatchError(context.Canceled))
		})

		It("frees the lock after a cancelled context", func() {
			cctx, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := a.AcquireRunLease(cctx, runLeaseItem, "run:a"); err == nil {
				panic("cancelled acquire succeeded")
			}
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(Succeed())
		})

		It("returns the injected error unchanged in kind", func() {
			runLeaseTxHook = func(int) error { return injected }
			Expect(acquireErr(a, runLeaseItem, "run:a")).To(MatchError(injected))
		})

		It("does not call an injected error a busy session", func() {
			runLeaseTxHook = func(int) error { return injected }
			Expect(errors.Is(acquireErr(a, runLeaseItem, "run:a"), ErrSessionBusy)).To(BeFalse())
		})

		It("frees the lock after an injected error", func() {
			runLeaseTxHook = func(int) error { return injected }
			if acquireErr(a, runLeaseItem, "run:a") == nil {
				panic("injected error was not returned")
			}
			runLeaseTxHook = func(int) error { return nil }
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(Succeed())
		})

		It("frees the lock after a recovered panic", func() {
			runLeaseTxHook = func(int) error { panic("boom") }
			func() {
				defer func() { _ = recover() }()
				_, _ = a.AcquireRunLease(ctx, runLeaseItem, "run:a")
			}()
			runLeaseTxHook = func(int) error { return nil }
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(Succeed())
		})

		It("retries a busy transaction and then succeeds", func() {
			runLeaseTxHook = func(attempt int) error {
				if attempt < 3 {
					return busyCodeError{code: 5}
				}
				return nil
			}
			Expect(acquireErr(a, runLeaseItem, "run:a")).To(Succeed())
		})

		It("retries a locked transaction and then succeeds", func() {
			runLeaseTxHook = func(attempt int) error {
				if attempt < 2 {
					return busyCodeError{code: 6 | 1<<8}
				}
				return nil
			}
			Expect(acquireErr(a, runLeaseItem, "run:a")).To(Succeed())
		})

		It("gives up with the typed error after the bounded attempts", func() {
			runLeaseTxHook = func(int) error { return busyCodeError{code: 5} }
			Expect(acquireErr(a, runLeaseItem, "run:a")).To(MatchError(busyCodeError{code: 5}))
		})

		It("does not call an exhausted retry a busy session", func() {
			runLeaseTxHook = func(int) error { return busyCodeError{code: 5} }
			Expect(errors.Is(acquireErr(a, runLeaseItem, "run:a"), ErrSessionBusy)).To(BeFalse())
		})

		It("makes exactly the bounded number of attempts", func() {
			calls := 0
			runLeaseTxHook = func(int) error { calls++; return busyCodeError{code: 5} }
			_ = acquireErr(a, runLeaseItem, "run:a")
			Expect(calls).To(Equal(runLeaseTxAttempts))
		})

		It("does not retry an error that is not busy", func() {
			calls := 0
			runLeaseTxHook = func(int) error { calls++; return busyCodeError{code: 19} }
			_ = acquireErr(a, runLeaseItem, "run:a")
			Expect(calls).To(Equal(1))
		})

		It("stops retrying when the context is cancelled", func() {
			cctx, cancel := context.WithCancel(ctx)
			runLeaseBackoff = time.Minute
			runLeaseTxHook = func(int) error { cancel(); return busyCodeError{code: 5} }
			_, err := a.AcquireRunLease(cctx, runLeaseItem, "run:a")
			Expect(err).To(MatchError(context.Canceled))
		})

		It("frees the lock after the retries are exhausted", func() {
			runLeaseTxHook = func(int) error { return busyCodeError{code: 5} }
			_ = acquireErr(a, runLeaseItem, "run:a")
			runLeaseTxHook = func(int) error { return nil }
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(Succeed())
		})
	})

	Describe("the refusal text of a legacy row", func() {
		var msg string

		BeforeEach(func() {
			if _, err := a.AcquireLease(ctx, runLeaseItem, "cli"); err != nil {
				panic(err)
			}
			msg = acquireErr(b, runLeaseItem, "run:b").Error()
		})

		It("says the lease cannot be judged and names the owner", func() {
			Expect(msg).To(ContainSubstring("held by a lease that cannot be judged (another machine or an older writer): owner cli, "))
		})

		It("prints unknown for the empty host", func() {
			Expect(msg).To(ContainSubstring("host unknown, "))
		})

		It("prints unknown for the zero pid", func() {
			Expect(msg).To(ContainSubstring("pid unknown, since "))
		})

		It("names the unlock command with the session id", func() {
			Expect(msg).To(ContainSubstring("run `flowstate session unlock sess-1 --force`"))
		})
	})

	Describe("a row that cannot be judged", func() {
		It("refuses a row of another machine", func() {
			old := mustRunLease(a, runLeaseItem, "run:a")
			setLeaseRow(a, runLeaseItem, map[string]any{"machine": "other"})
			crash(old)
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(MatchError(ErrSessionBusy))
		})

		It("never takes over a row of another machine", func() {
			old := mustRunLease(a, runLeaseItem, "run:a")
			setLeaseRow(a, runLeaseItem, map[string]any{"machine": "other"})
			crash(old)
			_ = acquireErr(b, runLeaseItem, "run:b")
			Expect(readLeaseRow(a, runLeaseItem).Agent).To(Equal("run:a"))
		})

		It("marks a row of another machine unjudgeable", func() {
			old := mustRunLease(a, runLeaseItem, "run:a")
			setLeaseRow(a, runLeaseItem, map[string]any{"machine": "other"})
			crash(old)
			Expect(busyOf(acquireErr(b, runLeaseItem, "run:b")).Unjudgeable).To(BeTrue())
		})

		It("names the unlock command for a row of another machine", func() {
			old := mustRunLease(a, runLeaseItem, "run:a")
			setLeaseRow(a, runLeaseItem, map[string]any{"machine": "other"})
			crash(old)
			Expect(acquireErr(b, runLeaseItem, "run:b").Error()).To(ContainSubstring("flowstate session unlock sess-1"))
		})

		It("refuses a legacy row with an empty machine", func() {
			if _, err := a.AcquireLease(ctx, runLeaseItem, "cli"); err != nil {
				panic(err)
			}
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(MatchError(ErrSessionBusy))
		})

		It("never takes over a legacy row", func() {
			if _, err := a.AcquireLease(ctx, runLeaseItem, "cli"); err != nil {
				panic(err)
			}
			_ = acquireErr(b, runLeaseItem, "run:b")
			Expect(readLeaseRow(a, runLeaseItem).Agent).To(Equal("cli"))
		})

		It("never bumps the token of a legacy row", func() {
			if _, err := a.AcquireLease(ctx, runLeaseItem, "cli"); err != nil {
				panic(err)
			}
			_ = acquireErr(b, runLeaseItem, "run:b")
			Expect(readLeaseRow(a, runLeaseItem).Token).To(Equal(int64(1)))
		})

		It("refuses a legacy row of the same owner name", func() {
			if _, err := a.AcquireLease(ctx, runLeaseItem, "run:b"); err != nil {
				panic(err)
			}
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(MatchError(ErrSessionBusy))
		})

		It("refuses a legacy row written between the lock and the transaction", func() {
			runLeaseAfterLockHook = func(item string) {
				if _, err := a.AcquireLease(ctx, item, "cli"); err != nil {
					panic(err)
				}
			}
			DeferCleanup(func() { runLeaseAfterLockHook = func(string) {} })
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(MatchError(ErrSessionBusy))
		})

		It("leaves the racing legacy row in place", func() {
			runLeaseAfterLockHook = func(item string) {
				if _, err := a.AcquireLease(ctx, item, "cli"); err != nil {
					panic(err)
				}
			}
			_ = acquireErr(b, runLeaseItem, "run:b")
			runLeaseAfterLockHook = func(string) {}
			Expect(readLeaseRow(a, runLeaseItem).Agent).To(Equal("cli"))
		})

		It("releases the lock after refusing a legacy row, so the next acquire succeeds", func() {
			tok, err := a.AcquireLease(ctx, runLeaseItem, "cli")
			if err != nil {
				panic(err)
			}
			if acquireErr(b, runLeaseItem, "run:b") == nil {
				panic("legacy row was not refused")
			}
			if err := a.ReleaseLease(ctx, runLeaseItem, "cli", tok); err != nil {
				panic(err)
			}
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(Succeed())
		})

		It("releases the lock after refusing another machine, so the next acquire succeeds", func() {
			old := mustRunLease(a, runLeaseItem, "run:a")
			setLeaseRow(a, runLeaseItem, map[string]any{"machine": "other"})
			crash(old)
			if acquireErr(b, runLeaseItem, "run:b") == nil {
				panic("foreign row was not refused")
			}
			setLeaseRow(a, runLeaseItem, map[string]any{"machine": MachineIdentity()})
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(Succeed())
		})

		It("releases the lock after the racing legacy row is refused", func() {
			var tok int64
			runLeaseAfterLockHook = func(item string) {
				var err error
				if tok, err = a.AcquireLease(ctx, item, "cli"); err != nil {
					panic(err)
				}
			}
			if acquireErr(b, runLeaseItem, "run:b") == nil {
				panic("racing row was not refused")
			}
			runLeaseAfterLockHook = func(string) {}
			if err := a.ReleaseLease(ctx, runLeaseItem, "cli", tok); err != nil {
				panic(err)
			}
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(Succeed())
		})
	})

	Describe("the handle", func() {
		It("fences while it holds the lease", func() {
			Expect(mustRunLease(a, runLeaseItem, "run:a").Fence(ctx)).To(Succeed())
		})

		It("deletes the row on release", func() {
			h := mustRunLease(a, runLeaseItem, "run:a")
			if err := h.Release(ctx); err != nil {
				panic(err)
			}
			Expect(countLeaseRows(a, runLeaseItem)).To(Equal(int64(0)))
		})

		It("keeps a row replaced by another owner on release", func() {
			h := mustRunLease(a, runLeaseItem, "run:a")
			setLeaseRow(a, runLeaseItem, map[string]any{"agent": "run:other"})
			if err := h.Release(ctx); err != nil {
				panic(err)
			}
			Expect(countLeaseRows(a, runLeaseItem)).To(Equal(int64(1)))
		})

		It("keeps a row with another token on release", func() {
			h := mustRunLease(a, runLeaseItem, "run:a")
			setLeaseRow(a, runLeaseItem, map[string]any{"token": h.Token() + 3})
			if err := h.Release(ctx); err != nil {
				panic(err)
			}
			Expect(countLeaseRows(a, runLeaseItem)).To(Equal(int64(1)))
		})

		It("releases twice without error", func() {
			h := mustRunLease(a, runLeaseItem, "run:a")
			if err := h.Release(ctx); err != nil {
				panic(err)
			}
			Expect(h.Release(ctx)).To(Succeed())
		})

		It("frees the lock on release even when the row was replaced", func() {
			h := mustRunLease(a, runLeaseItem, "run:a")
			setLeaseRow(a, runLeaseItem, map[string]any{"machine": MachineIdentity(), "agent": "run:other"})
			if err := h.Release(ctx); err != nil {
				panic(err)
			}
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(Succeed())
		})

		It("frees the lock for the next acquire", func() {
			h := mustRunLease(a, runLeaseItem, "run:a")
			if err := h.Release(ctx); err != nil {
				panic(err)
			}
			Expect(acquireErr(b, runLeaseItem, "run:b")).To(Succeed())
		})

		It("does not free a taken-over lock when the old handle releases late", func() {
			old := mustRunLease(a, runLeaseItem, "run:a")
			crash(old)
			mustRunLease(b, runLeaseItem, "run:b")
			if err := old.Release(ctx); err != nil {
				panic(err)
			}
			Expect(countLeaseRows(a, runLeaseItem)).To(Equal(int64(1)))
		})
	})

	Describe("two stores racing for a free item", func() {
		It("lets exactly one win", func() {
			stores := []*Store{a, b}
			var mu sync.Mutex
			wins := 0
			var wg sync.WaitGroup
			for i := range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					h, err := stores[i%2].AcquireRunLease(ctx, runLeaseItem, "run:"+string(rune('a'+i)))
					if err != nil {
						return
					}
					DeferCleanup(func() { _ = h.Release(ctx) })
					mu.Lock()
					wins++
					mu.Unlock()
				}()
			}
			wg.Wait()
			Expect(wins).To(Equal(1))
		})
	})

	Describe("a store without a path", func() {
		var m1, m2 *Store

		BeforeEach(func() {
			var err error
			if m1, err = OpenMemory(); err != nil {
				panic(err)
			}
			if m2, err = OpenMemory(); err != nil {
				panic(err)
			}
			DeferCleanup(func() { _ = m1.Close(); _ = m2.Close() })
		})

		It("grants a lease through its own table", func() {
			Expect(mustRunLease(m1, runLeaseItem, "run:a").Token()).To(Equal(int64(1)))
		})

		It("refuses a second acquire in the same store", func() {
			mustRunLease(m1, runLeaseItem, "run:a")
			Expect(acquireErr(m1, runLeaseItem, "run:b")).To(MatchError(ErrSessionBusy))
		})

		It("does not conflict with another in-memory store", func() {
			mustRunLease(m1, runLeaseItem, "run:a")
			Expect(acquireErr(m2, runLeaseItem, "run:b")).To(Succeed())
		})

		It("frees the table entry on release", func() {
			h := mustRunLease(m1, runLeaseItem, "run:a")
			if err := h.Release(ctx); err != nil {
				panic(err)
			}
			Expect(acquireErr(m1, runLeaseItem, "run:b")).To(Succeed())
		})
	})

	if runtime.GOOS != "windows" {
		Describe("a holder in another process", func() {
			var (
				child *exec.Cmd
				stdin interface{ Close() error }
			)

			BeforeEach(func() {
				child = exec.CommandContext(context.Background(), os.Args[0])
				child.Env = append(os.Environ(), runLeaseChildEnv+"="+path)
				in, err := child.StdinPipe()
				if err != nil {
					panic(err)
				}
				stdin = in
				out, err := child.StdoutPipe()
				if err != nil {
					panic(err)
				}
				if err := child.Start(); err != nil {
					panic(err)
				}
				buf := make([]byte, 7)
				if _, err := out.Read(buf); err != nil || string(buf) != "locked\n" {
					panic("child did not take the lease")
				}
				DeferCleanup(func() { _ = stdin.Close(); _ = child.Process.Kill(); _ = child.Wait() })
			})

			It("is refused while it lives", func() {
				Expect(acquireErr(a, runLeaseItem, "run:a")).To(MatchError(ErrSessionBusy))
			})

			It("is named by its pid in the refusal", func() {
				Expect(busyOf(acquireErr(a, runLeaseItem, "run:a")).Pid).To(Equal(int64(child.Process.Pid)))
			})

			It("is taken over with the token plus one after it is killed", func() {
				if err := child.Process.Kill(); err != nil {
					panic(err)
				}
				_ = child.Wait()
				Expect(mustRunLease(a, runLeaseItem, "run:a").Token()).To(Equal(int64(2)))
			})
		})
	}
})
