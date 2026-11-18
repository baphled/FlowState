package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

func TestRuntime(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Runtime Suite")
}

// fakeStreamer is an injectable provider double for engine-loop tests;
// the engine never requires registry.Resolve (M1-COMPOSITION).
type fakeStreamer struct {
	events []provider.StreamEvent
}

func (f *fakeStreamer) Name() string { return "fake" }
func (f *fakeStreamer) Models() []string {
	return []string{"fake-model"}
}
func (f *fakeStreamer) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: "ignored"}, nil
}
func (f *fakeStreamer) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	out := make(chan provider.StreamEvent, len(f.events))
	for _, e := range f.events {
		out <- e
	}
	close(out)
	return out
}

func newStore() *store.Store {
	st, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = st.Close() })
	return st
}

func types(evts []store.RecordedEvent) []string {
	out := make([]string, len(evts))
	for i, e := range evts {
		out[i] = e.Type
	}
	return out
}

func actors(evts []store.RecordedEvent) []string {
	out := make([]string, len(evts))
	for i, e := range evts {
		out[i] = e.Actor
	}
	return out
}

func seqs(evts []store.RecordedEvent) []int64 {
	out := make([]int64, len(evts))
	for i, e := range evts {
		out[i] = e.Seq
	}
	return out
}

var _ = Describe("Turn", func() {
	Describe("with a fake streamer", func() {
		It("joins streamed deltas into the final content", func() {
			st := newStore()
			ctx := context.Background()
			sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
			Expect(err).NotTo(HaveOccurred())
			fake := &fakeStreamer{events: []provider.StreamEvent{
				{Content: "hel"},
				{Content: "lo"},
			}}

			res, err := Turn(ctx, st, sess, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.FinalContent).To(Equal("hello"))
		})

		It("persists the ordered event sequence for a successful turn", func() {
			st := newStore()
			ctx := context.Background()
			sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
			Expect(err).NotTo(HaveOccurred())
			fake := &fakeStreamer{events: []provider.StreamEvent{
				{Content: "hel"},
				{Content: "lo"},
			}}

			_, err = Turn(ctx, st, sess, fake)
			Expect(err).NotTo(HaveOccurred())
			evts, err := st.Replay(ctx, sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(seqs(evts)).To(Equal([]int64{0, 1, 2, 3, 4, 5}))
		})

		It("persists the full event type stream for a successful turn", func() {
			st := newStore()
			ctx := context.Background()
			sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
			Expect(err).NotTo(HaveOccurred())
			fake := &fakeStreamer{events: []provider.StreamEvent{
				{Content: "hel"},
				{Content: "lo"},
			}}

			_, err = Turn(ctx, st, sess, fake)
			Expect(err).NotTo(HaveOccurred())
			evts, err := st.Replay(ctx, sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(types(evts)).To(Equal([]string{
				"turn.start",
				"user.message",
				"assistant.delta",
				"assistant.delta",
				"assistant.message",
				"turn.end",
			}))
		})

		It("attributes each persisted event to the expected actor", func() {
			st := newStore()
			ctx := context.Background()
			sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
			Expect(err).NotTo(HaveOccurred())
			fake := &fakeStreamer{events: []provider.StreamEvent{
				{Content: "hel"},
				{Content: "lo"},
			}}

			_, err = Turn(ctx, st, sess, fake)
			Expect(err).NotTo(HaveOccurred())
			evts, err := st.Replay(ctx, sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(actors(evts)).To(Equal([]string{
				session.ActorUser,
				session.ActorUser,
				session.ActorEngine,
				session.ActorEngine,
				session.ActorEngine,
				session.ActorEngine,
			}))
		})

		It("marks the session completed after a successful turn", func() {
			st := newStore()
			ctx := context.Background()
			sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
			Expect(err).NotTo(HaveOccurred())
			fake := &fakeStreamer{events: []provider.StreamEvent{
				{Content: "hel"},
				{Content: "lo"},
			}}

			_, err = Turn(ctx, st, sess, fake)
			Expect(err).NotTo(HaveOccurred())
			after, err := st.GetSession(ctx, sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(after.Status).To(Equal("completed"))
		})

		It("surfaces the streamer error from the turn", func() {
			st := newStore()
			ctx := context.Background()
			sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "boom")
			Expect(err).NotTo(HaveOccurred())
			boom := errors.New("upstream exploded")
			fake := &fakeStreamer{events: []provider.StreamEvent{
				{Content: "par"},
				{Err: boom},
			}}

			_, err = Turn(ctx, st, sess, fake)
			Expect(errors.Is(err, boom)).To(BeTrue())
		})

		It("persists the typed provider.error event stream on streamer error", func() {
			st := newStore()
			ctx := context.Background()
			sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "boom")
			Expect(err).NotTo(HaveOccurred())
			boom := errors.New("upstream exploded")
			fake := &fakeStreamer{events: []provider.StreamEvent{
				{Content: "par"},
				{Err: boom},
			}}

			_, _ = Turn(ctx, st, sess, fake)
			evts, err := st.Replay(ctx, sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(types(evts)).To(Equal([]string{"turn.start", "user.message", "assistant.delta", "provider.error"}))
		})

		It("attributes the provider.error event to the engine actor", func() {
			st := newStore()
			ctx := context.Background()
			sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "boom")
			Expect(err).NotTo(HaveOccurred())
			boom := errors.New("upstream exploded")
			fake := &fakeStreamer{events: []provider.StreamEvent{
				{Content: "par"},
				{Err: boom},
			}}

			_, _ = Turn(ctx, st, sess, fake)
			evts, err := st.Replay(ctx, sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(evts[len(evts)-1].Actor).To(Equal(session.ActorEngine))
		})

		Context("serialises the terminal error payload in the provider.error event", func() {
			var observedHasError bool
			var observedIsTerminal bool
			BeforeEach(func() {
				st := newStore()
				ctx := context.Background()
				sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "boom")
				Expect(err).NotTo(HaveOccurred())
				boom := errors.New("upstream exploded")
				fake := &fakeStreamer{events: []provider.StreamEvent{
					{Content: "par"},
					{Err: boom},
				}}

				_, _ = Turn(ctx, st, sess, fake)
				evts, err := st.Replay(ctx, sess.ID)
				Expect(err).NotTo(HaveOccurred())
				last := evts[len(evts)-1].PayloadJSON
				observedHasError = strings.Contains(last, `"error"`)
				observedIsTerminal = strings.Contains(last, `"terminal":true`)
			})
			It("includes the error in the payload", func() { Expect(observedHasError).To(BeTrue()) })
			It("marks the provider error terminal", func() { Expect(observedIsTerminal).To(BeTrue()) })
		})

		It("marks the session failed on streamer error", func() {
			st := newStore()
			ctx := context.Background()
			sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "boom")
			Expect(err).NotTo(HaveOccurred())
			boom := errors.New("upstream exploded")
			fake := &fakeStreamer{events: []provider.StreamEvent{
				{Content: "par"},
				{Err: boom},
			}}

			_, _ = Turn(ctx, st, sess, fake)
			after, err := st.GetSession(ctx, sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(after.Status).To(Equal("failed"))
		})
	})
})
