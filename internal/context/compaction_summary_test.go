package context_test

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	contextpkg "github.com/baphled/flowstate/internal/context"
)

// CompactionSummary specs lock in the JSON wire-shape contract so an
// upstream LLM, an on-disk replay log, and downstream subscribers all
// agree on the schema.
//
// Highlights:
//   - Round-trip preserves every field except CompactedAt, which is
//     server-only (json:"-") per C3 — the engine stamps it after
//     unmarshal. Any LLM-emitted compacted_at value is silently ignored
//     (regression covered in auto_compaction_compactedat_test).
//   - Empty slices encode as [] (not null); nil slices encode as null.
//     Pinning both shapes prevents accidental omitempty/init regressions.
//   - Forward-compat: unknown JSON keys are silently ignored. Switching
//     to DisallowUnknownFields would be a visible breaking change.
//   - Malformed JSON returns an error rather than panicking.
var _ = Describe("CompactionSummary JSON wire contract", func() {
	It("round-trips every field except the server-only CompactedAt", func() {
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
		Expect(err).NotTo(HaveOccurred())

		var decoded contextpkg.CompactionSummary
		Expect(json.Unmarshal(data, &decoded)).To(Succeed())

		Expect(decoded.Intent).To(Equal(original.Intent))
		Expect(decoded.KeyDecisions).To(Equal(original.KeyDecisions))
		Expect(decoded.Errors).To(Equal(original.Errors))
		Expect(decoded.NextSteps).To(Equal(original.NextSteps))
		Expect(decoded.FilesToRestore).To(Equal(original.FilesToRestore))
		// CompactedAt is server-only per C3 — must NOT survive the
		// JSON round-trip.
		Expect(decoded.CompactedAt.IsZero()).To(BeTrue(),
			"CompactedAt = %v; expected zero (server-only field)", decoded.CompactedAt)
		Expect(decoded.OriginalTokenCount).To(Equal(original.OriginalTokenCount))
		Expect(decoded.SummaryTokenCount).To(Equal(original.SummaryTokenCount))
	})

	It("zero-value round-trip emits null for nil slices and decodes back to nil", func() {
		var original contextpkg.CompactionSummary

		data, err := json.Marshal(original)
		Expect(err).NotTo(HaveOccurred())

		want := `{"intent":"","key_decisions":null,"errors":null,"next_steps":null,` +
			`"files_to_restore":null,` +
			`"original_token_count":0,"summary_token_count":0}`
		Expect(string(data)).To(Equal(want))

		var decoded contextpkg.CompactionSummary
		Expect(json.Unmarshal(data, &decoded)).To(Succeed())

		Expect(decoded.Intent).To(BeEmpty())
		Expect(decoded.KeyDecisions).To(BeNil())
		Expect(decoded.Errors).To(BeNil())
		Expect(decoded.NextSteps).To(BeNil())
		Expect(decoded.FilesToRestore).To(BeNil())
		Expect(decoded.CompactedAt.IsZero()).To(BeTrue())
		Expect(decoded.OriginalTokenCount).To(Equal(0))
		Expect(decoded.SummaryTokenCount).To(Equal(0))
	})

	It("empty (non-nil) slices encode as [] and decode back as empty non-nil slices", func() {
		original := contextpkg.CompactionSummary{
			Intent:         "nothing to decide",
			KeyDecisions:   []string{},
			Errors:         []string{},
			NextSteps:      []string{},
			FilesToRestore: []string{},
			CompactedAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		}

		data, err := json.Marshal(original)
		Expect(err).NotTo(HaveOccurred())

		// Empty slices must appear as [] not null.
		for _, key := range []string{
			`"key_decisions":[]`,
			`"errors":[]`,
			`"next_steps":[]`,
			`"files_to_restore":[]`,
		} {
			Expect(string(data)).To(ContainSubstring(key))
		}

		var decoded contextpkg.CompactionSummary
		Expect(json.Unmarshal(data, &decoded)).To(Succeed())

		// Decoded empty slices must be non-nil, length 0.
		for name, got := range map[string][]string{
			"KeyDecisions":   decoded.KeyDecisions,
			"Errors":         decoded.Errors,
			"NextSteps":      decoded.NextSteps,
			"FilesToRestore": decoded.FilesToRestore,
		} {
			Expect(got).NotTo(BeNil(), "%s decoded as nil; expected empty non-nil slice", name)
			Expect(got).To(BeEmpty(), "%s len mismatch", name)
		}
	})

	It("Unmarshal silently ignores unknown JSON keys (forward-compat)", func() {
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
		Expect(json.Unmarshal([]byte(input), &decoded)).To(Succeed())

		Expect(decoded.Intent).To(Equal("handle extra keys"))
		Expect(decoded.KeyDecisions).To(Equal([]string{"keep forward-compat"}))
		Expect(decoded.OriginalTokenCount).To(Equal(100))
		Expect(decoded.SummaryTokenCount).To(Equal(25))
	})

	DescribeTable("Unmarshal returns an error on malformed JSON",
		func(input string) {
			var decoded contextpkg.CompactionSummary
			Expect(json.Unmarshal([]byte(input), &decoded)).To(HaveOccurred())
		},
		Entry("truncated", `{"intent": "oops"`),
		Entry("not json", `this is not json at all`),
		Entry("wrong type int", `{"original_token_count": "not-an-int"}`),
		Entry("wrong type slice", `{"key_decisions": "should-be-array"}`),
		// compacted_at wrong-type case removed: per C3 the field is
		// server-only (json:"-"), so any emitted value — well-formed or
		// not — is silently ignored at unmarshal time. Regression
		// coverage for that ignore-semantic lives in
		// TestAutoCompactor_Compact_LLMEmittedValidRFC3339_IsIgnored.
	)
})
