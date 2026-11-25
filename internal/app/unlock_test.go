package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"

	sqlite "github.com/libtnb/sqlite"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// unlockFixture is a file-backed store with one session.
type unlockFixture struct {
	ctx context.Context
	st  *store.Store
	id  string
}

// newUnlockFixture opens a file store (its temp dir is under TMPDIR) and a session.
func newUnlockFixture() *unlockFixture {
	st, err := store.Open(filepath.Join(GinkgoT().TempDir(), "unlock.db"))
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = st.Close() })
	f := &unlockFixture{ctx: context.Background(), st: st}
	f.id = mustCreateCrashSession(f.ctx, st).ID
	return f
}

// seed writes a full lease row for the session through a mapped-less ORM insert.
func (f *unlockFixture) seed(owner string, token int64, machine, host string, pid int64) {
	g, err := gorm.Open(sqlite.New(sqlite.Config{Conn: f.st.DB()}), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		panic(err)
	}
	err = g.Table("leases").Create(map[string]any{"item": "exec:" + f.id, "agent": owner, "token": token,
		"acquired_at_utc": "2026-01-02T03:04:05.000Z", "machine": machine, "host": host, "pid": pid}).Error
	if err != nil {
		panic(err)
	}
}

// bumpToken replaces the row's fencing token, as a takeover would.
func (f *unlockFixture) bumpToken() {
	g, err := gorm.Open(sqlite.New(sqlite.Config{Conn: f.st.DB()}), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		panic(err)
	}
	err = g.Table("leases").Where("item = ?", "exec:"+f.id).Update("token", gorm.Expr("token + 1")).Error
	if err != nil {
		panic(err)
	}
}

