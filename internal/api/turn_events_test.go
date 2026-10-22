package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/turn"
)

// turnEventsDispatcher embeds DispatcherService-zero-value semantics by
// wrapping spyDispatcher and returning a real Turn registry so
// handleTurnEvents can be exercised without a running engine.
type turnEventsDispatcher struct {
	*spyDispatcher
	reg *turn.Registry
}

// TurnRegistry returns the wrapped turn registry.
func (d *turnEventsDispatcher) TurnRegistry() *turn.Registry { return d.reg }

// turnEventsFixture wires a Server whose dispatcher exposes a real Turn
// registry so handleTurnEvents can be exercised end-to-end without a
// running engine.
type turnEventsFixture struct {
	server   http.Handler
	registry *turn.Registry
	turnID   string
}

// newTurnEventsFixture starts a turn in a fresh registry and returns
// the fixture plus the HTTP handler under test.
func newTurnEventsFixture(t *testing.T) *turnEventsFixture {
	t.Helper()
	reg := turn.NewRegistry()
	turnID, err := reg.Start("sess-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	s := api.NewServer(nil, nil, nil, nil, api.WithDispatcher(&turnEventsDispatcher{spyDispatcher: &spyDispatcher{}, reg: reg}))
	return &turnEventsFixture{server: s.Handler(), registry: reg, turnID: turnID}
}

// get performs a GET against the events endpoint for the given turn id.
func (f *turnEventsFixture) get(t *testing.T, turnID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-1/turns/"+turnID+"/events", http.NoBody)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	return rec
}

// TestHandleTurnEventsUnknownTurn ensures unknown turn ids map to 404
// before any SSE headers are committed.
func TestHandleTurnEventsUnknownTurn(t *testing.T) {
	f := newTurnEventsFixture(t)
	rec := f.get(t, "00000000-0000-0000-0000-000000000000")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestHandleTurnEventsMissingTurnID ensures an empty turn_id maps to
// 400 rather than streaming.
func TestHandleTurnEventsMissingTurnID(t *testing.T) {
	f := newTurnEventsFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-1/turns//events", http.NoBody)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 400 or 404", rec.Code)
	}
}

// TestHandleTurnEventsStreamsContentThenDone drives a complete turn
// through the stream: content chunks for each appended assistant
// message, then the [DONE] sentinel once the turn completes.
func TestHandleTurnEventsStreamsContentThenDone(t *testing.T) {
	f := newTurnEventsFixture(t)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- f.get(t, f.turnID)
	}()

	time.Sleep(50 * time.Millisecond)
	if err := f.registry.Append(f.turnID, session.Message{ID: "m1", Role: "assistant", Content: "hello "}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := f.registry.Append(f.turnID, session.Message{ID: "m2", Role: "assistant", Content: "world"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	f.registry.SetProviderModel(f.turnID, "anthropic", "claude-opus-4.5")
	if err := f.registry.Complete(f.turnID, turn.ModelInfo{}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	select {
	case rec := <-done:
		body := rec.Body.String()
		if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
			t.Fatalf("content-type = %q, want text/event-stream", got)
		}
		if !strings.Contains(body, `"content":"hello "`) {
			t.Errorf("stream missing first chunk: %s", body)
		}
		if !strings.Contains(body, `"content":"world"`) {
			t.Errorf("stream missing second chunk: %s", body)
		}
		if !strings.Contains(body, `"type":"model_active"`) {
			t.Errorf("stream missing model_active event: %s", body)
		}
		if !strings.Contains(body, "data: [DONE]") {
			t.Errorf("stream missing [DONE] sentinel: %s", body)
		}
		if !strings.HasPrefix(strings.TrimSpace(body), "data:") && !strings.Contains(body, "data:") {
			t.Errorf("stream has no data lines: %s", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate after turn completion")
	}
}

// TestHandleTurnEventsMapsRoles pins the role → event-type mapping
// onto the frontend's discriminated union wire shapes.
func TestHandleTurnEventsMapsRoles(t *testing.T) {
	f := newTurnEventsFixture(t)

	done := make(chan string, 1)
	go func() {
		rec := f.get(t, f.turnID)
		done <- rec.Body.String()
	}()

	time.Sleep(50 * time.Millisecond)
	msgs := []session.Message{
		{ID: "t1", Role: "tool_call", ToolName: "bash", ToolInput: "ls"},
		{ID: "t2", Role: "tool", Content: "out"},
		{ID: "t3", Role: "tool", Content: "boom", Status: "error"},
		{ID: "t4", Role: "thinking", Content: "hm"},
		{ID: "t5", Role: "delegation_started", TargetAgent: "mid", ChainID: "c1", ToolCalls: 2, LastTool: "grep", Status: "started"},
	}
	for _, m := range msgs {
		if err := f.registry.Append(f.turnID, m); err != nil {
			t.Fatalf("Append %s: %v", m.ID, err)
		}
	}
	if err := f.registry.Fail(f.turnID, context.Canceled); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	select {
	case body := <-done:
		assertContains(t, body, `"type":"tool_call","name":"bash","status":"running","input":"ls"`)
		assertContains(t, body, `"type":"tool_result","content":"out"`)
		assertContains(t, body, `"type":"tool_error","content":"boom"`)
		assertContains(t, body, `"type":"thinking","content":"hm"`)
		assertContains(t, body, `"type":"delegation","target_agent":"mid","chain_id":"c1"`)
		assertContains(t, body, `"type":"error"`)
		assertContains(t, body, "data: [DONE]")
		var payload map[string]any
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data: ") || strings.Contains(line, "[DONE]") {
				continue
			}
			raw := strings.TrimPrefix(line, "data: ")
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				t.Fatalf("non-JSON payload %q: %v", raw, err)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate after turn failure")
	}
}

// assertContains fails the test when body lacks want.
func assertContains(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Errorf("stream missing %s\nbody: %s", want, body)
	}
}
