package tool

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
)

func TestTool(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tool Suite")
}

// newStoreWithSession opens a temp SQLite store with one session and
// returns the store, session id and a live event writer. Arrange-only
// helper: fixture failures panic so specs keep one behavioural
// expectation each.
func newStoreWithSession() (*store.Store, string, *store.EventWriter) {
	st, err := store.Open(filepath.Join(GinkgoT().TempDir(), "test.db"))
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = st.Close() })
	sess, err := st.CreateSession(context.Background(), "id-1", "prov", "model", "harness", "sha")
	if err != nil {
		panic(err)
	}
	w, err := st.NewEventWriter(context.Background(), sess.ID)
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = w.Close() })
	return st, sess.ID, w
}

// newEchoLoop wires a Loop over a temp store with one session, the
// echo tool registered under the given allowlist, and returns the
// loop, store and session id (arrange-only helper).
func newEchoLoop(allow []string) (*Loop, *store.Store, string) {
	st, sid, w := newStoreWithSession()
	r := NewRegistry()
	if err := r.Register(NewEcho()); err != nil {
		panic(err)
	}
	l, err := NewLoop(r, w, allow)
	if err != nil {
		panic(err)
	}
	return l, st, sid
}

// fakeTool is a configurable tool for tests.
type fakeTool struct {
	name string
	out  any
	err  error
}

func (f fakeTool) Name() string { return f.name }
func (f fakeTool) Invoke(_ context.Context, _ json.RawMessage) (any, error) {
	return f.out, f.err
}

// contains reports whether s contains sub (test-local shorthand).
func contains(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && strings.Contains(s, sub)
}

var _ = Describe("Registry", func() {
	It("errors with ErrUnknownTool when getting an unknown tool", func() {
		_, err := NewRegistry().Get("nope")
		Expect(errors.Is(err, ErrUnknownTool)).To(BeTrue())
	})

	It("errors with ErrToolExists when registering a duplicate tool", func() {
		r := NewRegistry()
		if err := r.Register(NewEcho()); err != nil {
			panic(err)
		}
		err := r.Register(NewEcho())
		Expect(errors.Is(err, ErrToolExists)).To(BeTrue())
	})

	It("errors with ErrNilTool when registering nil", func() {
		err := NewRegistry().Register(nil)
		Expect(errors.Is(err, ErrNilTool)).To(BeTrue())
	})

	It("lists registered tool names sorted", func() {
		r := NewRegistry()
		_ = r.Register(fakeTool{name: "b"})
		_ = r.Register(fakeTool{name: "a"})
		Expect(r.Names()).To(Equal([]string{"a", "b"}))
	})
})

