package app

import (
	"context"

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
