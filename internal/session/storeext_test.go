package session

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
)

func newStore() *store.Store {
	st, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = st.Close() })
	return st
}

var _ = Describe("Store-backed session extensions", func() {
	It("lists no sessions for an empty store", func() {
		st := newStore()
		got, err := List(context.Background(), st, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
	})

	It("lists a created session", func() {
		st := newStore()
		ctx := context.Background()
		created, err := Create(ctx, st, "prov", "model-x", DefaultHarness, "deadbeef", "do the thing")
		Expect(err).NotTo(HaveOccurred())
		got, err := List(ctx, st, "")
		Expect(err).NotTo(HaveOccurred())
		Expect([2]any{len(got), got[0].ID}).To(Equal([2]any{1, created.ID}))
	})

	It("filters list results by status", func() {
		st := newStore()
		ctx := context.Background()
		_, err := Create(ctx, st, "p", "m", DefaultHarness, "sha", "t1")
		Expect(err).NotTo(HaveOccurred())
		b, err := Create(ctx, st, "p", "m", DefaultHarness, "sha", "t2")
		Expect(err).NotTo(HaveOccurred())
		if _, err := Close(ctx, st, b.ID); err != nil {
			panic(err)
		}
		got, err := List(ctx, st, "cancelled")
		Expect(err).NotTo(HaveOccurred())
		Expect([2]any{len(got), got[0].ID}).To(Equal([2]any{1, b.ID}))
	})

	It("rejects an invalid status filter with ErrUsage", func() {
		st := newStore()
		_, err := List(context.Background(), st, "banana")
		Expect(errors.Is(err, ErrUsage)).To(BeTrue())
	})

	It("persists a turn start event and active status on create", func() {
		st := newStore()
		ctx := context.Background()
		created, err := Create(ctx, st, "prov", "model", DefaultHarness, "sha1", "hello task")
		Expect(err).NotTo(HaveOccurred())
		events, err := st.Replay(ctx, created.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect([5]any{created.Status, len(events), events[0].Type, events[0].Actor, events[0].Seq}).
			To(Equal([5]any{"active", 1, "turn.start", "user", int64(0)}))
	})

	It("rejects an empty task with ErrUsage", func() {
		st := newStore()
		_, err := Create(context.Background(), st, "p", "m", DefaultHarness, "sha", "")
		Expect(errors.Is(err, ErrUsage)).To(BeTrue())
	})

	It("rejects an empty provider with ErrUsage", func() {
		st := newStore()
		_, err := Create(context.Background(), st, "", "m", DefaultHarness, "sha", "t")
		Expect(errors.Is(err, ErrUsage)).To(BeTrue())
	})

	It("rejects an empty model with ErrUsage", func() {
		st := newStore()
		_, err := Create(context.Background(), st, "p", "", DefaultHarness, "sha", "t")
		Expect(errors.Is(err, ErrUsage)).To(BeTrue())
	})

	It("closes an active session with a cancellation event", func() {
		st := newStore()
		ctx := context.Background()
		created, err := Create(ctx, st, "p", "m", DefaultHarness, "sha", "t")
		Expect(err).NotTo(HaveOccurred())
		closed, err := Close(ctx, st, created.ID)
		Expect(err).NotTo(HaveOccurred())
		events, err := st.Replay(ctx, created.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect([2]any{closed.Status, events[len(events)-1].Type}).
			To(Equal([2]any{"cancelled", "session.cancelled"}))
	})

	It("rejects closing an already closed session with ErrTerminal and no new event", func() {
		st := newStore()
		ctx := context.Background()
		created, err := Create(ctx, st, "p", "m", DefaultHarness, "sha", "t")
		Expect(err).NotTo(HaveOccurred())
		if _, err := Close(ctx, st, created.ID); err != nil {
			panic(err)
		}
		_, closeErr := Close(ctx, st, created.ID)
		events, err := st.Replay(ctx, created.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect([2]any{errors.Is(closeErr, ErrTerminal), len(events)}).To(Equal([2]any{true, 2}))
	})

	It("errors with ErrUnknownSession when closing an unknown session", func() {
		st := newStore()
		_, err := Close(context.Background(), st, "nope")
		Expect(errors.Is(err, store.ErrUnknownSession)).To(BeTrue())
	})
})
