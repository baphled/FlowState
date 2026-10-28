package tool

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/baphled/flowstate/internal/store"
)

// newStoreWithSession opens a temp SQLite store with one session and
// returns the store, session id and a live event writer.
func newStoreWithSession(t *testing.T) (*store.Store, string, *store.EventWriter) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sess, err := st.CreateSession(context.Background(), "id-1", "prov", "model", "harness", "sha")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	w, err := st.NewEventWriter(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
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

func TestRegistry_GivenEmptyRegistry_WhenGetUnknownTool_ThenUnknownToolError(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get("nope"); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("err = %v, want ErrUnknownTool", err)
	}
}

func TestRegistry_GivenRegistryWithTool_WhenRegisterDuplicate_ThenToolExistsError(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(NewEcho()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register(NewEcho()); !errors.Is(err, ErrToolExists) {
		t.Fatalf("err = %v, want ErrToolExists", err)
	}
}

func TestRegistry_GivenNilTool_WhenRegister_ThenNilToolError(t *testing.T) {
	if err := NewRegistry().Register(nil); !errors.Is(err, ErrNilTool) {
		t.Fatalf("err = %v, want ErrNilTool", err)
	}
}

func TestRegistry_GivenTools_WhenNames_ThenSorted(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(fakeTool{name: "b"})
	_ = r.Register(fakeTool{name: "a"})
	got := r.Names()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("Names() = %v", got)
	}
}

func TestNewLoop_GivenNilRegistryOrWriter_WhenNewLoop_ThenError(t *testing.T) {
	_, _, w := newStoreWithSession(t)
	if _, err := NewLoop(nil, w, nil); err == nil {
		t.Fatal("nil registry accepted")
	}
	if _, err := NewLoop(NewRegistry(), nil, nil); err == nil {
		t.Fatal("nil writer accepted")
	}
}

func TestLoop_GivenEmptyAllowlist_WhenInvokeRegisteredTool_ThenDeniedWithEvent(t *testing.T) {
	st, sid, w := newStoreWithSession(t)
	r := NewRegistry()
	_ = r.Register(NewEcho())
	l, err := NewLoop(r, w, nil)
	if err != nil {
		t.Fatalf("new loop: %v", err)
	}
	_, err = l.Invoke(context.Background(), Echo, json.RawMessage(`{}`))
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("err = %v, want ErrDenied", err)
	}
	events, _ := st.Replay(context.Background(), sid)
	if len(events) != 1 || events[0].Type != TypeToolResult {
		t.Fatalf("events = %+v, want one tool.result", events)
	}
	if !jsonContains(events[0].PayloadJSON, "tool_denied") {
		t.Fatalf("payload %s lacks tool_denied", events[0].PayloadJSON)
	}
}

func TestLoop_GivenAllowlistedTool_WhenInvoke_ThenCallAndResultEventsInOrder(t *testing.T) {
	st, sid, w := newStoreWithSession(t)
	r := NewRegistry()
	_ = r.Register(NewEcho())
	l, _ := NewLoop(r, w, []string{Echo})
	res, err := l.Invoke(context.Background(), Echo, json.RawMessage(`{"msg":"hi"}`))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if m, ok := res.(map[string]any); !ok || m["msg"] != "hi" {
		t.Fatalf("res = %#v", res)
	}
	events, _ := st.Replay(context.Background(), sid)
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].Type != TypeToolCall || events[1].Type != TypeToolResult {
		t.Fatalf("order = %s, %s", events[0].Type, events[1].Type)
	}
	if events[0].Seq != 0 || events[1].Seq != 1 || events[0].Actor != "tool:echo" {
		t.Fatalf("attribution/seq wrong: %+v", events)
	}
	if !jsonContains(events[1].PayloadJSON, `"ok":true`) {
		t.Fatalf("payload %s lacks ok:true", events[1].PayloadJSON)
	}
}

