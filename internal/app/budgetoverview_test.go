package app

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
)

var _ = Describe("BudgetOverview", func() {
	It("returns the store snapshot fields for a session with a declared pool", func() {
		st, err := store.OpenMemory()
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = st.Close() })
		ctx := context.Background()
		sess, err := CreateSession(ctx, st, "overview-prov", "model-x", DefaultHarness, "cfg", "seed task")
		if err != nil {
			panic(err)
		}
		if err := st.SetBudgetPool(ctx, sess.ID, 100); err != nil {
			panic(err)
		}
		if _, err := st.ReserveBudget(ctx, sess.ID, "overview-child", 30); err != nil {
			panic(err)
		}
		snap, oerr := BudgetOverview(ctx, st, sess.ID)
		if oerr != nil {
			panic(oerr)
		}
		pool, available := int64(100), int64(70)
		Expect(snap).To(Equal(store.BudgetSnapshot{
			Pool: &pool, Reserved: 30, Available: &available,
		}))
	})

	It("preserves store.ErrUnknownSession without reclassifying it as usage", func() {
		st, err := store.OpenMemory()
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = st.Close() })
		_, err = BudgetOverview(context.Background(), st, "no-such-session")
		Expect(errors.Is(err, store.ErrUnknownSession)).To(BeTrue())
	})

	It("does not reclassify an unknown session as usage", func() {
		st, err := store.OpenMemory()
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = st.Close() })
		_, oerr := BudgetOverview(context.Background(), st, "no-such-session")
		Expect(errors.Is(oerr, ErrUsage)).To(BeFalse())
	})
})
