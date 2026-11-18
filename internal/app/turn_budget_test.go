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

// fakeStreamer is an injectable provider double for engine-loop tests.
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

var _ = Describe("TurnWithOptions budget stop", func() {
	It("returns BudgetExceeded true when the budget cap is reached", func() {
		st := newStore()
		ctx := context.Background()
		sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
		Expect(err).NotTo(HaveOccurred())
		fake := &fakeStreamer{events: []provider.StreamEvent{
			{Content: "hello"},
			{Content: " world"},
		}}

		res, err := TurnWithOptions(ctx, st, sess, fake, TurnOptions{Budget: 5})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.BudgetExceeded).To(BeTrue())
	})

	It("does not append turn.end on budget stop", func() {
		st := newStore()
		ctx := context.Background()
		sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
		Expect(err).NotTo(HaveOccurred())
		fake := &fakeStreamer{events: []provider.StreamEvent{
			{Content: "hello"},
			{Content: " world"},
		}}

		_, _ = TurnWithOptions(ctx, st, sess, fake, TurnOptions{Budget: 5})
		evts, err := st.Replay(ctx, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(types(evts)).ToNot(ContainElement("turn.end"))
	})

	It("persists a session.paused event on budget stop", func() {
		st := newStore()
		ctx := context.Background()
		sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
		if err != nil {
			panic(err)
		}
		fake := &fakeStreamer{events: []provider.StreamEvent{
			{Content: "hello"},
			{Content: " world"},
		}}

		_, _ = TurnWithOptions(ctx, st, sess, fake, TurnOptions{Budget: 5})
		evts, err := st.Replay(ctx, sess.ID)
		if err != nil {
			panic(err)
		}
		var paused *struct {
			PayloadJSON string
		}
		for _, e := range evts {
			if e.Type == "session.paused" {
				paused = &struct{ PayloadJSON string }{PayloadJSON: e.PayloadJSON}
			}
		}
		Expect(paused).NotTo(BeNil())
	})

	It("persists the paused event with reason budget_exceeded", func() {
		st := newStore()
		ctx := context.Background()
		sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
		if err != nil {
			panic(err)
		}
		fake := &fakeStreamer{events: []provider.StreamEvent{
			{Content: "hello"},
			{Content: " world"},
		}}

		_, _ = TurnWithOptions(ctx, st, sess, fake, TurnOptions{Budget: 5})
		evts, err := st.Replay(ctx, sess.ID)
		if err != nil {
			panic(err)
		}
		var payload string
		found := false
		for _, e := range evts {
			if e.Type == "session.paused" {
				payload = e.PayloadJSON
				found = true
			}
		}
		if !found {
			panic("no session.paused event persisted")
		}
		var p struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			panic(err)
		}
		Expect(p.Reason).NotTo(BeEmpty())
	})

	It("persists the paused event reason as budget_exceeded", func() {
		st := newStore()
		ctx := context.Background()
		sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
		if err != nil {
			panic(err)
		}
		fake := &fakeStreamer{events: []provider.StreamEvent{
			{Content: "hello"},
			{Content: " world"},
		}}

		_, _ = TurnWithOptions(ctx, st, sess, fake, TurnOptions{Budget: 5})
		evts, err := st.Replay(ctx, sess.ID)
		if err != nil {
			panic(err)
		}
		var payload string
		for _, e := range evts {
			if e.Type == "session.paused" {
				payload = e.PayloadJSON
			}
		}
		var p struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			panic(err)
		}
		Expect(p.Reason).To(Equal("budget_exceeded"))
	})

	It("records consumed_budget equal to the streamed content length", func() {
		st := newStore()
		ctx := context.Background()
		sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
		Expect(err).NotTo(HaveOccurred())
		fake := &fakeStreamer{events: []provider.StreamEvent{
			{Content: "hello"},
			{Content: " world"},
		}}

		res, err := TurnWithOptions(ctx, st, sess, fake, TurnOptions{Budget: 5})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.ConsumedBudget).To(Equal(int64(len("hello"))))
	})

	It("leaves the session status paused on budget stop", func() {
		st := newStore()
		ctx := context.Background()
		sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
		Expect(err).NotTo(HaveOccurred())
		fake := &fakeStreamer{events: []provider.StreamEvent{
			{Content: "hello"},
			{Content: " world"},
		}}

		_, _ = TurnWithOptions(ctx, st, sess, fake, TurnOptions{Budget: 5})
		after, err := st.GetSession(ctx, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.Status).To(Equal("paused"))
	})

	It("completes the turn normally when the budget exceeds the content", func() {
		st := newStore()
		ctx := context.Background()
		sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
		Expect(err).NotTo(HaveOccurred())
		fake := &fakeStreamer{events: []provider.StreamEvent{
			{Content: "hello"},
			{Content: " world"},
		}}

		res, err := TurnWithOptions(ctx, st, sess, fake, TurnOptions{Budget: 100})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.BudgetExceeded).To(BeFalse())
	})
})
