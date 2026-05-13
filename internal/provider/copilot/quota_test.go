package copilot_test

import (
	"context"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/copilot"
	"github.com/baphled/flowstate/internal/provider/quota"
)

var _ = Describe("Copilot Quota stays NotConfigured permanently (Quota Plan PR3, plan matrix line 149)", func() {
	// Per plan line 149: GitHub Copilot is subscription-based — no
	// published rate-limit windows, no monthly token cap, no per-
	// request meter a downstream consumer can poll. The subscription
	// model doesn't fit either the RateLimit variant or the TokenSpend
	// variant; surfacing NotConfigured permanently is the honest stance.
	//
	// PR3 graduates openai/openzen/zai/ollamacloud to real adapters.
	// Copilot stays NotConfigured — this spec is the regression pin
	// against an accidental graduation.
	var adapter quota.Quota

	BeforeEach(func() {
		adapter = copilot.NewQuota(quota.HashAccount("test-key"))
	})

	It("ships NotConfigured{subscription-only}", func() {
		snap, err := adapter.Remaining(context.Background(), "copilot", "gpt-4o")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.IsValid()).To(BeTrue())
		Expect(snap.NotConfigured).NotTo(BeNil(),
			"Copilot MUST surface NotConfigured permanently — no subscription-only graduation")
		Expect(snap.NotConfigured.Reason).To(Equal("subscription-only"))
		Expect(snap.RateLimit).To(BeNil(),
			"discriminant invariant — NotConfigured set means RateLimit must be nil")
	})

	It("stays NotConfigured even when fed a populated openai-compat rate-limit header set", func() {
		// Even if the GitHub Copilot proxy started emitting OpenAI-
		// dialect rate-limit headers tomorrow, the adapter MUST stay
		// NotConfigured per plan line 149 — the subscription model
		// doesn't map to either variant, and operators have explicitly
		// chosen this surface area for honesty.
		h := http.Header{}
		h.Set("x-ratelimit-limit-requests", "100")
		h.Set("x-ratelimit-remaining-requests", "50")
		h.Set("x-ratelimit-reset-requests", "60s")
		h.Set("x-ratelimit-limit-tokens", "10000")
		h.Set("x-ratelimit-remaining-tokens", "5000")

		adapter.RecordResponse("copilot", "gpt-4o", h, provider.Usage{TotalTokens: 100})

		snap, _ := adapter.Remaining(context.Background(), "copilot", "gpt-4o")
		Expect(snap.NotConfigured).NotTo(BeNil(),
			"Copilot RecordResponse MUST be a no-op even with populated rate-limit headers — the NotConfiguredAdapter's RecordResponse is by design a no-op (notconfigured.go:Record... ignores everything)")
		Expect(snap.NotConfigured.Reason).To(Equal("subscription-only"))
		Expect(snap.RateLimit).To(BeNil(),
			"no accidental graduation — Copilot rate-limit headers are dropped on the floor")
	})
})
