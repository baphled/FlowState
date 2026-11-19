package app

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
)

// mustCreateCrashChild composes a delegated child session under
// parent, panicking on errors, so steering scope fixtures have a
// chain-eligible sender.
func mustCreateCrashChild(ctx context.Context, st *store.Store, parentID string) *store.Session {
	sess, err := CreateChildSession(ctx, st, parentID, "probe.ai", "probe-model", DefaultHarness, "", "child task")
	if err != nil {
		panic(err)
	}
	return sess
}

var _ = Describe("DiagnoseSession", func() {
	var ctx context.Context
	BeforeEach(func() {
		ctx = context.Background()
	})

	It("flags a crashed session with a dangling turn and run activity", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "user", "turn.start", map[string]any{"task": "x"})
		mustAppendEvent(ctx, st, sess.ID, "engine", "provider.request", map[string]any{})
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Crashed).To(BeTrue())
	})

	It("keeps a fresh never-run session un-crashed", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Crashed).To(BeFalse())
	})

	It("reports run activity for a session with provider traffic", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "engine", "provider.request", map[string]any{})
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.RunActivity).To(BeTrue())
	})

	It("keeps run activity false for a fresh session", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.RunActivity).To(BeFalse())
	})

	It("keeps a completed turn un-crashed", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "engine", "provider.request", map[string]any{})
		mustAppendEvent(ctx, st, sess.ID, "engine", "turn.end", map[string]any{"status": "completed"})
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Crashed).To(BeFalse())
	})

	It("keeps a paused session un-crashed", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "engine", "provider.request", map[string]any{})
		mustAppendEvent(ctx, st, sess.ID, "engine", "session.paused", map[string]any{"reason": "budget_exceeded"})
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Crashed).To(BeFalse())
	})

	It("surfaces the raw active row status", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Status).To(Equal("active"))
	})

	It("surfaces the raw cancelled row status for a terminal session", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		if _, cerr := CancelSession(ctx, st, sess.ID); cerr != nil {
			panic(cerr)
		}
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Status).To(Equal("cancelled"))
	})

	It("echoes the session id", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.SessionID).To(Equal(sess.ID))
	})

	It("passes the unknown-session sentinel through unchanged", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		_, err := DiagnoseSession(ctx, st, "no-such-session")
		Expect(errors.Is(err, store.ErrUnknownSession)).To(BeTrue())
	})

	It("carries the reserved total of the budget snapshot", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		if perr := st.SetBudgetPool(ctx, sess.ID, 100); perr != nil {
			panic(perr)
		}
		if _, rerr := st.ReserveBudget(ctx, sess.ID, "doctor-child", 30); rerr != nil {
			panic(rerr)
		}
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Reserved).To(Equal(int64(30)))
	})

	It("carries the pool anchor of the budget snapshot", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		if perr := st.SetBudgetPool(ctx, sess.ID, 100); perr != nil {
			panic(perr)
		}
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		pool := int64(100)
		Expect(d.Pool).To(Equal(&pool))
	})

	It("carries the available remainder of the budget snapshot", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		if perr := st.SetBudgetPool(ctx, sess.ID, 100); perr != nil {
			panic(perr)
		}
		if _, rerr := st.ReserveBudget(ctx, sess.ID, "doctor-child", 30); rerr != nil {
			panic(rerr)
		}
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		available := int64(70)
		Expect(d.Available).To(Equal(&available))
	})

	It("keeps the pool anchor null without a declared budget pool", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Pool).To(BeNil())
	})

	It("keeps the available remainder null without a declared budget pool", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Available).To(BeNil())
	})

	It("counts pending steering messages before the drain", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		parent := mustCreateCrashSession(ctx, st)
		child := mustCreateCrashChild(ctx, st, parent.ID)
		if _, serr := st.SendMessage(ctx, child.ID, parent.ID, "steer left"); serr != nil {
			panic(serr)
		}
		d, err := DiagnoseSession(ctx, st, child.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.PendingMessages).To(Equal(1))
	})

	It("counts zero pending steering messages after the drain", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		parent := mustCreateCrashSession(ctx, st)
		child := mustCreateCrashChild(ctx, st, parent.ID)
		if _, serr := st.SendMessage(ctx, child.ID, parent.ID, "steer left"); serr != nil {
			panic(serr)
		}
		if _, derr := DrainSteeringMessages(ctx, st, child.ID); derr != nil {
			panic(derr)
		}
		d, err := DiagnoseSession(ctx, st, child.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.PendingMessages).To(Equal(0))
	})
})
