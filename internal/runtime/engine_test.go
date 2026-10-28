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
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
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

var _ = Describe("Turn", func() {
	Describe("with a fake streamer", func() {
		It("persists ordered events for a successful turn", func() {
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
			for i, e := range evts {
				Expect(e.Seq).To(Equal(int64(i)))
			}
			Expect(evts[1].Actor).To(Equal(session.ActorUser))
			for _, e := range evts[2:] {
				Expect(e.Actor).To(Equal(session.ActorEngine))
			}
			after, err := st.GetSession(ctx, sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(after.Status).To(Equal("completed"))
		})

		It("persists a typed provider.error event and fails the session on streamer error", func() {
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
			evts, err := st.Replay(ctx, sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(types(evts)).To(Equal([]string{"turn.start", "user.message", "assistant.delta", "provider.error"}))
			last := evts[len(evts)-1]
			Expect(last.Actor).To(Equal(session.ActorEngine))
			for _, sub := range []string{`"error"`, `"terminal":true`} {
				Expect(strings.Contains(last.PayloadJSON, sub)).To(BeTrue())
			}
			after, err := st.GetSession(ctx, sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(after.Status).To(Equal("failed"))
		})
	})
})
