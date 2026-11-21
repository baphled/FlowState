package app

import (
	"context"
	"errors"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
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

// closingStreamer closes the store as the stream begins, so every
// later append or status write fails; it replays events afterwards.
type closingStreamer struct {
	fakeStreamer
	st *store.Store
}

func (c *closingStreamer) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	if err := c.st.Close(); err != nil {
		panic(err)
	}
	return c.fakeStreamer.Stream(ctx, req)
}

// failOnceCtx fails the first store call made after arm: Done reports
// closed while armed and the next Err call reports cancellation and disarms.
type failOnceCtx struct {
	context.Context
	armed atomic.Bool
}

func (c *failOnceCtx) Done() <-chan struct{} {
	if c.armed.Load() {
		return closedChan
	}
	return c.Context.Done()
}

func (c *failOnceCtx) Err() error {
	if c.armed.CompareAndSwap(true, false) {
		return context.Canceled
	}
	return c.Context.Err()
}

var closedChan = func() chan struct{} { c := make(chan struct{}); close(c); return c }()

// armingStreamer arms its failOnceCtx as the stream starts, so the
// turn's first append fails.
type armingStreamer struct {
	fakeStreamer
	ctx *failOnceCtx
}

func (a *armingStreamer) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	a.ctx.armed.Store(true)
	return a.fakeStreamer.Stream(ctx, req)
}

// eventTypes replays one session's event types; a panic is a setup failure.
func eventTypes(st *store.Store, id string) []string {
	evts, err := st.Replay(context.Background(), id)
	if err != nil {
		panic(err)
	}
	return types(evts)
}

// lastPayload reads the payload of one session's last event; a panic
// is a setup failure.
func lastPayload(st *store.Store, id string) string {
	evts, err := st.Replay(context.Background(), id)
	if err != nil || len(evts) == 0 {
		panic("no replayable events")
	}
	return evts[len(evts)-1].PayloadJSON
}

