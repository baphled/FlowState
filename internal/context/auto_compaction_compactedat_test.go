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

// TestAutoCompactor_Compact_LegacyPlaceholderString_ReturnsParseError is
// the regression guard for model drift: if a summariser (or prompt
// cache) still emits the old contract
// (`"compacted_at": "PLACEHOLDER_COMPACTED_AT"`), Compact must surface a
// clear parse error rather than silently succeed with a zero time or
// crash. We accept that this particular shape is a hard error — the
// prompt change means legitimate responses never contain it.
func TestAutoCompactor_Compact_LegacyPlaceholderString_ReturnsParseError(t *testing.T) {
	t.Parallel()

	legacy := `{
		"intent": "legacy shape",
		"next_steps": ["continue"],
		"compacted_at": "PLACEHOLDER_COMPACTED_AT"
	}`

	summariser := &fakeSummariser{response: legacy}
	compactor := contextpkg.NewAutoCompactor(summariser)

	_, err := compactor.Compact(context.Background(), sampleMessages())
	if err == nil {
		t.Fatalf("Compact: expected parse error for legacy placeholder string; got nil")
	}
	if !strings.Contains(err.Error(), "parse") && !strings.Contains(err.Error(), "unmarshal") {
		t.Fatalf("Compact: err = %q; want message mentioning parse/unmarshal", err.Error())
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
