// Package streaming — T10b EventTypeContextCompacted contract.
//
// These tests pin the streaming-layer half of [[ADR - Tool-Call
// Atomicity in Context Compaction]]: the streaming package must expose
// a dedicated EventTypeContextCompacted constant and ContextCompactedEvent
// struct distinct from EventTypeRecallSummarized. Overloading the
// recall-summarised event would conflate recall summarisation (emitted
// by internal/recall/query_tools.go) with context compaction (emitted
// by internal/engine buildContextWindow), making downstream
// subscribers unable to tell which fired.
package streaming

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEventTypeContextCompacted_ConstantValue asserts the stable wire
// name. Changing the constant value is a breaking change for any
// downstream subscriber filtering on the string literal.
func TestEventTypeContextCompacted_ConstantValue(t *testing.T) {
	t.Parallel()

	if EventTypeContextCompacted != "context_compacted" {
		t.Fatalf("EventTypeContextCompacted = %q; want %q", EventTypeContextCompacted, "context_compacted")
	}
	if EventTypeContextCompacted == EventTypeRecallSummarized {
		t.Fatalf("EventTypeContextCompacted must differ from EventTypeRecallSummarized per ADR")
	}
}

// TestContextCompactedEvent_Type asserts the event implements Event and
// returns the expected type discriminator.
func TestContextCompactedEvent_Type(t *testing.T) {
	t.Parallel()

	var e Event = ContextCompactedEvent{}
	if e.Type() != EventTypeContextCompacted {
		t.Fatalf("Type() = %q; want %q", e.Type(), EventTypeContextCompacted)
	}
}

// TestContextCompactedEvent_MarshalRoundtrip asserts the event survives
// JSON marshal/unmarshal via the discriminator-aware envelope. This is
// the same contract every other streaming event honours; without it
// subscribers that decode from the wire cannot recover the payload.
func TestContextCompactedEvent_MarshalRoundtrip(t *testing.T) {
	t.Parallel()

	original := ContextCompactedEvent{
		OriginalTokens: 4200,
		SummaryTokens:  560,
		LatencyMS:      1234,
		AgentID:        "t10b-agent",
	}

	data, err := MarshalEvent(original)
	if err != nil {
		t.Fatalf("MarshalEvent: %v", err)
	}

	if !strings.Contains(string(data), `"type":"context_compacted"`) {
		t.Fatalf("marshalled event missing type discriminator: %s", data)
	}

	decoded, err := UnmarshalEvent(data)
	if err != nil {
		t.Fatalf("UnmarshalEvent: %v", err)
	}
	got, ok := decoded.(ContextCompactedEvent)
	if !ok {
		t.Fatalf("UnmarshalEvent returned %T; want ContextCompactedEvent", decoded)
	}
	if got != original {
		t.Fatalf("roundtrip drift: got %+v; want %+v", got, original)
	}
}

// TestContextCompactedEvent_PayloadFieldsMatchPlan asserts the field
// names on the JSON wire match the plan's T10b payload contract
// exactly: OriginalTokens, SummaryTokens, LatencyMS, AgentID. A
// subscriber parsing by field name breaks if these drift.
func TestContextCompactedEvent_PayloadFieldsMatchPlan(t *testing.T) {
	t.Parallel()

	e := ContextCompactedEvent{
		OriginalTokens: 1,
		SummaryTokens:  2,
		LatencyMS:      3,
		AgentID:        "x",
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	body := string(data)
	for _, key := range []string{`"originalTokens"`, `"summaryTokens"`, `"latencyMs"`, `"agentId"`} {
		if !strings.Contains(body, key) {
			t.Fatalf("marshalled body %s missing expected key %s", body, key)
		}
	}
}
