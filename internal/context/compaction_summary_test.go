package context_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	contextpkg "github.com/baphled/flowstate/internal/context"
)

// TestCompactionSummary_RoundTrip_PreservesEveryField marshals a fully
// populated CompactionSummary to JSON, unmarshals it back, and asserts
// every field survives the trip byte-for-byte (modulo time precision,
// which we normalise to UTC RFC3339Nano via time.Equal).
func TestCompactionSummary_RoundTrip_PreservesEveryField(t *testing.T) {
	t.Parallel()

	original := contextpkg.CompactionSummary{
		Intent:             "implement auto-compaction L2 pipeline",
		KeyDecisions:       []string{"use LLM summariser", "unit-aligned boundaries"},
		Errors:             []string{"initial prompt leaked tool names", "token budget exceeded once"},
		NextSteps:          []string{"wire SummariserResolver", "persist summaries to disk"},
		FilesToRestore:     []string{"/abs/path/a.go", "/abs/path/b.go"},
		CompactedAt:        time.Date(2026, 4, 14, 10, 30, 45, 123456789, time.UTC),
		OriginalTokenCount: 12000,
		SummaryTokenCount:  850,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded contextpkg.CompactionSummary
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Intent != original.Intent {
		t.Errorf("Intent: got %q want %q", decoded.Intent, original.Intent)
	}
	if !equalStrings(decoded.KeyDecisions, original.KeyDecisions) {
		t.Errorf("KeyDecisions: got %v want %v", decoded.KeyDecisions, original.KeyDecisions)
	}
	if !equalStrings(decoded.Errors, original.Errors) {
		t.Errorf("Errors: got %v want %v", decoded.Errors, original.Errors)
	}
	if !equalStrings(decoded.NextSteps, original.NextSteps) {
		t.Errorf("NextSteps: got %v want %v", decoded.NextSteps, original.NextSteps)
	}
	if !equalStrings(decoded.FilesToRestore, original.FilesToRestore) {
		t.Errorf("FilesToRestore: got %v want %v", decoded.FilesToRestore, original.FilesToRestore)
	}
	if !decoded.CompactedAt.Equal(original.CompactedAt) {
		t.Errorf("CompactedAt: got %v want %v", decoded.CompactedAt, original.CompactedAt)
	}
	if decoded.OriginalTokenCount != original.OriginalTokenCount {
		t.Errorf("OriginalTokenCount: got %d want %d", decoded.OriginalTokenCount, original.OriginalTokenCount)
	}
	if decoded.SummaryTokenCount != original.SummaryTokenCount {
		t.Errorf("SummaryTokenCount: got %d want %d", decoded.SummaryTokenCount, original.SummaryTokenCount)
	}
}

// TestCompactionSummary_RoundTrip_ZeroValue documents the zero-value
// behaviour explicitly. Go's encoding/json emits nil slices as JSON null,
// and decoding JSON null into a []string yields nil (not an empty slice).
// We pin that behaviour here so a later refactor to `omitempty` or to
// empty-slice initialisation is a visible, reviewed change.
func TestCompactionSummary_RoundTrip_ZeroValue(t *testing.T) {
	t.Parallel()

	var original contextpkg.CompactionSummary

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Zero time renders as "0001-01-01T00:00:00Z"; nil slices render as null.
	want := `{"intent":"","key_decisions":null,"errors":null,"next_steps":null,` +
		`"files_to_restore":null,"compacted_at":"0001-01-01T00:00:00Z",` +
		`"original_token_count":0,"summary_token_count":0}`
	if string(data) != want {
		t.Errorf("zero-value JSON mismatch\n got: %s\nwant: %s", string(data), want)
	}

	var decoded contextpkg.CompactionSummary
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Intent != "" {
		t.Errorf("Intent: got %q want empty", decoded.Intent)
	}
	if decoded.KeyDecisions != nil {
		t.Errorf("KeyDecisions: got %v want nil", decoded.KeyDecisions)
	}
	if decoded.Errors != nil {
		t.Errorf("Errors: got %v want nil", decoded.Errors)
	}
	if decoded.NextSteps != nil {
		t.Errorf("NextSteps: got %v want nil", decoded.NextSteps)
	}
	if decoded.FilesToRestore != nil {
		t.Errorf("FilesToRestore: got %v want nil", decoded.FilesToRestore)
	}
	if !decoded.CompactedAt.IsZero() {
		t.Errorf("CompactedAt: got %v want zero", decoded.CompactedAt)
	}
	if decoded.OriginalTokenCount != 0 {
		t.Errorf("OriginalTokenCount: got %d want 0", decoded.OriginalTokenCount)
	}
	if decoded.SummaryTokenCount != 0 {
		t.Errorf("SummaryTokenCount: got %d want 0", decoded.SummaryTokenCount)
	}
}

// TestCompactionSummary_RoundTrip_EmptySlicesDistinctFromNil proves that
// explicitly-initialised empty slices round-trip as JSON arrays (not null)
// — this is the shape the summariser will actually produce when a range
// has, e.g., no errors. Pinning it prevents accidental regressions from
// future `omitempty` additions.
func TestCompactionSummary_RoundTrip_EmptySlicesDistinctFromNil(t *testing.T) {
	t.Parallel()

	original := contextpkg.CompactionSummary{
		Intent:         "nothing to decide",
		KeyDecisions:   []string{},
		Errors:         []string{},
		NextSteps:      []string{},
		FilesToRestore: []string{},
		CompactedAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Empty slices must appear as [] not null.
	for _, key := range []string{
		`"key_decisions":[]`,
		`"errors":[]`,
		`"next_steps":[]`,
		`"files_to_restore":[]`,
	} {
		if !strings.Contains(string(data), key) {
			t.Errorf("expected %s in JSON, got %s", key, string(data))
		}
	}

	var decoded contextpkg.CompactionSummary
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Decoded empty slices must be non-nil, length 0.
	for name, got := range map[string][]string{
		"KeyDecisions":   decoded.KeyDecisions,
		"Errors":         decoded.Errors,
		"NextSteps":      decoded.NextSteps,
		"FilesToRestore": decoded.FilesToRestore,
	} {
		if got == nil {
			t.Errorf("%s: decoded as nil, expected empty non-nil slice", name)
		}
		if len(got) != 0 {
			t.Errorf("%s: len=%d, expected 0", name, len(got))
		}
	}
}

// TestCompactionSummary_Unmarshal_ToleratesUnknownFields ensures that
// extra keys injected by an upstream (e.g. a newer summariser producing a
// forward-compatible extension) are silently ignored rather than rejected.
// Default encoding/json behaviour permits this; this test pins it so a
// future switch to Decoder.DisallowUnknownFields() is a visible change.
func TestCompactionSummary_Unmarshal_ToleratesUnknownFields(t *testing.T) {
	t.Parallel()

	input := `{
		"intent": "handle extra keys",
		"key_decisions": ["keep forward-compat"],
		"errors": [],
		"next_steps": [],
		"files_to_restore": [],
		"compacted_at": "2026-04-14T10:30:45Z",
		"original_token_count": 100,
		"summary_token_count": 25,
		"future_field_we_do_not_know": "ignored",
		"nested_unknown": {"anything": [1,2,3]}
	}`

	var decoded contextpkg.CompactionSummary
	if err := json.Unmarshal([]byte(input), &decoded); err != nil {
		t.Fatalf("unmarshal with unknown fields: %v", err)
	}

	if decoded.Intent != "handle extra keys" {
		t.Errorf("Intent: got %q want %q", decoded.Intent, "handle extra keys")
	}
	if len(decoded.KeyDecisions) != 1 || decoded.KeyDecisions[0] != "keep forward-compat" {
		t.Errorf("KeyDecisions: got %v", decoded.KeyDecisions)
	}
	if decoded.OriginalTokenCount != 100 {
		t.Errorf("OriginalTokenCount: got %d want 100", decoded.OriginalTokenCount)
	}
	if decoded.SummaryTokenCount != 25 {
		t.Errorf("SummaryTokenCount: got %d want 25", decoded.SummaryTokenCount)
	}
}

// TestCompactionSummary_Unmarshal_MalformedReturnsError asserts that
// malformed JSON yields an error rather than a panic or silent success.
func TestCompactionSummary_Unmarshal_MalformedReturnsError(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"truncated":        `{"intent": "oops"`,
		"not json":         `this is not json at all`,
		"wrong type int":   `{"original_token_count": "not-an-int"}`,
		"wrong type time":  `{"compacted_at": 12345}`,
		"wrong type slice": `{"key_decisions": "should-be-array"}`,
	}

	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var decoded contextpkg.CompactionSummary
			if err := json.Unmarshal([]byte(input), &decoded); err == nil {
				t.Errorf("expected error for %s, got nil", name)
			}
		})
	}
}

// equalStrings returns true when two string slices have identical length
// and contents in order.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
