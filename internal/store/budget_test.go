package store

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mustBudgetStore opens an in-memory store; panics are setup failures.
func mustBudgetStore() *Store {
	st, err := OpenMemory()
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = st.Close() })
	return st
}

// mustBudgetSession creates a probe root session; panics are setup failures.
func mustBudgetSession(st *Store, id string) *Session {
	sess, err := st.CreateSession(context.Background(), id, "zai", "glm-5.3", "pi", "sha")
	if err != nil {
		panic(err)
	}
	return sess
}

// mustBudgetChild creates a probe child session; panics are setup failures.
func mustBudgetChild(st *Store, parentID, id string) *Session {
	sess, err := st.CreateChildSession(context.Background(), id, parentID, "zai", "glm-5.3", "pi", "sha")
	if err != nil {
		panic(err)
	}
	return sess
}

// mustBudgetWriter opens the single event writer; panics are setup failures.
func mustBudgetWriter(st *Store, id string) *EventWriter {
	w, err := st.NewEventWriter(context.Background(), id)
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = w.Close() })
	return w
}

// mustBudgetPool declares the session pool; panics are setup failures.
func mustBudgetPool(st *Store, id string, total int64) {
	if err := st.SetBudgetPool(context.Background(), id, total); err != nil {
		panic(err)
	}
}

// mustBudgetReserve reserves grant for child; panics are setup failures.
func mustBudgetReserve(st *Store, parentID, childID string, grant int64) {
	if _, err := st.ReserveBudget(context.Background(), parentID, childID, grant); err != nil {
		panic(err)
	}
}

// mustBudgetSettle settles child's reservation; panics are setup failures.
func mustBudgetSettle(st *Store, childID string) {
	if err := st.SetReservationCharged(context.Background(), childID); err != nil {
		panic(err)
	}
}

var _ = Describe("durable budget reservations", func() {
	It("anchors a declared budget pool on the session", func() {
		ctx := context.Background()
		st := mustBudgetStore()
		parent := mustBudgetSession(st, "budget-parent")
		mustBudgetPool(st, parent.ID, 500)
		snap, err := st.BudgetSnapshot(ctx, parent.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(*snap.Pool).To(Equal(int64(500)))
	})

	It("clears the pool when the total is not positive", func() {
		ctx := context.Background()
		st := mustBudgetStore()
		parent := mustBudgetSession(st, "budget-parent")
		mustBudgetPool(st, parent.ID, 500)
		if err := st.SetBudgetPool(ctx, parent.ID, 0); err != nil {
			panic(err)
		}
		snap, err := st.BudgetSnapshot(ctx, parent.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Pool).To(BeNil())
	})

	It("reports outstanding reserved budget against the pool", func() {
		ctx := context.Background()
		st := mustBudgetStore()
		parent := mustBudgetSession(st, "budget-parent")
		child := mustBudgetChild(st, parent.ID, "budget-child")
		mustBudgetPool(st, parent.ID, 100)
		mustBudgetReserve(st, parent.ID, child.ID, 30)
		if err := st.ChargeReservation(ctx, child.ID, 10); err != nil {
			panic(err)
		}
		snap, err := st.BudgetSnapshot(ctx, parent.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap).To(Equal(BudgetSnapshot{Pool: &[]int64{100}[0], Reserved: 20, Available: &[]int64{80}[0]}))
	})

	It("releases charged reservations from the outstanding budget", func() {
		ctx := context.Background()
		st := mustBudgetStore()
		parent := mustBudgetSession(st, "budget-parent")
		child := mustBudgetChild(st, parent.ID, "budget-child")
		mustBudgetPool(st, parent.ID, 100)
		mustBudgetReserve(st, parent.ID, child.ID, 30)
		if err := st.ChargeReservation(ctx, child.ID, 10); err != nil {
			panic(err)
		}
		mustBudgetSettle(st, child.ID)
		snap, err := st.BudgetSnapshot(ctx, parent.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap).To(Equal(BudgetSnapshot{Pool: &[]int64{100}[0], Reserved: 0, Available: &[]int64{100}[0]}))
	})

	It("writes nothing when a grant exceeds availability", func() {
		ctx := context.Background()
		st := mustBudgetStore()
		parent := mustBudgetSession(st, "budget-parent")
		child := mustBudgetChild(st, parent.ID, "budget-child")
		mustBudgetPool(st, parent.ID, 10)
		if _, err := st.ReserveBudget(ctx, parent.ID, child.ID, 8); err != nil {
			panic(err)
		}
		_, _ = st.ReserveBudget(ctx, parent.ID, "budget-child-2", 5)
		snap, err := st.BudgetSnapshot(ctx, parent.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Reserved).To(Equal(int64(8)))
	})

	It("settles an already charged reservation as a no-op", func() {
		ctx := context.Background()
		st := mustBudgetStore()
		parent := mustBudgetSession(st, "budget-parent")
		child := mustBudgetChild(st, parent.ID, "budget-child")
		mustBudgetPool(st, parent.ID, 100)
		mustBudgetReserve(st, parent.ID, child.ID, 30)
		mustBudgetSettle(st, child.ID)
		mustBudgetSettle(st, child.ID)
		row, err := st.ReservationFor(ctx, child.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(row.Status).To(Equal(ReservationStatusCharged))
	})

	It("pause records consumption on the live reservation", func() {
		ctx := context.Background()
		st := mustBudgetStore()
		parent := mustBudgetSession(st, "budget-parent")
		child := mustBudgetChild(st, parent.ID, "budget-child")
		mustBudgetPool(st, parent.ID, 100)
		mustBudgetReserve(st, parent.ID, child.ID, 30)
		w := mustBudgetWriter(st, child.ID)
		if err := w.Pause(ctx, PauseInfo{Reason: "budget", ConsumedBudget: 12}); err != nil {
			panic(err)
		}
		row, rerr := st.ReservationFor(ctx, child.ID)
		if rerr != nil {
			panic(rerr)
		}
		got := struct {
			Consumed int64
			Status   string
		}{row.Consumed, row.Status}
		Expect(got).To(Equal(struct {
			Consumed int64
			Status   string
		}{12, ReservationStatusReserved}))
	})

	It("pause caps reservation consumption at the grant", func() {
		ctx := context.Background()
		st := mustBudgetStore()
		parent := mustBudgetSession(st, "budget-parent")
		child := mustBudgetChild(st, parent.ID, "budget-child")
		mustBudgetPool(st, parent.ID, 100)
		mustBudgetReserve(st, parent.ID, child.ID, 30)
		w := mustBudgetWriter(st, child.ID)
		if err := w.Pause(ctx, PauseInfo{Reason: "budget", ConsumedBudget: 50}); err != nil {
			panic(err)
		}
		row, err := st.ReservationFor(ctx, child.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(row.Consumed).To(Equal(int64(30)))
	})

	It("pause never shrinks recorded reservation consumption", func() {
		ctx := context.Background()
		st := mustBudgetStore()
		parent := mustBudgetSession(st, "budget-parent")
		child := mustBudgetChild(st, parent.ID, "budget-child")
		mustBudgetPool(st, parent.ID, 100)
		mustBudgetReserve(st, parent.ID, child.ID, 30)
		if err := st.ChargeReservation(ctx, child.ID, 20); err != nil {
			panic(err)
		}
		w := mustBudgetWriter(st, child.ID)
		if err := w.Pause(ctx, PauseInfo{Reason: "budget", ConsumedBudget: 5}); err != nil {
			panic(err)
		}
		row, err := st.ReservationFor(ctx, child.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(row.Consumed).To(Equal(int64(20)))
	})

})