// foreignEvent inserts an event at the next seq behind any writer's back, as a
// live legacy writer appending concurrently would.
func (f *unlockFixture) foreignEvent() {
	g, err := gorm.Open(sqlite.New(sqlite.Config{Conn: f.st.DB()}), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256([]byte("{}"))
	err = g.Table("events").Create(map[string]any{"session_id": f.id, "seq": len(f.events()),
		"created_at_utc": "2026-01-02T03:04:05.000Z", "actor": "agent", "type": "tool.call",
		"payload_sha256": hex.EncodeToString(sum[:]), "payload_json": "{}"}).Error
	if err != nil {
		panic(err)
	}
}

// thisMachine is the identity a row of this machine carries.
func thisMachine() string {
	m := store.MachineIdentity()
	if m == "" {
		panic("no machine identity")
	}
	return m
}

func (f *unlockFixture) events() []store.RecordedEvent {
	evs, err := f.st.Replay(f.ctx, f.id)
	if err != nil {
		panic(err)
	}
	return evs
}

// unlockedEvents returns the session.unlocked events of the log.
func (f *unlockFixture) unlockedEvents() []store.RecordedEvent {
	var out []store.RecordedEvent
	for _, e := range f.events() {
		if e.Type == session.TypeSessionUnlocked {
			out = append(out, e)
		}
	}
	return out
}

func (f *unlockFixture) leaseRow() (store.LeaseInfo, bool) {
	info, ok, err := f.st.ReadLease(f.ctx, "exec:"+f.id)
	if err != nil {
		panic(err)
	}
	return info, ok
}

var _ = Describe("UnlockSession", func() {
	var f *unlockFixture
	BeforeEach(func() { f = newUnlockFixture() })

	It("reports no lease when the session has no row", func() {
		r, _ := UnlockSession(f.ctx, f.st, f.id, true)
		Expect(r.Outcome).To(Equal(UnlockNoLease))
	})

	It("appends no event when there is no row", func() {
		_, _ = UnlockSession(f.ctx, f.st, f.id, true)
		Expect(f.unlockedEvents()).To(BeEmpty())
	})

	It("fails for an unknown session", func() {
		_, err := UnlockSession(f.ctx, f.st, "no-such-session", true)
		Expect(err).To(MatchError(store.ErrUnknownSession))
	})

	Context("without force", func() {
		BeforeEach(func() { f.seed("run:dead", 3, thisMachine(), "box", 77) })

		It("reports the holder", func() {
			r, _ := UnlockSession(f.ctx, f.st, f.id, false)
			Expect(r.Holder).To(Equal(UnlockHolder{Owner: "run:dead", Host: "box", Pid: 77, Token: 3,
				AcquiredAt: "2026-01-02T03:04:05.000Z"}))
		})

		It("has the reported outcome", func() {
			r, _ := UnlockSession(f.ctx, f.st, f.id, false)
			Expect(r.Outcome).To(Equal(UnlockReported))
		})

		It("deletes nothing", func() {
			_, _ = UnlockSession(f.ctx, f.st, f.id, false)
			_, ok := f.leaseRow()
			Expect(ok).To(BeTrue())
		})

		It("appends no event", func() {
			_, _ = UnlockSession(f.ctx, f.st, f.id, false)
			Expect(f.unlockedEvents()).To(BeEmpty())
		})

		It("warns that force cannot tell whether a row it did not lock is running", func() {
			r, _ := UnlockSession(f.ctx, f.st, f.id, false)
			Expect(r.Message).To(ContainSubstring("cannot tell whether a row it did not lock is still running"))
		})
	})

	Context("with force on a dead holder of this machine", func() {
		BeforeEach(func() {
			f.seed("run:dead", 3, thisMachine(), "box", 77)
			if _, err := UnlockSession(f.ctx, f.st, f.id, true); err != nil {
				panic(err)
			}
		})

		It("deletes the row", func() {
			_, ok := f.leaseRow()
			Expect(ok).To(BeFalse())
		})

		It("appends exactly one session.unlocked event", func() {
			Expect(f.unlockedEvents()).To(HaveLen(1))
		})

		It("attributes the event to the engine", func() {
			Expect(f.unlockedEvents()[0].Actor).To(Equal(session.ActorEngine))
		})

		DescribeTable("records the payload", func(key string, want any) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(f.unlockedEvents()[0].PayloadJSON), &payload); err != nil {
				panic(err)
			}
			Expect(payload).To(HaveKeyWithValue(key, want))
		},
			Entry("owner", "owner", "run:dead"),
			Entry("host", "host", "box"),
			Entry("pid", "pid", float64(77)),
			Entry("token", "token", float64(3)),
			Entry("forced", "forced", true),
			Entry("machine_matches", "machine_matches", true),
			Entry("legacy_row", "legacy_row", false),
		)

		It("leaves a log that replays", func() {
			evs, err := f.st.Replay(f.ctx, f.id)
			if err != nil {
				panic(err)
			}
			Expect(evs).To(HaveLen(2))
		})

		It("leaves a log the replay export accepts", func() {
			lines, err := f.st.ExportJSONL(f.ctx, f.id)
			if err != nil {
				panic(err)
			}
			Expect(lines).NotTo(BeEmpty())
		})

		It("leaves a log session doctor accepts", func() {
			d, err := DiagnoseSession(f.ctx, f.st, f.id)
			if err != nil {
				panic(err)
			}
			Expect(d.SessionID).To(Equal(f.id))
		})

		It("leaves a log session events accepts", func() {
			_, evs, _ := InspectSession(f.ctx, f.st, f.id)
			Expect(evs[len(evs)-1].Type).To(Equal(session.TypeSessionUnlocked))
		})

		It("assigns the event the next contiguous seq", func() {
			Expect(f.unlockedEvents()[0].Seq).To(Equal(int64(1)))
		})

		It("is rejected by LoadSessionHistory with ErrInvalidHistory", func() {
			limits := HistoryLimits{MaxEvents: 100, MaxMessages: 100, MaxBytes: 100000, MaxResultBytes: 10000}
			_, err := LoadSessionHistory(f.ctx, f.st, f.id, limits)
			Expect(err).To(MatchError(ErrInvalidHistory))
		})

		It("leaves no lease row of the unlocker's own", func() {
			_, ok := f.leaseRow()
			Expect(ok).To(BeFalse())
		})

		It("leaves the lock free", func() {
			l, err := f.st.TryItemLock("exec:" + f.id)
			if err != nil {
				panic(err)
			}
			DeferCleanup(func() { _ = l.Release() })
			Expect(l.Item()).To(Equal("exec:" + f.id))
		})
	})

	Context("with a live holder on this machine", func() {
		var lock *store.LeaseLock
		BeforeEach(func() {
			f.seed("run:live", 5, thisMachine(), "box", 99)
			l, err := f.st.TryItemLock("exec:" + f.id)
			if err != nil {
				panic(err)
			}
			lock = l
			DeferCleanup(func() { _ = lock.Release() })
		})

		It("refuses even with force", func() {
			_, err := UnlockSession(f.ctx, f.st, f.id, true)
			Expect(err).To(MatchError(ErrLeaseLive))
		})

		It("names the holder in the typed error", func() {
			_, err := UnlockSession(f.ctx, f.st, f.id, true)
			var live *LeaseLiveError
			if !errors.As(err, &live) {
				panic("not a LeaseLiveError")
			}
			Expect(live.Holder.Owner).To(Equal("run:live"))
		})

		It("refuses without force too", func() {
			_, err := UnlockSession(f.ctx, f.st, f.id, false)
			Expect(err).To(MatchError(ErrLeaseLive))
		})

		It("keeps the row", func() {
			_, _ = UnlockSession(f.ctx, f.st, f.id, true)
			_, ok := f.leaseRow()
			Expect(ok).To(BeTrue())
		})

		It("writes no event", func() {
			_, _ = UnlockSession(f.ctx, f.st, f.id, true)
			Expect(f.unlockedEvents()).To(BeEmpty())
		})

		It("keeps the holder's lock", func() {
			_, _ = UnlockSession(f.ctx, f.st, f.id, true)
			_, err := f.st.TryItemLock("exec:" + f.id)
			Expect(err).To(MatchError(store.ErrLockHeld))
		})
	})

	Context("with force on another machine's row", func() {
		BeforeEach(func() {
			f.seed("run:far", 8, "other-machine-hash", "faraway", 5)
			if _, err := UnlockSession(f.ctx, f.st, f.id, true); err != nil {
				panic(err)
			}
		})

		It("deletes the row", func() {
			_, ok := f.leaseRow()
			Expect(ok).To(BeFalse())
		})

		It("records machine_matches false", func() {
			Expect(f.unlockedEvents()[0].PayloadJSON).To(ContainSubstring(`"machine_matches":false`))
		})
	})

	It("reports another machine's row without force and deletes nothing", func() {
		f.seed("run:far", 8, "other-machine-hash", "faraway", 5)
		_, _ = UnlockSession(f.ctx, f.st, f.id, false)
		_, ok := f.leaseRow()
		Expect(ok).To(BeTrue())
	})

	Context("with a legacy row of the old acquire", func() {
		BeforeEach(func() {
			if _, err := f.st.AcquireLease(f.ctx, "exec:"+f.id, "cli"); err != nil {
				panic(err)
			}
		})

		It("is reported as legacy without force", func() {
			r, _ := UnlockSession(f.ctx, f.st, f.id, false)
			Expect(r.LegacyRow).To(BeTrue())
		})

		It("is deleted by force", func() {
			if _, err := UnlockSession(f.ctx, f.st, f.id, true); err != nil {
				panic(err)
			}
			_, ok := f.leaseRow()
			Expect(ok).To(BeFalse())
		})

		It("says in the report text that the row was legacy and may still run", func() {
			r, _ := UnlockSession(f.ctx, f.st, f.id, true)
			Expect(r.Message).To(ContainSubstring("legacy row"))
		})

		It("records legacy_row true in the event", func() {
			_, _ = UnlockSession(f.ctx, f.st, f.id, true)
			Expect(f.unlockedEvents()[0].PayloadJSON).To(ContainSubstring(`"legacy_row":true`))
		})

		It("records machine_matches false in the event", func() {
			_, _ = UnlockSession(f.ctx, f.st, f.id, true)
			Expect(f.unlockedEvents()[0].PayloadJSON).To(ContainSubstring(`"machine_matches":false`))
		})
	})

	Context("with a terminal session", func() {
		BeforeEach(func() {
			f.seed("run:dead", 3, thisMachine(), "box", 77)
			if _, err := session.Close(f.ctx, f.st, f.id); err != nil {
				panic(err)
			}
		})

		It("still deletes the row on force", func() {
			_, _ = UnlockSession(f.ctx, f.st, f.id, true)
			_, ok := f.leaseRow()
			Expect(ok).To(BeFalse())
		})

		It("leaves the closed log alone", func() {
			_, _ = UnlockSession(f.ctx, f.st, f.id, true)
			Expect(f.unlockedEvents()).To(BeEmpty())
		})

		It("says no event was appended", func() {
			r, _ := UnlockSession(f.ctx, f.st, f.id, true)
			Expect(r.EventAppended).To(BeFalse())
		})
	})

	Context("while the call runs", func() {
		BeforeEach(func() {
			f.seed("run:dead", 3, thisMachine(), "box", 77)
			DeferCleanup(func(h func(string)) { unlockBeforeDeleteHook = h }, unlockBeforeDeleteHook)
		})

		It("keeps the lock held until the delete, so a run cannot acquire", func() {
			var acquireErr error
			unlockBeforeDeleteHook = func(item string) {
				lease, err := f.st.AcquireRunLease(f.ctx, item, "run:new")
				if err == nil {
					DeferCleanup(func() { _ = lease.Release(f.ctx) })
				}
				acquireErr = err
			}
			_, _ = UnlockSession(f.ctx, f.st, f.id, true)
			Expect(acquireErr).To(MatchError(store.ErrSessionBusy))
		})

		Context("when the row is replaced before the delete", func() {
			var err error
			BeforeEach(func() {
				unlockBeforeDeleteHook = func(string) { f.bumpToken() }
				_, err = UnlockSession(f.ctx, f.st, f.id, true)
			})

			It("fails with ErrLeaseChanged", func() {
				Expect(errors.Is(err, ErrLeaseChanged)).To(BeTrue())
			})

			It("keeps the new row", func() {
				_, ok := f.leaseRow()
				Expect(ok).To(BeTrue())
			})

			It("appends no event for a delete that did not happen", func() {
				Expect(f.unlockedEvents()).To(BeEmpty())
			})
		})

		It("gives a session that became terminal during the call no event", func() {
			unlockBeforeDeleteHook = func(string) {
				if _, err := session.Close(f.ctx, f.st, f.id); err != nil {
					panic(err)
				}
			}
			_, _ = UnlockSession(f.ctx, f.st, f.id, true)
			Expect(f.unlockedEvents()).To(BeEmpty())
		})
	})

	Context("when the audit event cannot be written", func() {
		var r UnlockReport
		var err error
		BeforeEach(func() {
			f.seed("run:dead", 3, thisMachine(), "box", 77)
			w, werr := f.st.NewEventWriter(f.ctx, f.id)
			if werr != nil {
				panic(werr)
			}
			DeferCleanup(func() { _ = w.Close() })
			r, err = UnlockSession(f.ctx, f.st, f.id, true)
		})

		It("still reports the row as deleted", func() {
			Expect(r.Outcome).To(Equal(UnlockDeleted))
		})

		It("says no event was appended", func() {
			Expect(r.EventAppended).To(BeFalse())
		})

		It("returns the writer error wrapped", func() {
			Expect(err).To(MatchError(store.ErrWriterExists))
		})
	})

	Context("when a concurrent writer takes the seq", func() {
		var attempts []int
		BeforeEach(func() {
			f.seed("run:dead", 3, thisMachine(), "box", 77)
			attempts = nil
			DeferCleanup(func(h func(string, int)) { unlockBeforeAppendHook = h }, unlockBeforeAppendHook)
		})

		It("retries once with a fresh writer and then appends", func() {
			unlockBeforeAppendHook = func(_ string, attempt int) {
				attempts = append(attempts, attempt)
				if attempt == 0 {
					f.foreignEvent()
				}
			}
			r, _ := UnlockSession(f.ctx, f.st, f.id, true)
			Expect(r.EventAppended).To(BeTrue())
		})

		It("records the event once after a collision", func() {
			unlockBeforeAppendHook = func(_ string, attempt int) {
				if attempt == 0 {
					f.foreignEvent()
				}
			}
			_, _ = UnlockSession(f.ctx, f.st, f.id, true)
			Expect(f.unlockedEvents()).To(HaveLen(1))
		})

		Context("on every attempt", func() {
			var r UnlockReport
			var err error
			BeforeEach(func() {
				unlockBeforeAppendHook = func(string, int) {
					attempts = append(attempts, len(attempts))
					f.foreignEvent()
				}
				r, err = UnlockSession(f.ctx, f.st, f.id, true)
			})

			It("gives up after three attempts", func() {
				Expect(attempts).To(HaveLen(3))
			})

			It("returns the collision wrapped", func() {
				Expect(err).To(MatchError(store.ErrSeqCollision))
			})

			It("still reports the row as deleted", func() {
				Expect(r.Outcome).To(Equal(UnlockDeleted))
			})

			It("says no event was appended", func() {
				Expect(r.EventAppended).To(BeFalse())
			})
		})
	})
})
