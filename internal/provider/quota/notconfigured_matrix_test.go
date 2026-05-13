package quota_test

import (
	"context"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/copilot"
	"github.com/baphled/flowstate/internal/provider/ollama"
	"github.com/baphled/flowstate/internal/provider/quota"
)

var _ = Describe("Per-provider quota fidelity matrix (plan lines 141-149)", func() {
	// One row per provider that ships NotConfigured **permanently**.
	//
	// History:
	//   - PR1 seeded six rows: anthropic (lifted to RateLimit by
	//     PR1 itself); openai/openzen/zai/ollamacloud (placeholder
	//     "awaiting-pr3" until PR3 lifts the openaicompat shared
	//     parser to the success path); ollama (local, no quota);
	//     copilot (subscription, no public meter).
	//   - PR3 (this slice, May 2026) lifts the four openai-compat-
	//     routed providers to real RateLimit adapters — their pinning
	//     moves out of this matrix into per-package quota_test.go
	//     suites (openai/quota_test.go, openzen/quota_test.go,
	//     zai/quota_test.go, ollamacloud/quota_test.go), each
	//     mirroring the anthropic/quota_test.go shape.
	//   - This matrix now pins only the two PERMANENT NotConfigured
	//     adapters: ollama (local model, no rate-limit concept) and
	//     copilot (subscription-only, no per-request meter — plan
	//     line 149).
	//
	// Behaviour pin: any future regression that flips ollama or
	// copilot to a "real" adapter must explicitly remove the row here
	// and add a new per-package suite — silent drift is impossible.
	type matrixRow struct {
		providerID     string
		expectedReason string
		newQuota       func(string) quota.Quota
	}

	rows := []matrixRow{
		// ollama is local — no quota concept ever.
		{"ollama", "local-model", ollama.NewQuota},
		// copilot is subscription-only — no per-request meter.
		{"copilot", "subscription-only", copilot.NewQuota},
	}

	for _, row := range rows {
		row := row // closure capture

		Context(row.providerID, func() {
			var adapter quota.Quota

			BeforeEach(func() {
				adapter = row.newQuota(quota.HashAccount("test-key"))
			})

			It("ships NotConfigured with the matrix-mandated reason", func() {
				snap, err := adapter.Remaining(context.Background(), row.providerID, "any-model")
				Expect(err).NotTo(HaveOccurred())
				Expect(snap.IsValid()).To(BeTrue())
				Expect(snap.NotConfigured).NotTo(BeNil(),
					"%s MUST surface NotConfigured per plan per-provider matrix (lines 141-149)", row.providerID)
				Expect(snap.NotConfigured.Reason).To(Equal(row.expectedReason),
					"%s reason MUST match plan matrix exactly — the chip tooltip surfaces this string verbatim", row.providerID)
				Expect(snap.Provider).To(Equal(row.providerID))
			})

			It("RecordResponse is a no-op (NotConfigured providers emit no quota signal)", func() {
				// Should not panic, should not change the Snapshot.
				h := http.Header{}
				h.Set("x-ratelimit-remaining", "ignored-by-not-configured-adapter")
				adapter.RecordResponse(row.providerID, "any-model", h, provider.Usage{TotalTokens: 999})

				snap, _ := adapter.Remaining(context.Background(), row.providerID, "any-model")
				Expect(snap.NotConfigured).NotTo(BeNil(),
					"RecordResponse on a NotConfigured adapter MUST be a no-op — variant stays NotConfigured")
				Expect(snap.NotConfigured.Reason).To(Equal(row.expectedReason))
			})
		})
	}
})
