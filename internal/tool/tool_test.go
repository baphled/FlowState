package tool

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
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
// returns the store, session id and a live event writer.
func newStoreWithSession() (*store.Store, string, *store.EventWriter) {
	st, err := store.Open(filepath.Join(GinkgoT().TempDir(), "test.db"))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = st.Close() })
	sess, err := st.CreateSession(context.Background(), "id-1", "prov", "model", "harness", "sha")
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	w, err := st.NewEventWriter(context.Background(), sess.ID)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = w.Close() })
	return st, sess.ID, w
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

var _ = Describe("Registry", func() {
	It("errors with ErrUnknownTool when getting an unknown tool", func() {
		_, err := NewRegistry().Get("nope")
		Expect(errors.Is(err, ErrUnknownTool)).To(BeTrue())
	})

	It("errors with ErrToolExists when registering a duplicate tool", func() {
		r := NewRegistry()
		Expect(r.Register(NewEcho())).To(Succeed())
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
	It("rejects a nil registry or nil writer", func() {
		_, _, w := newStoreWithSession()
		_, err := NewLoop(nil, w, nil)
		Expect(err).To(HaveOccurred())
		_, err = NewLoop(NewRegistry(), nil, nil)
		Expect(err).To(HaveOccurred())
	})

	It("denies a registered tool under an empty allowlist and records the denial", func() {
		st, sid, w := newStoreWithSession()
		r := NewRegistry()
		_ = r.Register(NewEcho())
		l, err := NewLoop(r, w, nil)
		Expect(err).NotTo(HaveOccurred())
		_, err = l.Invoke(context.Background(), Echo, json.RawMessage(`{}`))
		Expect(errors.Is(err, ErrDenied)).To(BeTrue())
		events, err := st.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
		Expect(events[0].Type).To(Equal(TypeToolResult))
		Expect(events[0].PayloadJSON).To(ContainSubstring("tool_denied"))
	})

	It("invokes an allowlisted tool and records call then result events in order", func() {
		st, sid, w := newStoreWithSession()
		r := NewRegistry()
		_ = r.Register(NewEcho())
		l, err := NewLoop(r, w, []string{Echo})
		Expect(err).NotTo(HaveOccurred())
		res, err := l.Invoke(context.Background(), Echo, json.RawMessage(`{"msg":"hi"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(HaveKeyWithValue("msg", "hi"))
		events, err := st.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(2))
		Expect(events[0].Type).To(Equal(TypeToolCall))
		Expect(events[1].Type).To(Equal(TypeToolResult))
		Expect(events[0].Seq).To(Equal(int64(0)))
		Expect(events[1].Seq).To(Equal(int64(1)))
		Expect(events[0].Actor).To(Equal("tool:echo"))
		Expect(events[1].PayloadJSON).To(ContainSubstring(`"ok":true`))
	})

	It("errors with ErrUnknownTool for an unregistered tool without recording events", func() {
		st, sid, w := newStoreWithSession()
		l, err := NewLoop(NewRegistry(), w, []string{"ghost"})
		Expect(err).NotTo(HaveOccurred())
		_, err = l.Invoke(context.Background(), "ghost", nil)
		Expect(errors.Is(err, ErrUnknownTool)).To(BeTrue())
		events, err := st.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(BeEmpty())
	})

	It("wraps tool errors with ErrToolExecuted and records a tool_error event", func() {
		st, sid, w := newStoreWithSession()
		r := NewRegistry()
		boom := errors.New("boom")
		_ = r.Register(fakeTool{name: "failing", err: boom})
		l, err := NewLoop(r, w, []string{"failing"})
		Expect(err).NotTo(HaveOccurred())
		_, err = l.Invoke(context.Background(), "failing", nil)
		Expect(errors.Is(err, ErrToolExecuted)).To(BeTrue())
		Expect(errors.Is(err, boom)).To(BeTrue())
		events, err := st.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(2))
		Expect(events[1].Type).To(Equal(TypeToolResult))
		Expect(events[1].PayloadJSON).To(ContainSubstring("tool_error"))
		Expect(events[1].PayloadJSON).To(ContainSubstring("boom"))
	})

	It("errors with ErrEmptyName when invoking with an empty name", func() {
		_, _, w := newStoreWithSession()
		l, err := NewLoop(NewRegistry(), w, []string{"x"})
		Expect(err).NotTo(HaveOccurred())
		_, err = l.Invoke(context.Background(), "", nil)
		Expect(errors.Is(err, ErrEmptyName)).To(BeTrue())
	})

	It("propagates append failure from a closed writer for an allowlisted tool", func() {
		st, sid, w := newStoreWithSession()
		r := NewRegistry()
		_ = r.Register(NewEcho())
		l, err := NewLoop(r, w, []string{Echo})
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Close()).To(Succeed())
		_, err = l.Invoke(context.Background(), Echo, nil)
		Expect(errors.Is(err, store.ErrClosed)).To(BeTrue())
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
		Expect(w.Close()).To(Succeed())
		_, err = l.Invoke(context.Background(), "echo", nil)
		Expect(errors.Is(err, store.ErrClosed)).To(BeTrue())
	})

	It("allowlists only the configured tools under deny-by-default", func() {
		_, _, w := newStoreWithSession()
		r := NewRegistry()
		_ = r.Register(NewEcho())
		l, err := NewLoop(r, w, []string{Echo})
		Expect(err).NotTo(HaveOccurred())
		Expect(l.Allowed(Echo)).To(BeTrue())
		Expect(l.Allowed("other")).To(BeFalse())
	})
})

var _ = Describe("Echo", func() {
	It("returns an empty object for blank arguments", func() {
		res, err := NewEcho().Invoke(context.Background(), json.RawMessage("  "))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(HaveLen(0))
	})

	It("errors on invalid json arguments", func() {
		_, err := NewEcho().Invoke(context.Background(), json.RawMessage(`{bad`))
		Expect(err).To(HaveOccurred())
	})
})
