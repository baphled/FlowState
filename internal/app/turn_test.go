package app

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"syscall"

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

// reservedChildOf returns the child's reservation status; a panic is a
// setup failure.
func reservedChildOf(st *store.Store, childID string) string {
	res, err := st.ReservationFor(context.Background(), childID)
	if err != nil {
		panic(err)
	}
	return res.Status
}

// reservedInterrupted seeds a pooled parent and a child holding a live
// reservation, with the child's event writer for the interrupt path.
func reservedInterrupted(st *store.Store) (*store.Session, *store.EventWriter) {
	ctx := context.Background()
	parent, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "parent")
	if err != nil {
		panic(err)
	}
	if err = st.SetBudgetPool(ctx, parent.ID, 100); err != nil {
		panic(err)
	}
	child, err := CreateChildSession(ctx, st, parent.ID, "fake", "fake-model", DefaultHarness, "sha", "child")
	if err != nil {
		panic(err)
	}
	if err = ReserveChildBudget(ctx, st, parent.ID, child.ID, 40); err != nil {
		panic(err)
	}
	w, err := st.NewEventWriter(ctx, child.ID)
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = w.Close() })
	return child, w
}

var _ = Describe("cancelOnInterrupt and the budget reservation", func() {
	Context("when the session holds a reservation", func() {
		It("settles it", func() {
			st := newStore()
			child, w := reservedInterrupted(st)
			if err := cancelOnInterrupt(context.Background(), w, st, child); !errors.Is(err, ErrSessionCancelled) {
				panic(err)
			}
			Expect(reservedChildOf(st, child.ID)).To(Equal("charged"))
		})

		It("settles it on a context that is already cancelled", func() {
			st := newStore()
			child, w := reservedInterrupted(st)
			cctx, cancel := context.WithCancelCause(context.Background())
			cancel(SignalCause(syscall.SIGTERM))
			if err := cancelOnInterrupt(cctx, w, st, child); !errors.Is(err, ErrSessionCancelled) {
				panic(err)
			}
			Expect(reservedChildOf(st, child.ID)).To(Equal("charged"))
		})
	})

	Context("when the settle fails", func() {
		var (
			st    *store.Store
			child *store.Session
			err   error
		)

		BeforeEach(func() {
			var childID string
			st, _, childID = failingSettleStore()
			sess, gerr := st.GetSession(context.Background(), childID)
			if gerr != nil {
				panic(gerr)
			}
			child = sess
			w, werr := st.NewEventWriter(context.Background(), childID)
			if werr != nil {
				panic(werr)
			}
			DeferCleanup(func() { _ = w.Close() })
			err = cancelOnInterrupt(context.Background(), w, st, child)
		})

		It("still reports the cancelled class", func() {
			Expect(ErrorCode(err)).To(Equal(CodeSessionCancelled))
		})

		It("reports the settle failure beside it", func() {
			Expect(err).To(MatchError(ErrReservationSettle))
		})

		It("leaves the session cancelled", func() {
			Expect(statusOf(st, child.ID)).To(Equal("cancelled"))
		})

		It("carries the session id structurally", func() {
			var se *SettleError
			if !errors.As(err, &se) {
				panic("the settle failure is not a SettleError")
			}
			Expect(se.SessionID).To(Equal(child.ID))
		})

		It("carries the settle cause structurally", func() {
			var se *SettleError
			if !errors.As(err, &se) {
				panic("the settle failure is not a SettleError")
			}
			Expect(se.SettleCause()).To(MatchError(ContainSubstring("settle blocked")))
		})

		It("is still the cancelled class", func() {
			Expect(errors.Is(err, ErrSessionCancelled)).To(BeTrue())
		})
	})
})

// namedCause mimics a cause defined outside this package (the server's),
// which names its reason only through CancelReason.
type namedCause struct{ reason string }

func (c namedCause) Error() string        { return "named " + c.reason }
func (c namedCause) CancelReason() string { return c.reason }

