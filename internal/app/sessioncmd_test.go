package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
)

// mustOpenStore opens an in-memory store, panicking on setup errors so
// specs keep a single behavioural expectation.
func mustOpenStore() *store.Store {
	st, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	return st
}

// mustCreateSession composes a probe session, panicking on setup
// errors so specs keep a single behavioural expectation.
func mustCreateSession(ctx context.Context, st *store.Store, provider string) *store.Session {
	sess, err := CreateSession(ctx, st, provider, "probe-model", DefaultHarness, "probe-config-sha", "probe task")
	if err != nil {
		panic(err)
	}
	return sess
}

// mustCreateChildSession composes a probe child session under
// parentID, panicking on setup errors so specs keep a single
// behavioural expectation.
func mustCreateChildSession(ctx context.Context, st *store.Store, parentID string) *store.Session {
	sess, err := CreateChildSession(ctx, st, parentID, "probe.ai", "probe-model", DefaultHarness, "probe-config-sha", "delegate task")
	if err != nil {
		panic(err)
	}
	return sess
}

var _ = Describe("child session delegation", func() {
	var ctx context.Context
	BeforeEach(func() { ctx = context.Background() })

	It("round-trips the parent linkage on the created child", func() {
		st := mustOpenStore()
		parent := mustCreateSession(ctx, st, "probe.ai")
		child := mustCreateChildSession(ctx, st, parent.ID)
		Expect(child.ParentID).To(Equal(parent.ID))
	})

	It("records the delegated event before the initial turn start", func() {
		st := mustOpenStore()
		parent := mustCreateSession(ctx, st, "probe.ai")
		child := mustCreateChildSession(ctx, st, parent.ID)
		events, err := st.Replay(ctx, child.ID)
		Expect(err).NotTo(HaveOccurred())
		types := make([]string, 0, len(events))
		for _, e := range events {
			types = append(types, e.Type)
		}
		Expect(types).To(Equal([]string{"session.delegated", "turn.start"}))
	})

	It("attributes the delegated event to the user", func() {
		st := mustOpenStore()
		parent := mustCreateSession(ctx, st, "probe.ai")
		child := mustCreateChildSession(ctx, st, parent.ID)
		events, err := st.Replay(ctx, child.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(events[0].Actor).To(Equal("user"))
	})

	It("carries the parent id in the delegated payload", func() {
		st := mustOpenStore()
		parent := mustCreateSession(ctx, st, "probe.ai")
		child := mustCreateChildSession(ctx, st, parent.ID)
		events, err := st.Replay(ctx, child.ID)
		Expect(err).NotTo(HaveOccurred())
		var m map[string]any
		mErr := json.Unmarshal([]byte(events[0].PayloadJSON), &m)
		Expect(mErr).NotTo(HaveOccurred())
		Expect(m["parent_id"]).To(Equal(parent.ID))
	})

	It("rejects an unknown parent with a usage error", func() {
		st := mustOpenStore()
		_, err := CreateChildSession(ctx, st, "00000000-0000-4000-8000-000000000000", "probe.ai", "probe-model", DefaultHarness, "probe-config-sha", "probe task")
		Expect(err).To(MatchError(ErrUsage))
	})

	It("rejects a terminal parent with a usage error", func() {
		st := mustOpenStore()
		parent := mustCreateSession(ctx, st, "probe.ai")
		_, cerr := CloseSession(ctx, st, parent.ID)
		if cerr != nil {
			panic(cerr)
		}
		_, err := CreateChildSession(ctx, st, parent.ID, "probe.ai", "probe-model", DefaultHarness, "probe-config-sha", "probe task")
		Expect(err).To(MatchError(ErrUsage))
	})

	It("supports nested children chains", func() {
		st := mustOpenStore()
		parent := mustCreateSession(ctx, st, "probe.ai")
		child := mustCreateChildSession(ctx, st, parent.ID)
		grandchild := mustCreateChildSession(ctx, st, child.ID)
		Expect(grandchild.ParentID).To(Equal(child.ID))
	})

	It("keeps freshly created roots without a parent", func() {
		st := mustOpenStore()
		root := mustCreateSession(ctx, st, "probe.ai")
		Expect(root.ParentID).To(Equal(""))
	})

	It("persists the parent linkage across store reopen", func() {
		path := filepath.Join(GinkgoT().TempDir(), "ws.db")
		st, err := store.Open(path)
		Expect(err).NotTo(HaveOccurred())
		parent := mustCreateSession(ctx, st, "probe.ai")
		child := mustCreateChildSession(ctx, st, parent.ID)
		err = st.Close()
		Expect(err).NotTo(HaveOccurred())
		reopened, err := store.Open(path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = reopened.Close() })
		sess, err := reopened.GetSession(ctx, child.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(sess.ParentID).To(Equal(parent.ID))
	})
})

var _ = Describe("session command wrappers", func() {
	var ctx context.Context
	BeforeEach(func() {
		ctx = context.Background()
	})

	It("composes session creation with the supplied composition", func() {
		st := mustOpenStore()
		sess, err := CreateSession(ctx, st, "probe.ai", "probe-model", DefaultHarness, "probe-config-sha", "probe task")
		Expect(err).NotTo(HaveOccurred())
		Expect([3]string{sess.Provider, sess.ModelID, sess.ConfigSHA}).To(Equal([3]string{
			"probe.ai", "probe-model", "probe-config-sha",
		}))
	})

	It("composes session listing over the store", func() {
		st := mustOpenStore()
		a := mustCreateSession(ctx, st, "probe.ai")
		b := mustCreateSession(ctx, st, "probe.ai")
		sessions, err := ListSessions(ctx, st, "")
		Expect(err).NotTo(HaveOccurred())
		ids := make([]string, 0, len(sessions))
		for _, s := range sessions {
			ids = append(ids, s.ID)
		}
		Expect(ids).To(ConsistOf(a.ID, b.ID))
	})

	It("composes session inspection of the requested session", func() {
		st := mustOpenStore()
		created := mustCreateSession(ctx, st, "probe.ai")
		sess, _, err := InspectSession(ctx, st, created.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(sess.ID).To(Equal(created.ID))
	})
	It("composes session inspection with the verified event log", func() {
		st := mustOpenStore()
		created := mustCreateSession(ctx, st, "probe.ai")
		_, events, err := InspectSession(ctx, st, created.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).NotTo(BeEmpty())
	})

	It("composes explicit close to the cancelled status", func() {
		st := mustOpenStore()
		created := mustCreateSession(ctx, st, "probe.ai")
		closed, err := CloseSession(ctx, st, created.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(closed.Status).To(Equal("cancelled"))
	})

	It("surfaces unknown-session errors from inspection", func() {
		st := mustOpenStore()
		_, _, err := InspectSession(ctx, st, "00000000-0000-4000-8000-000000000000")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("ReserveChildBudget", func() {
	reserveStore := func() *store.Store {
		st, err := store.OpenMemory()
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = st.Close() })
		return st
	}
	newSess := func(st *store.Store, id, task string) *store.Session {
		sess, err := CreateSession(context.Background(), st, "prov", "model-x", DefaultHarness, "cfg", task)
		if err != nil {
			panic(err)
		}
		return sess
	}

	It("records the reservation grant for a pooled parent", func() {
		st := reserveStore()
		ctx := context.Background()
		parent := newSess(st, "rsv-parent", "parent")
		child := newSess(st, "rsv-child", "child")
		if err := st.SetBudgetPool(ctx, parent.ID, 100); err != nil {
			panic(err)
		}

		if rerr := ReserveChildBudget(ctx, st, parent.ID, child.ID, 40); rerr != nil {
			panic(rerr)
		}
		snap, serr := st.BudgetSnapshot(ctx, parent.ID)
		if serr != nil {
			panic(serr)
		}
		Expect(snap).To(Equal(store.BudgetSnapshot{Pool: &[]int64{100}[0], Reserved: 40, Available: &[]int64{60}[0]}))
	})

	It("maps a missing parent pool to usage on reserve", func() {
		st := reserveStore()
		ctx := context.Background()
		parent := newSess(st, "rsv-parent-2", "parent")
		child := newSess(st, "rsv-child-2", "child")

		err := ReserveChildBudget(ctx, st, parent.ID, child.ID, 10)
		Expect(errors.Is(err, ErrUsage)).To(BeTrue())
	})

	It("maps a capacity overrun to usage on reserve", func() {
		st := reserveStore()
		ctx := context.Background()
		parent := newSess(st, "rsv-parent-3", "parent")
		child := newSess(st, "rsv-child-3", "child")
		if err := st.SetBudgetPool(ctx, parent.ID, 10); err != nil {
			panic(err)
		}

		err := ReserveChildBudget(ctx, st, parent.ID, child.ID, 50)
		Expect(errors.Is(err, ErrUsage)).To(BeTrue())
	})
})

var _ = Describe("ResolveRunBudget", func() {
	resolveStore := func() *store.Store {
		st, err := store.OpenMemory()
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = st.Close() })
		return st
	}

	It("returns an explicit budget unchanged", func() {
		st := resolveStore()
		ctx := context.Background()
		sess, err := CreateSession(ctx, st, "prov", "model-x", DefaultHarness, "cfg", "task")
		if err != nil {
			panic(err)
		}

		got, rerr := ResolveRunBudget(ctx, st, sess, 2000)
		if rerr != nil {
			panic(rerr)
		}
		Expect(got).To(Equal(int64(2000)))
	})

	It("anchors the session pool to an explicit budget", func() {
		st := resolveStore()
		ctx := context.Background()
		sess, err := CreateSession(ctx, st, "prov", "model-x", DefaultHarness, "cfg", "task")
		if err != nil {
			panic(err)
		}

		if _, rerr := ResolveRunBudget(ctx, st, sess, 2000); rerr != nil {
			panic(rerr)
		}
		snap, serr := st.BudgetSnapshot(ctx, sess.ID)
		if serr != nil {
			panic(serr)
		}
		pool := int64(0)
		if snap.Pool != nil {
			pool = *snap.Pool
		}
		Expect(pool).To(Equal(int64(2000)))
	})

	It("returns zero for a session without a reservation", func() {
		st := resolveStore()
		sess, err := CreateSession(context.Background(), st, "prov", "model-x", DefaultHarness, "cfg", "task")
		if err != nil {
			panic(err)
		}

		got, err := ResolveRunBudget(context.Background(), st, sess, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(int64(0)))
	})

	It("binds a delegated child to its remaining grant", func() {
		st := resolveStore()
		ctx := context.Background()
		parent, err := CreateSession(ctx, st, "prov", "model-x", DefaultHarness, "cfg", "parent")
		if err != nil {
			panic(err)
		}
		child, err := CreateChildSession(ctx, st, parent.ID, "prov", "model-x", DefaultHarness, "cfg", "child")
		if err != nil {
			panic(err)
		}
		if err := st.SetBudgetPool(ctx, parent.ID, 100); err != nil {
			panic(err)
		}
		if _, err := st.ReserveBudget(ctx, parent.ID, child.ID, 30); err != nil {
			panic(err)
		}
		if err := st.ChargeReservation(ctx, child.ID, 12); err != nil {
			panic(err)
		}

		got, err := ResolveRunBudget(ctx, st, child, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(int64(18)))
	})
})
