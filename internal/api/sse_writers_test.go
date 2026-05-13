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

// TestWriteSSEProviderQuota_RateLimitVariant pins the wire shape of
// the provider_quota SSE event registered by PR1 of the Provider
// Quota and Spend Visibility plan (May 2026). The writer MUST:
//   - inject "type":"provider_quota" at the wire (engine omits it
//     by contract);
//   - preserve the full discriminated-union payload bytewise so the
//     Vue chip's Pinia subscription (PR4a) can switch on the
//     `variant` field;
//   - never drop fields the engine populated.
//
// PR1 acceptance: writer is callable, server fan-out routes
// `provider_quota` chunks here, engine doesn't emit yet (PR4
// territory). This test pins the wire shape so the Vue contract
// spec (web/src/types/contract.spec.ts) and PR4's engine emitter
// have a frozen target.
func TestWriteSSEProviderQuota_RateLimitVariant(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	flusher := flushableRecorder{ResponseRecorder: recorder}

	// Engine-side payload as PR4 will emit it — no `type` field
	// (writer's contract is to inject it).
	enginePayload := `{
		"provider": "anthropic",
		"account_hash": "abc123def456",
		"model": "claude-opus-4-7",
		"observed_at": "2026-05-13T12:00:00Z",
		"stale": false,
		"store_backend": "memory",
		"variant": "rate_limit",
		"rate_limit": {
			"requests": {"limit": 1000, "remaining": 999, "reset": "2026-05-13T13:00:00Z"},
			"tokens": {"limit": -1, "remaining": -1},
			"input": {"limit": -1, "remaining": -1},
			"output": {"limit": 20000, "remaining": 18000, "reset": "2026-05-13T13:00:00Z"},
			"tightest_percent_remaining": 90,
			"tightest_reset_at": "2026-05-13T13:00:00Z"
		}
	}`

	writeSSEProviderQuota(recorder, flusher, enginePayload)

	body := recorder.Body.String()
	const dataPrefix = "data: "
	if !strings.HasPrefix(body, dataPrefix) {
		t.Fatalf("expected SSE data prefix, got %q", body)
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(body, dataPrefix), "\n\n")

	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatalf("payload not JSON-parseable: %v (raw=%q)", err, payload)
	}

	if parsed["type"] != "provider_quota" {
		t.Errorf("expected type=provider_quota, got %v", parsed["type"])
	}
	if parsed["provider"] != "anthropic" {
		t.Errorf("expected provider=anthropic, got %v", parsed["provider"])
	}
	if parsed["account_hash"] != "abc123def456" {
		t.Errorf("expected account_hash to round-trip; got %v", parsed["account_hash"])
	}
	if parsed["variant"] != "rate_limit" {
		t.Errorf("expected variant=rate_limit, got %v", parsed["variant"])
	}
	if parsed["store_backend"] != "memory" {
		t.Errorf("expected store_backend=memory, got %v", parsed["store_backend"])
	}

	rl, ok := parsed["rate_limit"].(map[string]any)
	if !ok {
		t.Fatalf("expected rate_limit object, got %T", parsed["rate_limit"])
	}
	if int(rl["tightest_percent_remaining"].(float64)) != 90 {
		t.Errorf("expected tightest_percent_remaining=90, got %v", rl["tightest_percent_remaining"])
	}
	requests, ok := rl["requests"].(map[string]any)
	if !ok {
		t.Fatalf("expected rate_limit.requests object")
	}
	if int(requests["limit"].(float64)) != 1000 {
		t.Errorf("expected requests.limit=1000, got %v", requests["limit"])
	}
}

// TestWriteSSEProviderQuota_NotConfiguredVariant pins the
// NotConfigured variant wire shape — the Reason string surfaces
// verbatim to the chip tooltip.
func TestWriteSSEProviderQuota_NotConfiguredVariant(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	flusher := flushableRecorder{ResponseRecorder: recorder}

	enginePayload := `{
		"provider": "copilot",
		"account_hash": "xxx",
		"observed_at": "2026-05-13T12:00:00Z",
		"store_backend": "memory",
		"variant": "not_configured",
		"not_configured": {"reason": "subscription-only"}
	}`

	writeSSEProviderQuota(recorder, flusher, enginePayload)

	body := recorder.Body.String()
	const dataPrefix = "data: "
	payload := strings.TrimSuffix(strings.TrimPrefix(body, dataPrefix), "\n\n")

	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatalf("payload not JSON-parseable: %v", err)
	}

	if parsed["type"] != "provider_quota" {
		t.Errorf("expected type=provider_quota, got %v", parsed["type"])
	}
	nc, ok := parsed["not_configured"].(map[string]any)
	if !ok {
		t.Fatalf("expected not_configured object, got %T", parsed["not_configured"])
	}
	if nc["reason"] != "subscription-only" {
		t.Errorf("expected reason=subscription-only, got %v", nc["reason"])
	}
}

// TestWriteSSEProviderQuota_MalformedPayloadIsDropped pins the
// silent-drop-on-malformed contract — mirrors writeSSEContextUsage
// at sse_writers.go:621. A malformed payload MUST NOT emit a partial
// SSE event the frontend's parser classifies as "unknown" and
// discards; the chip stays on the prior value.
func TestWriteSSEProviderQuota_MalformedPayloadIsDropped(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	flusher := flushableRecorder{ResponseRecorder: recorder}

	writeSSEProviderQuota(recorder, flusher, "{not-json")

	if body := recorder.Body.String(); body != "" {
		t.Errorf("malformed payload MUST drop silently; got %q", body)
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
