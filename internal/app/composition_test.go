package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

func TestApp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "App Suite")
}

// fakeTool is an injectable fake tool used to assert the wiring graph
// without executing real behaviour (M1-COMPOSITION).
type fakeTool struct {
	name    string
	calls   int
	payload string
}

func (f *fakeTool) Name() string { return f.name }

func (f *fakeTool) Invoke(_ context.Context, args json.RawMessage) (any, error) {
	f.calls++
	if f.payload != "" {
		return nil, errors.New(f.payload)
	}
	return map[string]any{"args": args, "tool": f.name}, nil
}

func mustStore() *store.Store {
	st, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = st.Close() })
	return st
}

func mustGraph() (*store.Store, *tool.Registry, []string) {
	reg := tool.NewRegistry()
	if err := reg.Register(tool.NewEcho()); err != nil {
		panic(err)
	}
	return mustStore(), reg, []string{"echo", "fake"}
}

var _ = Describe("Wire", func() {
	It("errors on nil dependencies", func() {
		_, err := Wire(nil)
		Expect(err).To(HaveOccurred())
	})

	It("errors on missing store", func() {
		_, err := Wire(&Dependencies{Registry: tool.NewRegistry()})
		Expect(err).To(HaveOccurred())
	})

	It("errors on missing registry", func() {
		_, err := Wire(&Dependencies{Store: mustStore()})
		Expect(err).To(HaveOccurred())
	})

	It("wires an identical graph for identical dependencies", func() {
		st, reg, allow := mustGraph()
		a, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		b, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		allowed := map[string][2]bool{}
		for _, name := range []string{"echo", "fake", "missing"} {
			allowed[name] = [2]bool{a.Allowed(name), b.Allowed(name)}
		}
		Expect(allowed).To(Equal(map[string][2]bool{
			"echo":    {true, true},
			"fake":    {true, true},
			"missing": {false, false},
		}))
	})

	It("exposes the same store for identical dependencies", func() {
		st, reg, allow := mustGraph()
		a, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		b, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Store()).To(Equal(b.Store()))
	})

	It("exposes the same registry for identical dependencies", func() {
		st, reg, allow := mustGraph()
		a, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		b, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Registry()).To(Equal(b.Registry()))
	})

	It("wires the session loop end-to-end through the composition", func() {
		st, reg, allow := mustGraph()
		fake := &fakeTool{name: "fake"}
		if err := reg.Register(fake); err != nil {
			panic(err)
		}
		c, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		sess, err := st.CreateSession(context.Background(), "s-1", "z.ai", "glm-4.6", "flowstate-cli", "")
		Expect(err).NotTo(HaveOccurred())
		h, err := c.SessionLoop(context.Background(), sess.ID)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Close)
		Expect(h.Loop.Allowed("fake")).To(BeTrue())
	})

	It("rejects tools outside the allowlist in the session loop", func() {
		st, reg, allow := mustGraph()
		fake := &fakeTool{name: "fake"}
		if err := reg.Register(fake); err != nil {
			panic(err)
		}
		c, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		sess, err := st.CreateSession(context.Background(), "s-2", "z.ai", "glm-4.6", "flowstate-cli", "")
		Expect(err).NotTo(HaveOccurred())
		h, err := c.SessionLoop(context.Background(), sess.ID)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Close)
		Expect(h.Loop.Allowed("other")).To(BeFalse())
	})

	It("invokes the fake tool exactly once through the session loop", func() {
		st, reg, allow := mustGraph()
		fake := &fakeTool{name: "fake"}
		if err := reg.Register(fake); err != nil {
			panic(err)
		}
		c, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		sess, err := st.CreateSession(context.Background(), "s-3", "z.ai", "glm-4.6", "flowstate-cli", "")
		Expect(err).NotTo(HaveOccurred())
		h, err := c.SessionLoop(context.Background(), sess.ID)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Close)
		_, err = h.Loop.Invoke(context.Background(), "fake", json.RawMessage(`{"x":1}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.calls).To(Equal(1))
	})

	It("records tool call and result events through the session loop", func() {
		st, reg, allow := mustGraph()
		fake := &fakeTool{name: "fake"}
		if err := reg.Register(fake); err != nil {
			panic(err)
		}
		c, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		sess, err := st.CreateSession(context.Background(), "s-4", "z.ai", "glm-4.6", "flowstate-cli", "")
		Expect(err).NotTo(HaveOccurred())
		h, err := c.SessionLoop(context.Background(), sess.ID)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Close)
		_, err = h.Loop.Invoke(context.Background(), "fake", json.RawMessage(`{"x":1}`))
		Expect(err).NotTo(HaveOccurred())
		events, err := st.Replay(context.Background(), sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect([]string{events[0].Type, events[1].Type}).To(Equal([]string{tool.TypeToolCall, tool.TypeToolResult}))
	})

	It("returns ErrUnknownSession for an unknown session", func() {
		st, reg, allow := mustGraph()
		c, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		_, err = c.SessionLoop(context.Background(), "nope")
		Expect(errors.Is(err, store.ErrUnknownSession)).To(BeTrue())
	})

	// Given a wired composition, when the consumer-side ports are
	// requested, then they expose the same concrete store behaviour as
	// the Store accessor (B-2: no behaviour change, only explicit ports).
	It("creates an active session through the SessionRepo port", func() {
		st, reg, allow := mustGraph()
		c, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		created, err := c.SessionRepo().CreateSession(context.Background(), "sid-port", "p", "m", "h", "sha")
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Status).To(Equal("active"))
	})

	It("appends and replays events through the EventLog port", func() {
		st, reg, allow := mustGraph()
		c, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		if _, err := c.SessionRepo().CreateSession(ctx, "sid-port", "p", "m", "h", "sha"); err != nil {
			panic(err)
		}
		w, err := c.EventLog().NewEventWriter(ctx, "sid-port")
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Append(ctx, "user", "turn.start", map[string]any{"task": "t"})
		Expect(err).NotTo(HaveOccurred())
		if err := w.Close(); err != nil {
			panic(err)
		}
		events, err := c.EventLog().Replay(ctx, "sid-port")
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
	})
})
