// Package context_test — regression tests for the `compacted_at` JSON
// round-trip bug surfaced on 2026-04-14.
//
// Before the fix: the T8 summary prompt instructed the LLM to emit the
// literal string "PLACEHOLDER_COMPACTED_AT" as the value of the
// `compacted_at` field. CompactionSummary.CompactedAt is a time.Time,
// so json.Unmarshal failed on every real summariser response and
// flowstate_compression_tokens_saved_total never advanced in production.
//
// After the fix: the prompt no longer references `compacted_at`, and
// AutoCompactor.Compact server-stamps the field with time.Now().UTC()
// after a successful unmarshal.
package context_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	contextpkg "github.com/baphled/flowstate/internal/context"
)

// TestAutoCompactor_Compact_SchemaWithoutCompactedAt_ServerStamps proves
// the happy path after the schema change: the summariser returns a JSON
// payload that omits `compacted_at` entirely (the new contract), and
// AutoCompactor.Compact populates CompactedAt itself from wall-clock
// time. CompactedAt must be non-zero and close to now.
func TestAutoCompactor_Compact_SchemaWithoutCompactedAt_ServerStamps(t *testing.T) {
	t.Parallel()

	// New-schema JSON: every field populated except compacted_at.
	raw := `{
		"intent": "schema without compacted_at",
		"key_decisions": ["route via summariser"],
		"errors": [],
		"next_steps": ["persist result"],
		"files_to_restore": [],
		"original_token_count": 0,
		"summary_token_count": 0
	}`

	summariser := &fakeSummariser{response: raw}
	compactor := contextpkg.NewAutoCompactor(summariser)

	before := time.Now().UTC()
	summary, err := compactor.Compact(context.Background(), sampleMessages())
	after := time.Now().UTC()

	if err != nil {
		t.Fatalf("Compact: unexpected error for new-schema payload: %v", err)
	}
	if summary.CompactedAt.IsZero() {
		t.Fatalf("Compact: CompactedAt is zero; server stamp did not run")
	}
	if summary.CompactedAt.Before(before) || summary.CompactedAt.After(after) {
		t.Fatalf("Compact: CompactedAt %v outside [%v, %v]; not stamped from wall clock",
			summary.CompactedAt, before, after)
	}
	if summary.CompactedAt.Location() != time.UTC {
		t.Fatalf("Compact: CompactedAt location = %v; want UTC", summary.CompactedAt.Location())
	}
}

// TestAutoCompactor_Compact_LegacyPlaceholderString_IsIgnored is the
// regression guard for model drift. Originally this test asserted a
// parse error: before the C3 schema change, a summariser (or prompt
// cache) emitting the old contract `"compacted_at":
// "PLACEHOLDER_COMPACTED_AT"` would blow up at json.Unmarshal because
// the value is not RFC3339, and the upstream metric
// flowstate_compression_tokens_saved_total never advanced.
//
// After C3 the `compacted_at` JSON tag is `-`, so any legacy value —
// including the placeholder string — is silently discarded at
// unmarshal time and overwritten by the server stamp. That is a
// strictly better failure mode than the pre-fix behaviour: a drifting
// summariser no longer poisons the compaction pipeline. This test
// pins the new semantic and carries forward the regression intent.
func TestAutoCompactor_Compact_LegacyPlaceholderString_IsIgnored(t *testing.T) {
	t.Parallel()

	legacy := `{
		"intent": "legacy shape",
		"next_steps": ["continue"],
		"compacted_at": "PLACEHOLDER_COMPACTED_AT"
	}`

	summariser := &fakeSummariser{response: legacy}
	compactor := contextpkg.NewAutoCompactor(summariser)

	before := time.Now().UTC()
	summary, err := compactor.Compact(context.Background(), sampleMessages())
	after := time.Now().UTC()

	if err != nil {
		t.Fatalf("Compact: legacy placeholder must now be ignored, not rejected; got err = %v", err)
	}
	// The server stamp must be in effect despite the legacy value.
	if summary.CompactedAt.IsZero() {
		t.Fatalf("Compact: CompactedAt is zero; server stamp did not run after ignoring legacy value")
	}
	if summary.CompactedAt.Before(before) || summary.CompactedAt.After(after) {
		t.Fatalf("Compact: CompactedAt %v outside server-stamp window [%v, %v]",
			summary.CompactedAt, before, after)
	}
}

