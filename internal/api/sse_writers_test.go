package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWriteSSEStreamingHeartbeat_TokenCount pins the wire shape of the
// streaming.heartbeat SSE event for UI Parity PR5 (Live token counter,
// May 2026). The writer MUST emit `token_count` on the wire so the Vue
// parser at web/src/lib/sseEvent.ts can read it back and the chat
// store can compute tokens-per-second from the delta-vs-prev-tick at
// the documented 15s heartbeat cadence.
//
// Pre-fix the writer dropped TokenCount because the sseStreamingHeartbeat
// struct only carried {type, session_id, agent_id, phase} — the Vue
// counter rendered nothing because the field never reached the wire.
func TestWriteSSEStreamingHeartbeat_TokenCount(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	flusher := flushableRecorder{ResponseRecorder: recorder}

	// Bridge handler shape — newStreamingHeartbeatHandler produces a
	// sanitised payload as map[string]any with primitive values
	// (event_type / session_id / agent_id / phase / token_count) so
	// the SSE writer can re-marshal under the typed sseStreamingHeartbeat
	// shape with the canonical `type` discriminant injected.
	writeSSEStreamingHeartbeat(recorder, flusher, map[string]any{
		"event_type":  "streaming.heartbeat",
		"session_id":  "sess-pr5",
		"agent_id":    "Tech-Lead",
		"phase":       "generating",
		"token_count": int64(1247),
	})

	body := recorder.Body.String()
	// SSE wire shape: `data: <json>\n\n`. Strip the data: prefix so the
	// assertion is on the JSON payload itself.
	const dataPrefix = "data: "
	if !strings.HasPrefix(body, dataPrefix) {
		t.Fatalf("expected SSE data prefix, got %q", body)
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(body, dataPrefix), "\n\n")

	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatalf("payload not JSON-parseable: %v (raw=%q)", err, payload)
	}

	if parsed["type"] != "streaming.heartbeat" {
		t.Errorf("expected type=streaming.heartbeat, got %v", parsed["type"])
	}
	if parsed["session_id"] != "sess-pr5" {
		t.Errorf("expected session_id=sess-pr5, got %v", parsed["session_id"])
	}
	if parsed["agent_id"] != "Tech-Lead" {
		t.Errorf("expected agent_id=Tech-Lead, got %v", parsed["agent_id"])
	}
	if parsed["phase"] != "generating" {
		t.Errorf("expected phase=generating, got %v", parsed["phase"])
	}
	// json.Unmarshal lands numbers as float64; 1247 is exactly representable.
	tc, ok := parsed["token_count"].(float64)
	if !ok {
		t.Fatalf("expected token_count as number, got %T (raw=%q)", parsed["token_count"], payload)
	}
	if int64(tc) != 1247 {
		t.Errorf("expected token_count=1247, got %v", tc)
	}
}

// TestWriteSSEStreamingHeartbeat_TokenCount_ZeroOmitted pins the
// degraded-emission contract — a heartbeat fired before the first
// UsageDelta MUST still emit on the wire, but with token_count=0 so
// the Vue counter renderer suppresses the chip until a positive value
// transitions in. The field is NOT omitempty: an explicit zero is
// informationally distinct from "no field" (which a future incompatible
// catalog could mean "field unknown to this server, route to legacy
// renderer").
func TestWriteSSEStreamingHeartbeat_TokenCount_ZeroEmittedExplicit(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	flusher := flushableRecorder{ResponseRecorder: recorder}

	writeSSEStreamingHeartbeat(recorder, flusher, map[string]any{
		"event_type":  "streaming.heartbeat",
		"session_id":  "sess-pr5-zero",
		"agent_id":    "planner",
		"phase":       "thinking",
		"token_count": int64(0),
	})

	body := recorder.Body.String()
	const dataPrefix = "data: "
	payload := strings.TrimSuffix(strings.TrimPrefix(body, dataPrefix), "\n\n")

	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatalf("payload not JSON-parseable: %v", err)
	}

	tc, ok := parsed["token_count"]
	if !ok {
		t.Fatalf("token_count missing from wire payload — zero MUST be explicit not omitted (raw=%q)", payload)
	}
	if num, isNum := tc.(float64); !isNum || int64(num) != 0 {
		t.Errorf("expected explicit token_count=0, got %v (%T)", tc, tc)
	}
}

// flushableRecorder wraps httptest.ResponseRecorder to satisfy
// http.Flusher (the recorder doesn't implement it by default). The
// writer's Flush call is a no-op on the recorder since it's an
// in-memory buffer; we just need the type-assertion to compile.
type flushableRecorder struct {
	*httptest.ResponseRecorder
}

func (flushableRecorder) Flush() {}