var _ = Describe("the recorded cancellation reason", func() {
	cancelledWith := func(cause error) context.Context {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(cause)
		return ctx
	}

	DescribeTable("names what cancelled the run",
		func(cause error, want string) {
			Expect(CancelReason(cancelledWith(cause))).To(Equal(want))
		},
		Entry("interrupt", SignalCause(os.Interrupt), "sigint"),
		Entry("termination", SignalCause(syscall.SIGTERM), "sigterm"),
		Entry("hang-up", SignalCause(syscall.SIGHUP), "sighup"),
		Entry("another signal", SignalCause(syscall.SIGQUIT), "cancelled"),
		Entry("a client disconnect", namedCause{"client_disconnected"}, "client_disconnected"),
		Entry("a shutdown", namedCause{"shutdown"}, "shutdown"),
		Entry("a wrapped cause", errors.Join(errors.New("x"), namedCause{"shutdown"}), "shutdown"),
		Entry("no cause", nil, "cancelled"),
		Entry("an unrelated cause", errors.New("boom"), "cancelled"),
	)

	It("records the cause on session.cancelled, read before the write detaches the context", func() {
		st := newStore()
		sess, w := interruptedSession(st)
		if err := cancelOnInterrupt(cancelledWith(SignalCause(syscall.SIGTERM)), w, st, sess); !errors.Is(err, ErrSessionCancelled) {
			panic(err)
		}
		Expect(payloadOf(st, sess.ID, session.TypeSessionCancelled)).To(MatchJSON(`{"status":"cancelled","reason":"sigterm"}`))
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

// backlogStreamer is a closingStreamer that keeps its channel so a
// spec can see how many events the turn left unconsumed.
type backlogStreamer struct {
	closingStreamer
	out <-chan provider.StreamEvent
}

func (b *backlogStreamer) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	b.out = b.closingStreamer.Stream(ctx, req)
	return b.out
}

// failOnceCtx fails the first store call made after arm: Done reports
// closed while armed and the next Err call reports cancellation and disarms.
type failOnceCtx struct {
	context.Context
	armed atomic.Bool
}

func (c *failOnceCtx) Done() <-chan struct{} {
	if c.armed.Load() {
		return closedDone()
	}
	return c.Context.Done()
}

func (c *failOnceCtx) Err() error {
	if c.armed.CompareAndSwap(true, false) {
		return context.Canceled
	}
	return c.Context.Err()
}

// closedDone returns an already-closed done channel.
func closedDone() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

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
			s   *backlogStreamer
		)

		BeforeEach(func() {
			s = &backlogStreamer{closingStreamer: closingStreamer{st: st, fakeStreamer: fakeStreamer{events: []provider.StreamEvent{{Content: "hello"}, {Content: " world"}}}}}
			res, err = TurnWithOptions(context.Background(), st, sess, s, TurnOptions{})
		})

		It("returns the append failure", func() {
			Expect(err).To(MatchError(store.ErrClosed))
		})

		// A later append fails on the closed store as well, so only the
		// unread backlog shows the failed delta stopped the stream.
		It("stops consuming the stream at the failed delta", func() {
			Expect(s.out).To(HaveLen(1))
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

// payloadOf returns the payload of the first event of one type in a
// session's log; a panic is a setup failure.
func payloadOf(st *store.Store, id, typ string) string {
	evts, err := st.Replay(context.Background(), id)
	if err != nil {
		panic(err)
	}
	for _, e := range evts {
		if e.Type == typ {
			return e.PayloadJSON
		}
	}
	panic("no " + typ + " event recorded")
}

// actorOf returns the actor of the first event of one type in a
// session's log; a panic is a setup failure.
func actorOf(st *store.Store, id, typ string) string {
	evts, err := st.Replay(context.Background(), id)
	if err != nil {
		panic(err)
	}
	for _, e := range evts {
		if e.Type == typ {
			return e.Actor
		}
	}
	panic("no " + typ + " event recorded")
}

var _ = Describe("TurnWithOptions attribution and content", func() {
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

	Context("when a turn completes over several chunks", func() {
		var res *TurnResult

		BeforeEach(func() {
			var err error
			res, err = TurnWithOptions(context.Background(), st, sess, &fakeStreamer{events: []provider.StreamEvent{{Content: "Hel"}, {Content: "lo, "}, {Content: "world"}}}, TurnOptions{})
			if err != nil {
				panic(err)
			}
		})

		DescribeTable("the actor of each recorded event",
			func(typ, actor string) {
				Expect(actorOf(st, sess.ID, typ)).To(Equal(actor))
			},
			Entry("user.message", TypeUserMessage, session.ActorUser),
			Entry("assistant.delta", TypeAssistantDelta, session.ActorEngine),
			Entry("assistant.message", TypeAssistantMessage, session.ActorEngine),
			Entry("turn.end", TypeTurnEnd, session.ActorEngine),
		)

		It("concatenates the chunks into FinalContent", func() {
			Expect(res.FinalContent).To(Equal("Hello, world"))
		})

		It("concatenates the chunks into the assistant.message content", func() {
			Expect(payloadOf(st, sess.ID, TypeAssistantMessage)).To(MatchJSON(`{"content":"Hello, world"}`))
		})
	})

	Context("when the provider fails", func() {
		BeforeEach(func() {
			_, err := TurnWithOptions(context.Background(), st, sess, &fakeStreamer{events: []provider.StreamEvent{{Err: errors.New("boom")}}}, TurnOptions{})
			if err == nil {
				panic("expected a provider error")
			}
		})

		It("attributes provider.error to the engine", func() {
			Expect(actorOf(st, sess.ID, TypeProviderError)).To(Equal(session.ActorEngine))
		})

		It("carries the error text in the provider.error payload", func() {
			Expect(lastPayload(st, sess.ID)).To(MatchJSON(`{"error":"boom","terminal":true}`))
		})
	})
})
