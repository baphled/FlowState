package app

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// interruptedSession seeds one active session and its event writer for
// the cancel-on-interrupt path; panics are setup failures.
func interruptedSession(st *store.Store) (*store.Session, *store.EventWriter) {
	ctx := context.Background()
	sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
	if err != nil {
		panic(err)
	}
	w, err := st.NewEventWriter(ctx, sess.ID)
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = w.Close() })
	return sess, w
}

// statusOf reads one session's persisted status; a panic is a setup failure.
func statusOf(st *store.Store, id string) string {
	got, err := st.GetSession(context.Background(), id)
	if err != nil {
		panic(err)
	}
	return got.Status
}

// lastType reads the type of one session's last recorded event; a
// panic is a setup failure.
func lastType(st *store.Store, id string) string {
	evts, err := st.Replay(context.Background(), id)
	if err != nil || len(evts) == 0 {
		panic("no replayable events")
	}
	return evts[len(evts)-1].Type
}

var _ = Describe("cancelOnInterrupt", func() {
	var (
		st   *store.Store
		sess *store.Session
		w    *store.EventWriter
		err  error
	)

	BeforeEach(func() {
		st = newStore()
		sess, w = interruptedSession(st)
	})

	Context("when both writes succeed", func() {
		BeforeEach(func() {
			err = cancelOnInterrupt(context.Background(), w, st, sess)
		})

		It("reports the session as cancelled", func() {
			Expect(err).To(MatchError(ErrSessionCancelled))
		})

		It("marks the session cancelled", func() {
			Expect(statusOf(st, sess.ID)).To(Equal("cancelled"))
		})

		It("ends the log with the cancellation event", func() {
			Expect(lastType(st, sess.ID)).To(Equal(session.TypeSessionCancelled))
		})
	})

	// The event commits before the status: were the order reversed, a
	// failed append would leave a terminal status with no final event,
	// and a reader polling status first could end without it.
	Context("when the event cannot be recorded", func() {
		BeforeEach(func() {
			if cerr := w.Close(); cerr != nil {
				panic(cerr)
			}
			err = cancelOnInterrupt(context.Background(), w, st, sess)
		})

		It("returns the append failure", func() {
			Expect(err).To(MatchError(store.ErrClosed))
		})

		It("leaves the session non-terminal", func() {
			Expect(statusOf(st, sess.ID)).To(Equal("active"))
		})
	})

	Context("when the status cannot be updated", func() {
		BeforeEach(func() {
			ghost := *sess
			ghost.ID = "01a1f000-0000-4000-8000-00000000dead" // no such session row
			err = cancelOnInterrupt(context.Background(), w, st, &ghost)
		})

		It("returns the status failure", func() {
			Expect(err).To(MatchError(store.ErrUnknownSession))
		})

		It("keeps the recorded cancellation event", func() {
			Expect(lastType(st, sess.ID)).To(Equal(session.TypeSessionCancelled))
		})
	})
})
