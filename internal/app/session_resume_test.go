package app

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// pausedFixture creates one budget-paused session, failing the spec
// when the pause setup itself breaks.
func pausedFixture(ctx context.Context, st *store.Store) *store.Session {
	sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
	if err != nil {
		panic(err)
	}
	fake := &fakeStreamer{events: []provider.StreamEvent{
		{Content: "hello"},
		{Content: " world"},
	}}
	if _, err := TurnWithOptions(ctx, st, sess, fake, TurnOptions{Budget: 5}); err != nil {
		panic(err)
	}
	// Reload so the fixture carries the persisted paused status
	// exactly as the CLI's load path observes it.
	loaded, err := st.GetSession(ctx, sess.ID)
	if err != nil {
		panic(err)
	}
	return loaded
}

// firstIndex returns the first index of want in list, or -1.
func firstIndex(list []string, want string) int {
	for i, s := range list {
		if s == want {
			return i
		}
	}
	return -1
}

var _ = Describe("ResumePausedSession", func() {
	Context("resuming a budget-paused session", func() {
		var observedTypes []string
		var observedResumedActor string
		var observedFromStatus string
		var observedStatus string
		BeforeEach(func() {
			st := newStore()
			ctx := context.Background()
			sess := pausedFixture(ctx, st)
			if err := ResumePausedSession(ctx, st, sess); err != nil {
				panic(err)
			}
			evts, err := st.Replay(ctx, sess.ID)
			if err != nil {
				panic(err)
			}
			observedTypes = types(evts)
			for _, e := range evts {
				if e.Type != "session.resumed" {
					continue
				}
				observedResumedActor = e.Actor
				var p struct {
					FromStatus string `json:"from_status"`
				}
				if err := json.Unmarshal([]byte(e.PayloadJSON), &p); err != nil {
					panic(err)
				}
				observedFromStatus = p.FromStatus
			}
			after, err := st.GetSession(ctx, sess.ID)
			if err != nil {
				panic(err)
			}
			observedStatus = after.Status
		})
		It("appends a session.resumed event", func() {
			Expect(observedTypes).To(ContainElement("session.resumed"))
		})
		It("appends the resumed event after the pause event", func() {
			Expect(firstIndex(observedTypes, "session.paused")).To(BeNumerically("<", firstIndex(observedTypes, "session.resumed")))
		})
		It("attributes the resumed event to the user actor", func() {
			Expect(observedResumedActor).To(Equal("user"))
		})
		It("records from_status paused in the resumed payload", func() {
			Expect(observedFromStatus).To(Equal("paused"))
		})
		It("restores the session status to active", func() {
			Expect(observedStatus).To(Equal("active"))
		})
	})

	Context("resuming a session that is not paused", func() {
		var observedNewEvents int
		var observedStatus string
		BeforeEach(func() {
			st := newStore()
			ctx := context.Background()
			sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
			if err != nil {
				panic(err)
			}
			before, err := st.Replay(ctx, sess.ID)
			if err != nil {
				panic(err)
			}
			if rerr := ResumePausedSession(ctx, st, sess); rerr != nil {
				panic(rerr)
			}
			after, err := st.Replay(ctx, sess.ID)
			if err != nil {
				panic(err)
			}
			observedNewEvents = len(after) - len(before)
			loaded, err := st.GetSession(ctx, sess.ID)
			if err != nil {
				panic(err)
			}
			observedStatus = loaded.Status
		})
		It("appends no event", func() {
			Expect(observedNewEvents).To(Equal(0))
		})
		It("keeps the session active", func() {
			Expect(observedStatus).To(Equal("active"))
		})
	})

	Context("a resume that cannot append its event", func() {
		var observedErr error
		var observedStatus string
		var observedTypes []string
		BeforeEach(func() {
			st := newStore()
			ctx := context.Background()
			sess := pausedFixture(ctx, st)
			// Occupy the session's single event-writer slot so the
			// resume append is fenced off deterministically.
			held, err := st.NewEventWriter(ctx, sess.ID)
			if err != nil {
				panic(err)
			}
			DeferCleanup(func() { _ = held.Close() })
			observedErr = ResumePausedSession(ctx, st, sess)
			after, err := st.GetSession(ctx, sess.ID)
			if err != nil {
				panic(err)
			}
			observedStatus = after.Status
			evts, err := st.Replay(ctx, sess.ID)
			if err != nil {
				panic(err)
			}
			observedTypes = types(evts)
		})
		It("surfaces the resume failure", func() {
			Expect(observedErr).To(MatchError(store.ErrWriterExists))
		})
		It("leaves the session paused and unresolved", func() {
			Expect(observedStatus).To(Equal("paused"))
		})
		It("persists no session.resumed event", func() {
			Expect(observedTypes).NotTo(ContainElement("session.resumed"))
		})
	})
})