var _ = Describe("Loop", func() {
	It("rejects a nil registry", func() {
		_, _, w := newStoreWithSession()
		_, err := NewLoop(nil, w, nil)
		Expect(err).To(HaveOccurred())
	})

	It("rejects a nil writer", func() {
		_, err := NewLoop(NewRegistry(), nil, nil)
		Expect(err).To(HaveOccurred())
	})

	It("denies a registered tool under an empty allowlist", func() {
		l, _, _ := newEchoLoop(nil)
		_, err := l.Invoke(context.Background(), Echo, json.RawMessage(`{}`))
		Expect(errors.Is(err, ErrDenied)).To(BeTrue())
	})

	Context("records exactly one tool_result denial event under an empty allowlist", func() {
		var observedEventCount int
		var observedEventType string
		BeforeEach(func() {
			l, st, sid := newEchoLoop(nil)
			_, _ = l.Invoke(context.Background(), Echo, json.RawMessage(`{}`))
			events, err := st.Replay(context.Background(), sid)
			Expect(err).NotTo(HaveOccurred())
			observedEventCount = len(events)
			observedEventType = events[0].Type
		})
		It("records the expected number of events", func() { Expect(observedEventCount).To(Equal(1)) })
		It("records the expected event type", func() { Expect(observedEventType).To(Equal(TypeToolResult)) })
	})

	It("flags the denial payload as tool_denied", func() {
		l, st, sid := newEchoLoop(nil)
		_, _ = l.Invoke(context.Background(), Echo, json.RawMessage(`{}`))
		events, err := st.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(events[0].PayloadJSON).To(ContainSubstring("tool_denied"))
	})

	It("invokes an allowlisted tool and echoes its arguments", func() {
		l, _, _ := newEchoLoop([]string{Echo})
		res, err := l.Invoke(context.Background(), Echo, json.RawMessage(`{"msg":"hi"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(HaveKeyWithValue("msg", "hi"))
	})

	It("records call then result events in order", func() {
		l, st, sid := newEchoLoop([]string{Echo})
		_, err := l.Invoke(context.Background(), Echo, json.RawMessage(`{"msg":"hi"}`))
		Expect(err).NotTo(HaveOccurred())
		events, err := st.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect([]string{events[0].Type, events[1].Type}).To(Equal([]string{TypeToolCall, TypeToolResult}))
	})

	It("sequences the call and result events", func() {
		l, st, sid := newEchoLoop([]string{Echo})
		_, err := l.Invoke(context.Background(), Echo, json.RawMessage(`{"msg":"hi"}`))
		Expect(err).NotTo(HaveOccurred())
		events, err := st.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect([]int64{events[0].Seq, events[1].Seq}).To(Equal([]int64{0, 1}))
	})

	It("attributes the tool call event to the tool actor", func() {
		l, st, sid := newEchoLoop([]string{Echo})
		_, err := l.Invoke(context.Background(), Echo, json.RawMessage(`{"msg":"hi"}`))
		Expect(err).NotTo(HaveOccurred())
		events, err := st.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(events[0].Actor).To(Equal("tool:echo"))
	})

	It("marks the tool result payload ok", func() {
		l, st, sid := newEchoLoop([]string{Echo})
		_, err := l.Invoke(context.Background(), Echo, json.RawMessage(`{"msg":"hi"}`))
		Expect(err).NotTo(HaveOccurred())
		events, err := st.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(events[1].PayloadJSON).To(ContainSubstring(`"ok":true`))
	})

	It("errors with ErrUnknownTool for an unregistered tool", func() {
		_, _, w := newStoreWithSession()
		l, err := NewLoop(NewRegistry(), w, []string{"ghost"})
		Expect(err).NotTo(HaveOccurred())
		_, err = l.Invoke(context.Background(), "ghost", nil)
		Expect(errors.Is(err, ErrUnknownTool)).To(BeTrue())
	})

	It("records no events for an unregistered tool", func() {
		st, sid, w := newStoreWithSession()
		l, err := NewLoop(NewRegistry(), w, []string{"ghost"})
		Expect(err).NotTo(HaveOccurred())
		_, _ = l.Invoke(context.Background(), "ghost", nil)
		events, err := st.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(BeEmpty())
	})

	It("wraps tool errors with ErrToolExecuted", func() {
		_, _, w := newStoreWithSession()
		r := NewRegistry()
		boom := errors.New("boom")
		_ = r.Register(fakeTool{name: "failing", err: boom})
		l, err := NewLoop(r, w, []string{"failing"})
		Expect(err).NotTo(HaveOccurred())
		_, err = l.Invoke(context.Background(), "failing", nil)
		Expect(errors.Is(err, ErrToolExecuted)).To(BeTrue())
	})

	It("preserves the wrapped tool error", func() {
		_, _, w := newStoreWithSession()
		r := NewRegistry()
		boom := errors.New("boom")
		_ = r.Register(fakeTool{name: "failing", err: boom})
		l, err := NewLoop(r, w, []string{"failing"})
		Expect(err).NotTo(HaveOccurred())
		_, err = l.Invoke(context.Background(), "failing", nil)
		Expect(errors.Is(err, boom)).To(BeTrue())
	})

	Context("records a tool_result event on tool error", func() {
		var observedEventCount int
		var observedEventType string
		BeforeEach(func() {
			st, sid, w := newStoreWithSession()
			r := NewRegistry()
			boom := errors.New("boom")
			_ = r.Register(fakeTool{name: "failing", err: boom})
			l, err := NewLoop(r, w, []string{"failing"})
			Expect(err).NotTo(HaveOccurred())
			_, _ = l.Invoke(context.Background(), "failing", nil)
			events, err := st.Replay(context.Background(), sid)
			Expect(err).NotTo(HaveOccurred())
			observedEventCount = len(events)
			observedEventType = events[1].Type
		})
		It("records the expected number of events", func() { Expect(observedEventCount).To(Equal(2)) })
		It("records the tool result type", func() { Expect(observedEventType).To(Equal(TypeToolResult)) })
	})

	Context("flags the error payload as tool_error containing the cause", func() {
		var observedHasToolError bool
		var observedHasCause bool
		BeforeEach(func() {
			st, sid, w := newStoreWithSession()
			r := NewRegistry()
			boom := errors.New("boom")
			_ = r.Register(fakeTool{name: "failing", err: boom})
			l, err := NewLoop(r, w, []string{"failing"})
			Expect(err).NotTo(HaveOccurred())
			_, _ = l.Invoke(context.Background(), "failing", nil)
			events, err := st.Replay(context.Background(), sid)
			Expect(err).NotTo(HaveOccurred())
			observedHasToolError = contains(events[1].PayloadJSON, "tool_error")
			observedHasCause = contains(events[1].PayloadJSON, "boom")
		})
		It("marks the payload as a tool error", func() { Expect(observedHasToolError).To(BeTrue()) })
		It("includes the original error cause", func() { Expect(observedHasCause).To(BeTrue()) })
	})

	It("errors with ErrEmptyName when invoking with an empty name", func() {
		_, _, w := newStoreWithSession()
		l, err := NewLoop(NewRegistry(), w, []string{"x"})
		Expect(err).NotTo(HaveOccurred())
		_, err = l.Invoke(context.Background(), "", nil)
		Expect(errors.Is(err, ErrEmptyName)).To(BeTrue())
	})

	It("propagates append failure from a closed writer for an allowlisted tool", func() {
		_, _, w := newStoreWithSession()
		r := NewRegistry()
		_ = r.Register(NewEcho())
		l, err := NewLoop(r, w, []string{Echo})
		Expect(err).NotTo(HaveOccurred())
		if err := w.Close(); err != nil {
			panic(err)
		}
		_, err = l.Invoke(context.Background(), Echo, nil)
		Expect(errors.Is(err, store.ErrClosed)).To(BeTrue())
	})

	It("records no events when the writer is closed for an allowlisted tool", func() {
		st, sid, w := newStoreWithSession()
		r := NewRegistry()
		_ = r.Register(NewEcho())
		l, err := NewLoop(r, w, []string{Echo})
		Expect(err).NotTo(HaveOccurred())
		if err := w.Close(); err != nil {
			panic(err)
		}
		_, _ = l.Invoke(context.Background(), Echo, nil)
		events, err := st.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(BeEmpty())
	})

	It("propagates append failure from a closed writer for a denied tool", func() {
		_, _, w := newStoreWithSession()
		r := NewRegistry()
		_ = r.Register(NewEcho())
		l, err := NewLoop(r, w, nil)
		Expect(err).NotTo(HaveOccurred())
		if err := w.Close(); err != nil {
			panic(err)
		}
		_, err = l.Invoke(context.Background(), "echo", nil)
		Expect(errors.Is(err, store.ErrClosed)).To(BeTrue())
	})

	It("allowlists the configured tool under deny-by-default", func() {
		l, _, _ := newEchoLoop([]string{Echo})
		Expect(l.Allowed(Echo)).To(BeTrue())
	})

	It("denies unconfigured tools under deny-by-default", func() {
		l, _, _ := newEchoLoop([]string{Echo})
		Expect(l.Allowed("other")).To(BeFalse())
	})
})

var _ = Describe("Echo", func() {
	It("returns an empty object for blank arguments", func() {
		res, err := NewEcho().Invoke(context.Background(), json.RawMessage("  "))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(map[string]any{}))
	})

	It("errors on invalid json arguments", func() {
		_, err := NewEcho().Invoke(context.Background(), json.RawMessage(`{bad`))
		Expect(err).To(HaveOccurred())
	})
})
