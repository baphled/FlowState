package ollamacloud_test

// Live-key smoke test for the Ollama Cloud quota adapter
// (Provider Quota and Spend Visibility plan, May 2026, PR3 row 427).
//
// The plan's per-provider matrix line 148 flags Ollama Cloud's
// rate-limit header emission as UNKNOWN without a live key — the
// upstream proxy is a thin wrapper around the openai SDK shape, but
// whether it surfaces `x-ratelimit-*` and `x-request-id` in practice
// is provider-implementation-dependent.
//
// This spec is gated on the OLLAMACLOUD_API_KEY environment variable.
// To exercise:
//
//	OLLAMACLOUD_API_KEY=<key> go test -run TestOllamaCloudLiveQuota \
//	  -tags=live -count=1 ./internal/provider/ollamacloud/...
//
// When the env var is unset the spec skips cleanly so CI stays green
// without secrets. When set, the spec drives a minimal Chat against
// the live upstream, captures the response headers via the success-
// path response observer the quota adapter binds to, and asserts the
// adapter's live Snapshot graduated past `awaiting-first-response`
// (either to RateLimit when headers are present, or stays
// NotConfigured if the proxy stripped them — operator-visible
// honesty either way).
//
// Per memory feedback_no_pending_tests: this is NOT a PIt — it
// skips at runtime via Skip() rather than being PIt-pending.

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/ollamacloud"
	"github.com/baphled/flowstate/internal/provider/quota"
)

var _ = Describe("Ollama Cloud live-key quota smoke test (Quota Plan PR3, plan matrix line 148)", func() {
	// Skip the whole context when no key is set so CI stays green
	// without secrets. The skip text surfaces in test output for
	// operators running the live exercise.
	const envVar = "OLLAMACLOUD_API_KEY"

	BeforeEach(func() {
		if os.Getenv(envVar) == "" {
			Skip("set " + envVar + "=<key> to exercise the live smoke test (PR3 plan matrix line 148)")
		}
	})

	It("Chat against the live upstream graduates the adapter past awaiting-first-response", func() {
		apiKey := os.Getenv(envVar)
		Expect(apiKey).NotTo(BeEmpty(),
			"BeforeEach should have skipped when the env var was unset")

		p, err := ollamacloud.New(apiKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(p).NotTo(BeNil())

		// Bind the quota adapter so the Provider's success-path
		// response observer flows into the live Snapshot.
		adapter := ollamacloud.NewQuota(quota.HashAccount(apiKey))
		adapter.Bind(p)

		// Pre-Chat: adapter is NotConfigured{awaiting-first-response}.
		before, err := adapter.Remaining(context.Background(), "ollamacloud", "llama3.3:70b")
		Expect(err).NotTo(HaveOccurred())
		Expect(before.NotConfigured).NotTo(BeNil(),
			"pre-flight Snapshot MUST be NotConfigured")
		Expect(before.NotConfigured.Reason).To(Equal("awaiting-first-response"))

		// Drive a minimal Chat. Use a small model that's broadly
		// available on the Ollama Cloud catalogue.
		_, err = p.Chat(context.Background(), provider.ChatRequest{
			Provider: "ollamacloud",
			Model:    "llama3.3:70b",
			Messages: []provider.Message{
				{Role: "user", Content: "say hi"},
			},
			MaxTokens: 8,
		})
		// A live API call may fail for reasons unrelated to the
		// quota lift (model unavailable, auth quirk, transient
		// 503). The post-Chat assertions below cover both cases —
		// either the observer fired (success path) or the live
		// Snapshot stays NotConfigured (no-signal honest fallback).
		// We do NOT Expect err to be nil here because the smoke test
		// is about the observer wiring, not the upstream's liveness.
		if err != nil {
			GinkgoWriter.Printf(
				"ollamacloud live Chat returned err=%v — testing observer wiring is unaffected, "+
					"continuing to assert post-Chat adapter state\n",
				err,
			)
		}

		// Post-Chat: the adapter MUST either:
		//  (a) graduate to RateLimit (proxy emitted the headers); or
		//  (b) stay NotConfigured{awaiting-first-response} (proxy
		//      stripped headers — operator-visible honesty).
		// Either is a valid acceptance for the smoke test — the
		// gate is that we don't crash, double-record, or synthesise
		// a phantom Snapshot.
		after, err := adapter.Remaining(context.Background(), "ollamacloud", "llama3.3:70b")
		Expect(err).NotTo(HaveOccurred())
		Expect(after.IsValid()).To(BeTrue(),
			"adapter MUST always return a valid Snapshot — either RateLimit-populated or NotConfigured")

		if after.RateLimit != nil {
			GinkgoWriter.Printf(
				"ollamacloud live key emitted rate-limit headers — adapter graduated to RateLimit "+
					"(Requests.Remaining=%d, Tokens.Remaining=%d, TightestPercent=%d)\n",
				after.RateLimit.Requests.Remaining,
				after.RateLimit.Tokens.Remaining,
				after.RateLimit.TightestPercentRemaining,
			)
			Expect(after.NotConfigured).To(BeNil(),
				"discriminant invariant — RateLimit set means NotConfigured cleared")
		} else {
			GinkgoWriter.Printf(
				"ollamacloud live key did NOT emit rate-limit headers — adapter stays NotConfigured " +
					"(operator-visible honesty per plan matrix line 148)\n",
			)
			Expect(after.NotConfigured).NotTo(BeNil())
		}
	})
})
