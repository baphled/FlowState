package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

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

func TestGivenNilDependenciesWhenWireThenError(t *testing.T) {
	if _, err := Wire(nil); err == nil {
		t.Fatal("expected error wiring nil dependencies")
	}
}

func TestGivenMissingStoreOrRegistryWhenWireThenTypedError(t *testing.T) {
	if _, err := Wire(&Dependencies{Registry: tool.NewRegistry()}); err == nil {
		t.Fatal("expected error for nil store")
	}
	if _, err := Wire(&Dependencies{Store: mustStore(t)}); err == nil {
		t.Fatal("expected error for nil registry")
	}
}

func TestGivenIdenticalDependenciesWhenWireThenIdenticalGraph(t *testing.T) {
	st, reg, allow := mustGraph(t)
	a, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
	if err != nil {
		t.Fatalf("wire a: %v", err)
	}
	b, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
	if err != nil {
		t.Fatalf("wire b: %v", err)
	}
	if a.Store() != b.Store() || a.Registry() != b.Registry() {
		t.Fatal("identical dependencies must wire identical store and registry")
	}
	for _, name := range []string{"echo", "fake", "missing"} {
		if a.Allowed(name) != b.Allowed(name) {
			t.Fatalf("allowlist wiring diverged for %q", name)
		}
	}
}

func TestGivenInjectableFakesWhenSessionLoopThenGraphWiredEndToEnd(t *testing.T) {
	st, reg, allow := mustGraph(t)
	fake := &fakeTool{name: "fake"}
	if err := reg.Register(fake); err != nil {
		t.Fatalf("register fake: %v", err)
	}
	c, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
	if err != nil {
		t.Fatalf("wire: %v", err)
	}
	sess, err := st.CreateSession(context.Background(), "s-1", "z.ai", "glm-4.6", "flowstate-cli", "")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	h, err := c.SessionLoop(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("session loop: %v", err)
	}
	defer h.Close()
	if !h.Loop.Allowed("fake") || h.Loop.Allowed("other") {
		t.Fatal("loop allowlist does not match wired allowlist")
	}
	if _, err := h.Loop.Invoke(context.Background(), "fake", json.RawMessage(`{"x":1}`)); err != nil {
		t.Fatalf("invoke wired fake: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("wired fake invoked %d times, want 1", fake.calls)
	}
	events, err := st.Replay(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want tool.call + tool.result", len(events))
	}
	if events[0].Type != tool.TypeToolCall || events[1].Type != tool.TypeToolResult {
		t.Fatalf("unexpected event types %q %q", events[0].Type, events[1].Type)
	}
}

func TestGivenUnknownSessionWhenSessionLoopThenUnknownSessionError(t *testing.T) {
	st, reg, allow := mustGraph(t)
	c, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
	if err != nil {
		t.Fatalf("wire: %v", err)
	}
	if _, err := c.SessionLoop(context.Background(), "nope"); !errors.Is(err, store.ErrUnknownSession) {
		t.Fatalf("want ErrUnknownSession, got %v", err)
	}
}

func mustStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func mustGraph(t *testing.T) (*store.Store, *tool.Registry, []string) {
	t.Helper()
	reg := tool.NewRegistry()
	if err := reg.Register(tool.NewEcho()); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	return mustStore(t), reg, []string{"echo", "fake"}
}

// Given a wired composition, when the consumer-side ports are
// requested, then they expose the same concrete store behaviour as
// the Store accessor (B-2: no behaviour change, only explicit ports).
func TestGivenWiredCompositionWhenPortsRequestedThenTheyServeTheStore(t *testing.T) {
	st, reg, allow := mustGraph(t)
	c, err := Wire(&Dependencies{Store: st, Registry: reg, Allowlist: allow})
	if err != nil {
		t.Fatalf("wire: %v", err)
	}
	ctx := context.Background()

	repo := c.SessionRepo()
	created, err := repo.CreateSession(ctx, "sid-port", "p", "m", "h", "sha")
	if err != nil || created.Status != "active" {
		t.Fatalf("create via port = %+v, %v", created, err)
	}

	log := c.EventLog()
	w, err := log.NewEventWriter(ctx, "sid-port")
	if err != nil {
		t.Fatalf("writer via port: %v", err)
	}
	if _, err := w.Append(ctx, "user", "turn.start", map[string]any{"task": "t"}); err != nil {
		t.Fatalf("append via port: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err) // errcheck
	}

	events, err := log.Replay(ctx, "sid-port")
	if err != nil || len(events) != 1 {
		t.Fatalf("replay via port = %+v, %v", events, err)
	}
}
