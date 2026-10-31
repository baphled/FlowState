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

	Context("lists a created session", func() {
		var observedResultCount int
		var observedSessionID string
		var observedExpectedSessionID string
		BeforeEach(func() {
			st := newStore()
			ctx := context.Background()
			created, err := Create(ctx, st, "prov", "model-x", DefaultHarness, "deadbeef", "do the thing")
			Expect(err).NotTo(HaveOccurred())
			got, err := List(ctx, st, "")
			Expect(err).NotTo(HaveOccurred())
			observedResultCount = len(got)
			observedSessionID = got[0].ID
			observedExpectedSessionID = created.ID
		})
		It("returns one matching session", func() { Expect(observedResultCount).To(Equal(1)) })
		It("returns the expected session ID", func() { Expect(observedSessionID).To(Equal(observedExpectedSessionID)) })
	})

	Context("filters list results by status", func() {
		var observedResultCount int
		var observedSessionID string
		var observedExpectedSessionID string
		BeforeEach(func() {
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
			observedResultCount = len(got)
			observedSessionID = got[0].ID
			observedExpectedSessionID = b.ID
		})
		It("returns one matching session", func() { Expect(observedResultCount).To(Equal(1)) })
		It("returns the expected session ID", func() { Expect(observedSessionID).To(Equal(observedExpectedSessionID)) })
	})

	It("rejects an invalid status filter with ErrUsage", func() {
		st := newStore()
		_, err := List(context.Background(), st, "banana")
		Expect(errors.Is(err, ErrUsage)).To(BeTrue())
	})

	Context("persists a turn start event and active status on create", func() {
		var observedStatus string
		var observedEventCount int
		var observedEventType string
		var observedActor string
		var observedSequence int64
		BeforeEach(func() {
			st := newStore()
			ctx := context.Background()
			created, err := Create(ctx, st, "prov", "model", DefaultHarness, "sha1", "hello task")
			Expect(err).NotTo(HaveOccurred())
			events, err := st.Replay(ctx, created.ID)
			Expect(err).NotTo(HaveOccurred())
			observedStatus = created.Status
			observedEventCount = len(events)
			observedEventType = events[0].Type
			observedActor = events[0].Actor
			observedSequence = events[0].Seq
		})
		It("creates an active session", func() { Expect(observedStatus).To(Equal("active")) })
		It("records the expected number of events", func() { Expect(observedEventCount).To(Equal(1)) })
		It("records the expected event type", func() { Expect(observedEventType).To(Equal("turn.start")) })
		It("attributes the initial event to the user", func() { Expect(observedActor).To(Equal("user")) })
		It("starts event numbering at zero", func() { Expect(observedSequence).To(Equal(int64(0))) })
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

	Context("closes an active session with a cancellation event", func() {
		var observedStatus string
		var observedEventType string
		BeforeEach(func() {
			st := newStore()
			ctx := context.Background()
			created, err := Create(ctx, st, "p", "m", DefaultHarness, "sha", "t")
			Expect(err).NotTo(HaveOccurred())
			closed, err := Close(ctx, st, created.ID)
			Expect(err).NotTo(HaveOccurred())
			events, err := st.Replay(ctx, created.ID)
			Expect(err).NotTo(HaveOccurred())
			observedStatus = closed.Status
			observedEventType = events[len(events)-1].Type
		})
		It("marks the closed session cancelled", func() { Expect(observedStatus).To(Equal("cancelled")) })
		It("appends a session cancelled event", func() { Expect(observedEventType).To(Equal("session.cancelled")) })
	})

	Context("rejects closing an already closed session with ErrTerminal and no new event", func() {
		var observedIsTerminal bool
		var observedEventCount int
		BeforeEach(func() {
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
			observedIsTerminal = errors.Is(closeErr, ErrTerminal)
			observedEventCount = len(events)
		})
		It("returns the terminal session sentinel", func() { Expect(observedIsTerminal).To(BeTrue()) })
		It("records the expected number of events", func() { Expect(observedEventCount).To(Equal(2)) })
	})

	It("errors with ErrUnknownSession when closing an unknown session", func() {
		st := newStore()
		_, err := Close(context.Background(), st, "nope")
		Expect(errors.Is(err, store.ErrUnknownSession)).To(BeTrue())
	})
})