func TestLoop_GivenUnregisteredTool_WhenInvoke_ThenUnknownToolWithoutEvents(t *testing.T) {
	st, sid, w := newStoreWithSession(t)
	l, _ := NewLoop(NewRegistry(), w, []string{"ghost"})
	_, err := l.Invoke(context.Background(), "ghost", nil)
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("err = %v, want ErrUnknownTool", err)
	}
	events, _ := st.Replay(context.Background(), sid)
	if len(events) != 0 {
		t.Fatalf("len(events) = %d, want 0", len(events))
	}
}

func TestLoop_GivenToolReturnsError_WhenInvoke_ThenErrorEventWithToolErrorCode(t *testing.T) {
	st, sid, w := newStoreWithSession(t)
	r := NewRegistry()
	boom := errors.New("boom")
	_ = r.Register(fakeTool{name: "failing", err: boom})
	l, _ := NewLoop(r, w, []string{"failing"})
	_, err := l.Invoke(context.Background(), "failing", nil)
	if !errors.Is(err, ErrToolExecuted) || !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped boom", err)
	}
	events, _ := st.Replay(context.Background(), sid)
	if len(events) != 2 || events[1].Type != TypeToolResult {
		t.Fatalf("events = %+v", events)
	}
	if !jsonContains(events[1].PayloadJSON, "tool_error") || !jsonContains(events[1].PayloadJSON, "boom") {
		t.Fatalf("payload %s lacks tool_error/boom", events[1].PayloadJSON)
	}
}

func TestLoop_GivenEmptyName_WhenInvoke_ThenEmptyNameError(t *testing.T) {
	_, _, w := newStoreWithSession(t)
	l, _ := NewLoop(NewRegistry(), w, []string{"x"})
	if _, err := l.Invoke(context.Background(), "", nil); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("err = %v, want ErrEmptyName", err)
	}
}

func TestLoop_GivenClosedWriter_WhenInvokeAllowlistedTool_ThenEventAppendFailurePropagates(t *testing.T) {
	st, sid, w := newStoreWithSession(t)
	r := NewRegistry()
	_ = r.Register(NewEcho())
	l, _ := NewLoop(r, w, []string{Echo})
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, err := l.Invoke(context.Background(), Echo, nil)
	if !errors.Is(err, store.ErrClosed) {
		t.Fatalf("err = %v, want store.ErrClosed", err)
	}
	events, _ := st.Replay(context.Background(), sid)
	if len(events) != 0 {
		t.Fatalf("len(events) = %d, want 0", len(events))
	}
}

func TestLoop_GivenClosedWriter_WhenInvokeDeniedTool_ThenAppendFailurePropagates(t *testing.T) {
	_, _, w := newStoreWithSession(t)
	r := NewRegistry()
	_ = r.Register(NewEcho())
	l, _ := NewLoop(r, w, nil) // deny-by-default
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, err := l.Invoke(context.Background(), "echo", nil)
	if !errors.Is(err, store.ErrClosed) {
		t.Fatalf("err = %v, want store.ErrClosed", err)
	}
}

func TestLoop_GivenDenyByDefault_WhenNonAllowlistedAndAllowlistedMixed_ThenOnlyAllowedRuns(t *testing.T) {
	_, _, w := newStoreWithSession(t)
	r := NewRegistry()
	_ = r.Register(NewEcho())
	l, _ := NewLoop(r, w, []string{Echo})
	if l.Allowed(Echo) != true {
		t.Fatal("echo should be allowed")
	}
	if l.Allowed("other") {
		t.Fatal("other should be denied by default")
	}
}

func TestEcho_GivenNoArgs_WhenInvoke_ThenEmptyObjectResult(t *testing.T) {
	res, err := NewEcho().Invoke(context.Background(), json.RawMessage("  "))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if m, ok := res.(map[string]any); !ok || len(m) != 0 {
		t.Fatalf("res = %#v, want empty object", res)
	}
}

func TestEcho_GivenInvalidJSON_WhenInvoke_ThenError(t *testing.T) {
	if _, err := NewEcho().Invoke(context.Background(), json.RawMessage(`{bad`)); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

// jsonContains is a minimal substring check for canonical payloads.
func jsonContains(payload, needle string) bool {
	return len(payload) > 0 && len(needle) > 0 && contains(payload, needle)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
