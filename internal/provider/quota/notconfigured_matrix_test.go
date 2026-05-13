package quota_test

import (
	"context"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/copilot"
	"github.com/baphled/flowstate/internal/provider/ollama"
	"github.com/baphled/flowstate/internal/provider/ollamacloud"
	"github.com/baphled/flowstate/internal/provider/openai"
	"github.com/baphled/flowstate/internal/provider/openzen"
	"github.com/baphled/flowstate/internal/provider/quota"
	"github.com/baphled/flowstate/internal/provider/zai"
)

var _ = Describe("Per-provider quota fidelity matrix (plan lines 141-149)", func() {
	// One row per provider that ships NotConfigured in PR1.
	// Anthropic is NOT in this matrix because PR1 ships the
	// success-path RateLimit lift for it — see anthropic/quota_test.go.
	type matrixRow struct {
		providerID     string
		expectedReason string
		newQuota       func(string) quota.Quota
	}

	rows := []matrixRow{
		// openai/openzen/zai/ollamacloud are openaicompat-inheriting
		// providers blocked on PR3's success-path lift.
		{"openai", "awaiting-pr3", openai.NewQuota},
		{"openzen", "awaiting-pr3", openzen.NewQuota},
		{"zai", "awaiting-pr3", zai.NewQuota},
		{"ollamacloud", "awaiting-pr3", ollamacloud.NewQuota},
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
