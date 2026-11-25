package app

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// toolRunFixture is a file database (under TMPDIR, via GinkgoT().TempDir) with one session, the store a
// tool run opens and a second handle on the same file for a live holder.
type toolRunFixture struct {
	ctx    context.Context
	db     string
	st     *store.Store
	second *store.Store
	id     string
}

// newToolRunFixture opens both handles on one file and creates the session. Panics are setup failures.
func newToolRunFixture() *toolRunFixture {
	db := filepath.Join(GinkgoT().TempDir(), "toolrun.db")
	st, err := store.Open(db)
	if err != nil {
		panic(err)
	}
	second, err := store.Open(db)
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = second.Close(); _ = st.Close() })
	f := &toolRunFixture{ctx: context.Background(), db: db, st: st, second: second}
	f.id = mustCreateCrashSession(f.ctx, st).ID
	return f
}

// request is an allowed echo call in the session; extra changes the request.
func (f *toolRunFixture) request() ToolRequest {
	return ToolRequest{SessionID: f.id, Tool: "echo", ToolArg: `{"a":1}`, Allow: []string{"echo"},
		Workdir: filepath.Join(filepath.Dir(f.db), "work")}
}

func (f *toolRunFixture) run(req ToolRequest) error {
	_, err := RunSessionTool(f.ctx, f.st, f.db, req)
	return err
}

func (f *toolRunFixture) events() []store.RecordedEvent {
	evs, err := f.st.Replay(f.ctx, f.id)
	if err != nil {
		panic(err)
	}
	return evs
}

// canTake reports whether the run lease can be taken from the second handle now, and releases it again.
func (f *toolRunFixture) canTake() bool {
	h, err := f.second.AcquireRunLease(f.ctx, "exec:"+f.id, "run:probe")
	if err != nil {
		return false
	}
	if rerr := h.Release(f.ctx); rerr != nil {
		panic(rerr)
	}
	return true
}

var _ = Describe("RunSessionTool lease (ADR-011)", func() {
	var f *toolRunFixture
	BeforeEach(func() { f = newToolRunFixture() })

	Context("with the lease held by a live run on a second handle", func() {
		var before []store.RecordedEvent
		var held *store.RunLease
		var err error
		BeforeEach(func() {
			var aerr error
			if held, aerr = f.second.AcquireRunLease(f.ctx, "exec:"+f.id, "run:live"); aerr != nil {
				panic(aerr)
			}
			DeferCleanup(func() { _ = held.Release(f.ctx) })
			before = f.events()
			err = f.run(f.request())
		})

		It("is refused as busy", func() { Expect(err).To(MatchError(store.ErrSessionBusy)) })
		It("names the holder", func() { Expect(err).To(MatchError(ContainSubstring("run:live"))) })
		It("leaves the log byte-identical", func() { Expect(f.events()).To(Equal(before)) })
		It("leaves the holder's lease valid", func() { Expect(held.Fence(f.ctx)).To(Succeed()) })
	})

	Context("with a legacy row, which records no machine", func() {
		var before []store.RecordedEvent
		var err error
		BeforeEach(func() {
			must(f.st.AcquireLease(f.ctx, "exec:"+f.id, "cli"))
			before = f.events()
			err = f.run(f.request())
		})

		It("is refused as unjudgeable", func() { Expect(err).To(MatchError(ContainSubstring("cannot be judged"))) })
		It("names the unlock command", func() {
			Expect(err).To(MatchError(ContainSubstring("flowstate session unlock " + f.id)))
		})
		It("leaves the log byte-identical", func() { Expect(f.events()).To(Equal(before)) })
		It("leaves the row", func() { Expect(f.st.LeaseToken(f.ctx, "exec:"+f.id)).To(Equal(int64(1))) })
	})

	Context("with a crashed judged row of this machine", func() {
		var types []string
		BeforeEach(func() {
			seedLeaseRow(f.st, f.id, "run:dead", 6, store.MachineIdentity(), "deadhost", 1)
			check(f.run(f.request()))
			types = nil
			for _, e := range f.events() {
				types = append(types, e.Type)
			}
		})

		It("takes the row over and records the tool result", func() { Expect(types).To(ContainElement("tool.result")) })
		It("releases the row afterwards", func() { Expect(f.st.LeaseToken(f.ctx, "exec:"+f.id)).To(BeZero()) })
	})

	Context("after a successful run", func() {
		BeforeEach(func() { check(f.run(f.request())) })

		It("records the tool call", func() {
			var types []string
			for _, e := range f.events() {
				types = append(types, e.Type)
			}
			Expect(types).To(ContainElement("tool.call"))
		})
		It("has released the lock for the next holder", func() { Expect(f.canTake()).To(BeTrue()) })
		It("has removed the row", func() { Expect(f.st.LeaseToken(f.ctx, "exec:"+f.id)).To(BeZero()) })
	})

	Context("after a run that fails", func() {
		var err error
		BeforeEach(func() {
			req := f.request()
			req.Allow = nil // deny by default
			err = f.run(req)
		})

		It("fails as a usage error", func() { Expect(err).To(MatchError(session.ErrUsage)) })
		It("has released the lock for the next holder", func() { Expect(f.canTake()).To(BeTrue()) })
		It("has removed the row", func() { Expect(f.st.LeaseToken(f.ctx, "exec:"+f.id)).To(BeZero()) })
	})

	Context("a terminal session", func() {
		var before []store.RecordedEvent
		var err error
		BeforeEach(func() {
			if uerr := f.st.UpdateSessionStatus(f.ctx, f.id, "completed"); uerr != nil {
				panic(uerr)
			}
			before = f.events()
			err = f.run(f.request())
		})

		It("is refused as terminal, as before the lease", func() { Expect(err).To(MatchError(session.ErrTerminal)) })
		It("writes nothing", func() { Expect(f.events()).To(Equal(before)) })
		It("takes no lease", func() { Expect(f.canTake()).To(BeTrue()) })
	})

	Context("while the tool run appends", func() {
		var probeErr error
		BeforeEach(func() {
			DeferCleanup(func(h func(string)) { toolRunBeforeAppendHook = h }, toolRunBeforeAppendHook)
			toolRunBeforeAppendHook = func(string) {
				h, err := f.second.AcquireRunLease(f.ctx, "exec:"+f.id, "run:second")
				if err == nil {
					_ = h.Release(f.ctx)
				}
				probeErr = err
			}
			check(f.run(f.request()))
		})

		It("refuses a second run, so the lease is held through the appends", func() {
			Expect(probeErr).To(MatchError(store.ErrSessionBusy))
		})
	})

	Context("when the session becomes terminal after the lease is taken", func() {
		var before []store.RecordedEvent
		var err error
		BeforeEach(func() {
			DeferCleanup(func(h func(string)) { toolRunAfterAcquireHook = h }, toolRunAfterAcquireHook)
			toolRunAfterAcquireHook = func(string) {
				if uerr := f.second.UpdateSessionStatus(f.ctx, f.id, "completed"); uerr != nil {
					panic(uerr)
				}
			}
			before = f.events()
			err = f.run(f.request())
		})

		It("is refused as terminal", func() { Expect(err).To(MatchError(session.ErrTerminal)) })
		It("writes nothing", func() { Expect(f.events()).To(Equal(before)) })
		It("has released the lease", func() { Expect(f.canTake()).To(BeTrue()) })
	})
})