// TestAutoCompactor_Compact_LLMEmittedValidRFC3339_IsIgnored is the
// defence-in-depth regression guard for C3. The T8 prompt no longer asks
// for `compacted_at`, but nothing stops a drifting summariser (or a
// cached prompt-response pair from an older deployment) from emitting a
// legitimately-shaped RFC3339 value like "2024-01-01T00:00:00Z". Before
// C3 that value was silently accepted by json.Unmarshal, then silently
// overwritten at auto_compaction.go:185 by time.Now().UTC() — the
// overwrite happened to be correct, but the schema made the overwrite
// invisible: nothing in the type system guaranteed the field was
// server-only.
//
// After C3 the `compacted_at` JSON key is `-`, so any value the LLM
// emits is discarded at unmarshal time. The server stamp then runs as
// before. The net observable behaviour is identical for the happy path,
// but the audit trail (field tag) now *documents* that the field is
// server-authoritative.
func TestAutoCompactor_Compact_LLMEmittedValidRFC3339_IsIgnored(t *testing.T) {
	t.Parallel()

	// Shaped like the new contract, but the summariser decided to be
	// "helpful" and add `compacted_at` anyway with a plausible value.
	raw := `{
		"intent": "drifting summariser returns compacted_at",
		"key_decisions": [],
		"errors": [],
		"next_steps": ["stamp server-side"],
		"files_to_restore": [],
		"compacted_at": "2024-01-01T00:00:00Z",
		"original_token_count": 0,
		"summary_token_count": 0
	}`

	summariser := &fakeSummariser{response: raw}
	compactor := contextpkg.NewAutoCompactor(summariser)

	before := time.Now().UTC()
	summary, err := compactor.Compact(context.Background(), sampleMessages())
	after := time.Now().UTC()

	if err != nil {
		t.Fatalf("Compact: unexpected error: %v", err)
	}

	// The LLM-emitted value (2024-01-01) must never survive.
	llmEmitted := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if summary.CompactedAt.Equal(llmEmitted) {
		t.Fatalf("Compact: CompactedAt = %v; LLM-emitted value leaked through unmarshal", summary.CompactedAt)
	}

	// It must be the server stamp, bounded by before/after.
	if summary.CompactedAt.Before(before) || summary.CompactedAt.After(after) {
		t.Fatalf("Compact: CompactedAt %v outside server-stamp window [%v, %v]",
			summary.CompactedAt, before, after)
	}
	if summary.CompactedAt.Location() != time.UTC {
		t.Fatalf("Compact: CompactedAt location = %v; want UTC", summary.CompactedAt.Location())
	}
}

// TestCompactionSummary_JSON_DoesNotContainCompactedAt pins the schema
// change: after C3 the field `compacted_at` never appears in the JSON
// encoding at all. This is the honest signal to reviewers and downstream
// consumers that the field is server-authoritative and not part of the
// wire contract.
func TestCompactionSummary_JSON_DoesNotContainCompactedAt(t *testing.T) {
	t.Parallel()

	summary := contextpkg.CompactionSummary{
		Intent:      "test field visibility",
		NextSteps:   []string{"verify"},
		CompactedAt: time.Date(2026, 4, 14, 10, 30, 45, 0, time.UTC),
	}

	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "compacted_at") {
		t.Fatalf("CompactionSummary JSON still contains `compacted_at` key: %s", data)
	}
	if strings.Contains(string(data), "2026-04-14") {
		t.Fatalf("CompactionSummary JSON leaks the CompactedAt timestamp: %s", data)
	}
}

// TestSummaryPrompt_NoLongerReferencesCompactedAt pins the prompt-level
// fix: the LLM must not be asked for `compacted_at` at all. Any
// mention of the field or of the old PLACEHOLDER_COMPACTED_AT literal
// in the rendered prompt is a regression.
func TestSummaryPrompt_NoLongerReferencesCompactedAt(t *testing.T) {
	t.Parallel()

	out, err := contextpkg.RenderSummaryPrompt(sampleMessages())
	if err != nil {
		t.Fatalf("RenderSummaryPrompt: %v", err)
	}
	if strings.Contains(out, "compacted_at") {
		t.Errorf("prompt still references `compacted_at`; LLM will keep emitting it")
	}
	if strings.Contains(out, "PLACEHOLDER_COMPACTED_AT") {
		t.Errorf("prompt still contains legacy PLACEHOLDER_COMPACTED_AT directive")
	}
}
