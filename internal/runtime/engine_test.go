package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

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

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func types(evts []store.RecordedEvent) []string {
	out := make([]string, len(evts))
	for i, e := range evts {
		out[i] = e.Type
	}
	return out
}

func TestGivenFakeStreamerWhenTurnThenOrderedEventsPersisted(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fake := &fakeStreamer{events: []provider.StreamEvent{
		{Content: "hel"},
		{Content: "lo"},
	}}

	res, err := Turn(ctx, st, sess, fake)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.FinalContent != "hello" {
		t.Fatalf("FinalContent = %q, want %q", res.FinalContent, "hello")
	}
	evts, err := st.Replay(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	got := types(evts)
	want := []string{
		"turn.start",
		"user.message",
		"assistant.delta",
		"assistant.delta",
		"assistant.message",
		"turn.end",
	}
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event types = %v, want %v", got, want)
		}
	}
	for i, e := range evts {
		if e.Seq != int64(i) {
			t.Fatalf("seq %d at index %d, want %d", e.Seq, i, i)
		}
	}
	if evts[1].Actor != session.ActorUser {
		t.Errorf("user.message actor = %q, want %q", evts[1].Actor, session.ActorUser)
	}
	for _, e := range evts[2:] {
		if e.Actor != session.ActorEngine {
			t.Errorf("%s actor = %q, want %q", e.Type, e.Actor, session.ActorEngine)
		}
	}
	after, err := st.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if after.Status != "completed" {
		t.Errorf("session status = %q, want completed", after.Status)
	}
}

func TestGivenStreamerErrorWhenTurnThenTypedErrorEventAndFailedStatus(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "boom")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	boom := errors.New("upstream exploded")
	fake := &fakeStreamer{events: []provider.StreamEvent{
		{Content: "par"},
		{Err: boom},
	}}

	if _, err := Turn(ctx, st, sess, fake); !errors.Is(err, boom) {
		t.Fatalf("Turn err = %v, want %v", err, boom)
	}
	evts, err := st.Replay(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	got := types(evts)
	want := []string{"turn.start", "user.message", "assistant.delta", "provider.error"}
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event types = %v, want %v", got, want)
		}
	}
	last := evts[len(evts)-1]
	if last.Actor != session.ActorEngine {
		t.Errorf("provider.error actor = %q, want %q", last.Actor, session.ActorEngine)
	}
	for _, sub := range []string{`"error"`, `"terminal":true`} {
		if !strings.Contains(last.PayloadJSON, sub) {
			t.Errorf("provider.error payload = %s, want %s", last.PayloadJSON, sub)
		}
	}
	after, err := st.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if after.Status != "failed" {
		t.Errorf("session status = %q, want failed", after.Status)
	}
}
