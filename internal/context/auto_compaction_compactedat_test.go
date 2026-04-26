package context_test

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	contextpkg "github.com/baphled/flowstate/internal/context"
)

// Regression tests for the `compacted_at` JSON round-trip bug surfaced
// on 2026-04-14.
//
// Before the fix: the T8 summary prompt instructed the LLM to emit the
// literal string "PLACEHOLDER_COMPACTED_AT" as the value of the
// `compacted_at` field. CompactionSummary.CompactedAt is a time.Time, so
// json.Unmarshal failed on every real summariser response and
// flowstate_compression_tokens_saved_total never advanced in production.
//
// After the fix: the prompt no longer references `compacted_at`, and
// AutoCompactor.Compact server-stamps the field with time.Now().UTC()
// after a successful unmarshal. C3 made the field server-only
// (json:"-"), so any LLM-emitted value — placeholder, RFC3339, or
// garbage — is silently discarded at unmarshal time.
var _ = Describe("AutoCompactor.Compact CompactedAt server-stamping", func() {
	expectServerStamped := func(stamped time.Time, before, after time.Time) {
		Expect(stamped.IsZero()).To(BeFalse(), "CompactedAt is zero; server stamp did not run")
		Expect(stamped.Before(before)).To(BeFalse(),
			"CompactedAt %v is before %v", stamped, before)
		Expect(stamped.After(after)).To(BeFalse(),
			"CompactedAt %v is after %v", stamped, after)
		Expect(stamped.Location()).To(Equal(time.UTC))
	}

	It("stamps CompactedAt from wall-clock UTC when the schema omits compacted_at", func() {
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
		Expect(err).NotTo(HaveOccurred())

		expectServerStamped(summary.CompactedAt, before, after)
	})

	It("ignores a legacy PLACEHOLDER_COMPACTED_AT string and still server-stamps", func() {
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
		Expect(err).NotTo(HaveOccurred(),
			"legacy placeholder must now be ignored, not rejected")

		expectServerStamped(summary.CompactedAt, before, after)
	})

	It("ignores a well-formed RFC3339 LLM-emitted compacted_at and still server-stamps", func() {
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
		Expect(err).NotTo(HaveOccurred())

		// The LLM-emitted value (2024-01-01) must never survive.
		llmEmitted := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		Expect(summary.CompactedAt.Equal(llmEmitted)).To(BeFalse(),
			"LLM-emitted compacted_at leaked through unmarshal: %v", summary.CompactedAt)

		expectServerStamped(summary.CompactedAt, before, after)
	})

	It("never serialises CompactedAt: the JSON output omits the compacted_at key entirely", func() {
		summary := contextpkg.CompactionSummary{
			Intent:      "test field visibility",
			NextSteps:   []string{"verify"},
			CompactedAt: time.Date(2026, 4, 14, 10, 30, 45, 0, time.UTC),
		}

		data, err := json.Marshal(summary)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).NotTo(ContainSubstring("compacted_at"),
			"CompactionSummary JSON still contains `compacted_at` key")
		Expect(string(data)).NotTo(ContainSubstring("2026-04-14"),
			"CompactionSummary JSON leaks the CompactedAt timestamp")
	})

	It("RenderSummaryPrompt no longer references compacted_at or PLACEHOLDER_COMPACTED_AT", func() {
		out, err := contextpkg.RenderSummaryPrompt(sampleMessages())
		Expect(err).NotTo(HaveOccurred())
		Expect(out).NotTo(ContainSubstring("compacted_at"),
			"prompt still references `compacted_at`; LLM will keep emitting it")
		Expect(out).NotTo(ContainSubstring("PLACEHOLDER_COMPACTED_AT"),
			"prompt still contains legacy PLACEHOLDER_COMPACTED_AT directive")
	})
})