var _ = Describe("TurnWithOptions persistence failures", func() {
	var (
		st   *store.Store
		sess *store.Session
	)

	BeforeEach(func() {
		st = newStore()
		var err error
		sess, err = session.Create(context.Background(), st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
		if err != nil {
			panic(err)
		}
	})

	Context("when the delta cannot be recorded", func() {
		var (
			res *TurnResult
			err error
		)

		BeforeEach(func() {
			s := &closingStreamer{st: st, fakeStreamer: fakeStreamer{events: []provider.StreamEvent{{Content: "hello"}, {Content: " world"}}}}
			res, err = TurnWithOptions(context.Background(), st, sess, s, TurnOptions{})
		})

		It("returns the append failure", func() {
			Expect(err).To(HaveOccurred())
		})

		It("is not reported as a provider failure", func() {
			Expect(err).NotTo(MatchError(ErrProvider))
		})

		It("returns no result", func() {
			Expect(res).To(BeNil())
		})
	})

	// failOnceCtx's next Err call reports cancellation once and then
	// clears, so exactly one store call fails. A failed append leaves
	// the writer's seq unchanged, so a colliding row or a closed
	// store would fail every later append too; this fails only one.
	Context("when only the delta append fails", func() {
		var (
			err error
			ctx *failOnceCtx
			s   *armingStreamer
		)

		BeforeEach(func() {
			ctx = &failOnceCtx{Context: context.Background()}
			s = &armingStreamer{ctx: ctx, fakeStreamer: fakeStreamer{events: []provider.StreamEvent{{Content: "hello"}, {Content: " world"}}}}
			_, err = TurnWithOptions(ctx, st, sess, s, TurnOptions{})
		})

		It("returns the append failure", func() {
			Expect(err).To(MatchError(context.Canceled))
		})

		It("does not blame the provider", func() {
			Expect(err).NotTo(MatchError(ErrProvider))
		})

		It("records no assistant.message", func() {
			Expect(eventTypes(st, sess.ID)).NotTo(ContainElement(TypeAssistantMessage))
		})

		It("records no turn.end", func() {
			Expect(eventTypes(st, sess.ID)).NotTo(ContainElement(TypeTurnEnd))
		})

		It("stops consuming the stream", func() {
			Expect(eventTypes(st, sess.ID)).NotTo(ContainElement(TypeAssistantDelta))
		})

		It("leaves the session active", func() {
			Expect(statusOf(st, sess.ID)).To(Equal("active"))
		})
	})

	Context("when only the provider.error append fails", func() {
		var err error

		BeforeEach(func() {
			ctx := &failOnceCtx{Context: context.Background()}
			s := &armingStreamer{ctx: ctx, fakeStreamer: fakeStreamer{events: []provider.StreamEvent{{Err: errors.New("boom")}}}}
			_, err = TurnWithOptions(ctx, st, sess, s, TurnOptions{})
		})

		It("keeps the provider cause", func() {
			Expect(err).To(MatchError(ContainSubstring("boom")))
		})

		It("leaves the session status untouched", func() {
			Expect(statusOf(st, sess.ID)).To(Equal("active"))
		})
	})

	Context("when the provider error cannot be recorded", func() {
		var err error

		BeforeEach(func() {
			s := &closingStreamer{st: st, fakeStreamer: fakeStreamer{events: []provider.StreamEvent{{Err: errors.New("boom")}}}}
			_, err = TurnWithOptions(context.Background(), st, sess, s, TurnOptions{})
		})

		It("reports the provider failure", func() {
			Expect(err).To(MatchError(ErrProvider))
		})

		It("keeps the provider cause", func() {
			Expect(err).To(MatchError(ContainSubstring("boom")))
		})

		It("keeps the persistence failure", func() {
			Expect(err).To(MatchError(ContainSubstring("record provider error")))
		})
	})

	Context("when the provider error is recorded but the status cannot be updated", func() {
		var (
			s   *turnState
			err error
		)

		BeforeEach(func() {
			w, werr := st.NewEventWriter(context.Background(), sess.ID)
			if werr != nil {
				panic(werr)
			}
			DeferCleanup(func() { _ = w.Close() })
			ghost := *sess
			ghost.ID = "01a1f000-0000-4000-8000-00000000dead" // no such session row
			s = &turnState{}
			consumeStreamEvent(context.Background(), w, st, &ghost, TurnOptions{}, provider.StreamEvent{Err: errors.New("boom")}, s)
			err = turnFailure(s)
		})

		It("reports the provider failure", func() {
			Expect(err).To(MatchError(ErrProvider))
		})

		It("keeps the provider cause", func() {
			Expect(err).To(MatchError(ContainSubstring("boom")))
		})

		It("keeps the status failure", func() {
			Expect(err).To(MatchError(store.ErrUnknownSession))
		})

		It("appends the provider.error before the status write", func() {
			Expect(lastType(st, sess.ID)).To(Equal(TypeProviderError))
		})
	})

	Context("when the provider fails and persistence works", func() {
		var err error

		BeforeEach(func() {
			_, err = TurnWithOptions(context.Background(), st, sess, &fakeStreamer{events: []provider.StreamEvent{{Content: "par"}, {Err: errors.New("boom")}}}, TurnOptions{})
		})

		It("wraps ErrProvider and the cause", func() {
			Expect(err).To(MatchError(ErrProvider.Error() + ": boom"))
		})

		It("marks the session failed", func() {
			Expect(statusOf(st, sess.ID)).To(Equal("failed"))
		})

		It("ends the log with the provider error", func() {
			Expect(lastType(st, sess.ID)).To(Equal(TypeProviderError))
		})
	})
})

var _ = Describe("TurnWithOptions finish reason", func() {
	var (
		st   *store.Store
		sess *store.Session
	)

	run := func(events ...provider.StreamEvent) *TurnResult {
		res, err := TurnWithOptions(context.Background(), st, sess, &fakeStreamer{events: events}, TurnOptions{})
		if err != nil {
			panic(err)
		}
		return res
	}

	BeforeEach(func() {
		st = newStore()
		var err error
		sess, err = session.Create(context.Background(), st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
		if err != nil {
			panic(err)
		}
	})

	Context("when the provider reports a length finish", func() {
		var res *TurnResult

		BeforeEach(func() {
			res = run(provider.StreamEvent{Content: "cut"}, provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: provider.FinishLength})
		})

		It("records the reason on turn.end", func() {
			Expect(lastPayload(st, sess.ID)).To(MatchJSON(`{"status":"completed","finish_reason":"length"}`))
		})

		It("reports the reason on the result", func() {
			Expect(res.FinishReason).To(Equal(provider.FinishLength))
		})

		It("keeps the session completed", func() {
			Expect(statusOf(st, sess.ID)).To(Equal("completed"))
		})

		It("records no delta for the finish event", func() {
			Expect(eventTypes(st, sess.ID)).To(Equal([]string{"turn.start", "user.message", "assistant.delta", "assistant.message", "turn.end"}))
		})

		It("keeps the streamed content", func() {
			Expect(res.FinalContent).To(Equal("cut"))
		})
	})

	Context("when the provider sends no finish event", func() {
		var res *TurnResult

		BeforeEach(func() {
			res = run(provider.StreamEvent{Content: "plain"})
		})

		It("leaves turn.end exactly as before", func() {
			Expect(lastPayload(st, sess.ID)).To(MatchJSON(`{"status":"completed"}`))
		})

		It("leaves the result reason empty", func() {
			Expect(res.FinishReason).To(BeEmpty())
		})
	})

	Context("when a tool call arrives on the single-turn path", func() {
		BeforeEach(func() {
			call := provider.ToolCall{ID: "c1", Name: "echo"}
			run(provider.StreamEvent{Kind: provider.StreamEventToolCall, ToolCall: &call})
		})

		It("keeps recording it as an empty delta, as before", func() {
			Expect(eventTypes(st, sess.ID)).To(Equal([]string{"turn.start", "user.message", "assistant.delta", "assistant.message", "turn.end"}))
		})
	})
})
