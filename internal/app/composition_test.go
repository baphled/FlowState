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
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = st.Close() })
	return st
}

func mustGraph() (*store.Store, *tool.Registry, []string) {
	reg := tool.NewRegistry()
	err := reg.Register(tool.NewEcho())
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return mustStore(), reg, []string{"echo", "fake"}
}

var _ = Describe("Wire", func() {
	It("errors on nil dependencies", func() {
		_, err := Wire(nil)
		Expect(err).To(HaveOccurred())
	})

	It("errors on missing store or registry", func() {
		_, err := Wire(&Dependencies{Registry: tool.NewRegistry()})
		Expect(err).To(HaveOccurred())
		_, err = Wire(&Dependencies{Store: mustStore()})
		Expect(err).To(HaveOccurred())
	})

	It("wires an identical graph for identical dependencies", func() {
		st, reg, allow := mustGraph()
		a, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		b, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Store()).To(Equal(b.Store()))
		Expect(a.Registry()).To(Equal(b.Registry()))
		for _, name := range []string{"echo", "fake", "missing"} {
			Expect(a.Allowed(name)).To(Equal(b.Allowed(name)))
		}
	})

	It("wires the graph end-to-end through the session loop", func() {
		st, reg, allow := mustGraph()
		fake := &fakeTool{name: "fake"}
		err := reg.Register(fake)
		Expect(err).NotTo(HaveOccurred())
		c, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		sess, err := st.CreateSession(context.Background(), "s-1", "z.ai", "glm-4.6", "flowstate-cli", "")
		Expect(err).NotTo(HaveOccurred())
		h, err := c.SessionLoop(context.Background(), sess.ID)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Close)
		Expect(h.Loop.Allowed("fake")).To(BeTrue())
		Expect(h.Loop.Allowed("other")).To(BeFalse())
		_, err = h.Loop.Invoke(context.Background(), "fake", json.RawMessage(`{"x":1}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.calls).To(Equal(1))
		events, err := st.Replay(context.Background(), sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(2))
		Expect(events[0].Type).To(Equal(tool.TypeToolCall))
		Expect(events[1].Type).To(Equal(tool.TypeToolResult))
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
	It("serves the store through the consumer-side ports", func() {
		st, reg, allow := mustGraph()
		c, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()

		repo := c.SessionRepo()
		created, err := repo.CreateSession(ctx, "sid-port", "p", "m", "h", "sha")
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Status).To(Equal("active"))

		log := c.EventLog()
		w, err := log.NewEventWriter(ctx, "sid-port")
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Append(ctx, "user", "turn.start", map[string]any{"task": "t"})
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Close()).NotTo(HaveOccurred())

		events, err := log.Replay(ctx, "sid-port")
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
	})
})
