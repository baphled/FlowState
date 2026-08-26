package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	anthropicAPI "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/quota"
)

// fullRateLimitHeaders builds an http.Header carrying all 14
// documented Anthropic rate-limit headers, so the success-path lift
// spec can assert every field populates.
func fullRateLimitHeaders(reset time.Time) http.Header {
	resetISO := reset.UTC().Format(time.RFC3339)
	h := http.Header{}
	h.Set("request-id", "req_success_path")
	h.Set("retry-after", "30")
	h.Set("anthropic-ratelimit-input-tokens-limit", "100000")
	h.Set("anthropic-ratelimit-input-tokens-remaining", "75000")
	h.Set("anthropic-ratelimit-input-tokens-reset", resetISO)
	h.Set("anthropic-ratelimit-output-tokens-limit", "20000")
	h.Set("anthropic-ratelimit-output-tokens-remaining", "18000")
	h.Set("anthropic-ratelimit-output-tokens-reset", resetISO)
	h.Set("anthropic-ratelimit-requests-limit", "1000")
	h.Set("anthropic-ratelimit-requests-remaining", "999")
	h.Set("anthropic-ratelimit-requests-reset", resetISO)
	h.Set("anthropic-ratelimit-tokens-limit", "120000")
	h.Set("anthropic-ratelimit-tokens-remaining", "118000")
	h.Set("anthropic-ratelimit-tokens-reset", resetISO)
	return h
}

var _ = Describe("Anthropic success-path RateLimit lift (Quota Plan PR1, plan lines 113-114, 425)", func() {
	Context("extractRateLimitHeadersFromResponse — the success-path-callable helper", func() {
		It("parses all 14 documented Anthropic rate-limit headers on a 2xx-style header set", func() {
			reset := time.Date(2026, 5, 13, 14, 0, 0, 0, time.UTC)
			rl := extractRateLimitHeadersFromResponse(fullRateLimitHeaders(reset), "")

			Expect(rl).NotTo(BeNil(),
				"a header set with all 14 rate-limit headers must produce a populated RateLimit")

			Expect(rl.RequestID).To(Equal("req_success_path"))
			Expect(rl.RetryAfter).To(Equal(30 * time.Second))

			Expect(rl.InputTokensLimit).To(Equal(100000))
			Expect(rl.InputTokensRemaining).To(Equal(75000))
			Expect(rl.InputTokensReset.Equal(reset)).To(BeTrue())

			Expect(rl.OutputTokensLimit).To(Equal(20000))
			Expect(rl.OutputTokensRemaining).To(Equal(18000))
			Expect(rl.OutputTokensReset.Equal(reset)).To(BeTrue())

			Expect(rl.RequestsLimit).To(Equal(1000))
			Expect(rl.RequestsRemaining).To(Equal(999))
			Expect(rl.RequestsReset.Equal(reset)).To(BeTrue())

			Expect(rl.TokensLimit).To(Equal(120000))
			Expect(rl.TokensRemaining).To(Equal(118000))
			Expect(rl.TokensReset.Equal(reset)).To(BeTrue())
		})

		It("returns nil when the response carries no rate-limit headers", func() {
			Expect(extractRateLimitHeadersFromResponse(http.Header{}, "")).To(BeNil())
		})

		It("returns nil when headers are absent and sdkRequestID is empty", func() {
			h := http.Header{}
			h.Set("content-type", "application/json")
			Expect(extractRateLimitHeadersFromResponse(h, "")).To(BeNil(),
				"a header set containing only non-rate-limit headers must produce nil")
		})

		It("populates only the fields the response actually carries (-1 sentinels for absent)", func() {
			h := http.Header{}
			h.Set("anthropic-ratelimit-requests-limit", "500")
			h.Set("anthropic-ratelimit-requests-remaining", "499")
			rl := extractRateLimitHeadersFromResponse(h, "")
			Expect(rl).NotTo(BeNil())
			Expect(rl.RequestsLimit).To(Equal(500))
			Expect(rl.RequestsRemaining).To(Equal(499))
			// Sibling windows stay at -1 sentinel.
			Expect(rl.InputTokensLimit).To(Equal(-1))
			Expect(rl.OutputTokensRemaining).To(Equal(-1))
			Expect(rl.TokensLimit).To(Equal(-1))
		})

		It("falls back to sdkRequestID when the response omits the request-id header (error-path parity)", func() {
			h := http.Header{}
			h.Set("anthropic-ratelimit-requests-limit", "1")
			h.Set("anthropic-ratelimit-requests-remaining", "0")
			rl := extractRateLimitHeadersFromResponse(h, "req_from_sdk_field")
			Expect(rl).NotTo(BeNil())
			Expect(rl.RequestID).To(Equal("req_from_sdk_field"),
				"sdkRequestID is the error-path fallback (apiErr.RequestID) — success path passes \"\" and the header alone populates")
		})
	})

	Context("Regression — existing error-path extractRateLimitHeaders behaviour unchanged", func() {
		It("returns nil for a nil apiErr (unchanged from prior behaviour)", func() {
			Expect(extractRateLimitHeaders(nil)).To(BeNil())
		})

		It("returns nil for an apiErr with no Response (unchanged)", func() {
			apiErr := &anthropicAPI.Error{}
			Expect(extractRateLimitHeaders(apiErr)).To(BeNil())
		})

		It("returns the same populated RateLimit the error path always produced (no regression)", func() {
			reset := time.Date(2026, 5, 13, 14, 0, 0, 0, time.UTC)
			apiErr := newTestAPIErrorWithHeaders(429, fullRateLimitHeaders(reset))
			apiErr.RequestID = "req_from_sdk"

			rl := extractRateLimitHeaders(apiErr)
			Expect(rl).NotTo(BeNil())
			// Header-set request-id wins over SDK request-id when both
			// present (existing scalar-header precedence at
			// readScalarHeaders).
			Expect(rl.RequestID).To(Equal("req_success_path"))
			Expect(rl.RequestsRemaining).To(Equal(999))
		})
	})
})

var _ = Describe("Anthropic Quota adapter (Quota Plan PR1, plan §Architecture lines 230)", func() {
	var (
		adapter *Quota
		ctx     context.Context
	)

	BeforeEach(func() {
		adapter = NewQuota("abc123def456")
		ctx = context.Background()
	})

	Context("NewQuota initial state", func() {
		It("starts with a NotConfigured Snapshot so Remaining never returns an invalid Snapshot pre-first-response", func() {
			snap, err := adapter.Remaining(ctx, "anthropic", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.IsValid()).To(BeTrue())
			Expect(snap.NotConfigured).NotTo(BeNil())
			Expect(snap.NotConfigured.Reason).To(Equal("awaiting-first-response"))
			Expect(snap.AccountHash).To(Equal("abc123def456"))
			Expect(snap.Provider).To(Equal("anthropic"))
			Expect(snap.Model).To(Equal("claude-opus-4-7"),
				"Remaining stamps the requested modelID into the returned Snapshot for audit")
		})

		It("conforms to the quota.Quota interface (compile-time check via package-level var _)", func() {
			// var _ quota.Quota = (*Quota)(nil) in quota.go enforces this
			// at compile time; this spec just documents the guarantee.
			var iface quota.Quota = adapter
			_ = iface
		})
	})

	Context("RecordResponse flips the variant to RateLimit", func() {
		It("parses rate-limit headers into a RateLimit variant on the first call", func() {
			reset := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
			adapter.RecordResponse("anthropic", "claude-opus-4-7", fullRateLimitHeaders(reset), provider.Usage{})

			snap, err := adapter.Remaining(ctx, "anthropic", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.IsValid()).To(BeTrue())
			Expect(snap.RateLimit).NotTo(BeNil(),
				"first successful RecordResponse with headers MUST flip the variant from NotConfigured to RateLimit — this is the load-bearing PR1 lift the chip depends on")
			Expect(snap.NotConfigured).To(BeNil(),
				"discriminant invariant — RateLimit set means NotConfigured cleared")

			Expect(snap.RateLimit.Requests.Limit).To(Equal(1000))
			Expect(snap.RateLimit.Requests.Remaining).To(Equal(999))
			Expect(snap.RateLimit.Output.Limit).To(Equal(20000))
		})

		It("computes TightestPercentRemaining as the minimum across non-sentinel windows", func() {
			h := http.Header{}
			// Requests: 999/1000 = 99%
			h.Set("anthropic-ratelimit-requests-limit", "1000")
			h.Set("anthropic-ratelimit-requests-remaining", "999")
			// Output tokens: 1000/20000 = 5% (this is the tightest)
			h.Set("anthropic-ratelimit-output-tokens-limit", "20000")
			h.Set("anthropic-ratelimit-output-tokens-remaining", "1000")
			h.Set("anthropic-ratelimit-output-tokens-reset", time.Now().Add(time.Hour).UTC().Format(time.RFC3339))

			adapter.RecordResponse("anthropic", "m", h, provider.Usage{})
			snap, _ := adapter.Remaining(ctx, "anthropic", "m")
			Expect(snap.RateLimit).NotTo(BeNil())
			Expect(snap.RateLimit.TightestPercentRemaining).To(Equal(5),
				"tightest window is Output at 1000/20000 = 5%; Requests at 99% must NOT be the tightest")
		})

		It("ignores responses with no rate-limit headers (keeps prior Snapshot — no-signal-no-regression)", func() {
			// Seed a populated Snapshot.
			adapter.RecordResponse("anthropic", "m", fullRateLimitHeaders(time.Now().Add(time.Hour)), provider.Usage{})
			snapBefore, _ := adapter.Remaining(ctx, "anthropic", "m")
			Expect(snapBefore.RateLimit).NotTo(BeNil())

			// Now record a header-less response (e.g. a custom proxy
			// that strips headers).
			adapter.RecordResponse("anthropic", "m", http.Header{}, provider.Usage{})
			snapAfter, _ := adapter.Remaining(ctx, "anthropic", "m")
			Expect(snapAfter.RateLimit).NotTo(BeNil(),
				"a header-less response MUST preserve the prior Snapshot rather than blanking — mirrors the error-path stance at anthropic_test.go:904-913")
		})

		It("ignores responses with only non-rate-limit headers", func() {
			h := http.Header{}
			h.Set("content-type", "application/json")
			h.Set("x-arbitrary", "noise")
			adapter.RecordResponse("anthropic", "m", h, provider.Usage{})
			snap, _ := adapter.Remaining(ctx, "anthropic", "m")
			// Stays NotConfigured (no first lift).
			Expect(snap.NotConfigured).NotTo(BeNil())
		})
	})

	Context("Stale-on-read (plan A2 fold, lines 396-397)", func() {
		It("flips Stale=true when TightestResetAt has passed", func() {
			h := http.Header{}
			h.Set("anthropic-ratelimit-requests-limit", "100")
			h.Set("anthropic-ratelimit-requests-remaining", "50")
			// Reset already in the past.
			h.Set("anthropic-ratelimit-requests-reset", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
			adapter.RecordResponse("anthropic", "m", h, provider.Usage{})

			snap, _ := adapter.Remaining(ctx, "anthropic", "m")
			Expect(snap.RateLimit).NotTo(BeNil())
			Expect(snap.Stale).To(BeTrue(),
				"a Snapshot whose TightestResetAt is in the past MUST be flagged Stale per plan A2 fold — chip continuity over the reset boundary")
		})

		It("keeps Stale=false when TightestResetAt is in the future", func() {
			adapter.RecordResponse("anthropic", "m", fullRateLimitHeaders(time.Now().Add(time.Hour)), provider.Usage{})
			snap, _ := adapter.Remaining(ctx, "anthropic", "m")
			Expect(snap.Stale).To(BeFalse())
		})
	})
})

var _ = Describe("Provider success-path observer wiring (Quota Plan PR1 — Chat + streamMessages)", func() {
	It("Chat invokes the response observer on a 2xx with the response headers", func() {
		// httptest server that returns a minimal valid Messages
		// response plus the success-path rate-limit headers.
		var resetTime time.Time
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resetTime = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
			w.Header().Set("content-type", "application/json")
			w.Header().Set("request-id", "req_chat_success_2xx")
			w.Header().Set("anthropic-ratelimit-requests-limit", "1000")
			w.Header().Set("anthropic-ratelimit-requests-remaining", "999")
			w.Header().Set("anthropic-ratelimit-requests-reset", resetTime.UTC().Format(time.RFC3339))
			w.WriteHeader(http.StatusOK)

			respBody := map[string]any{
				"id":            "msg_test_1",
				"type":          "message",
				"role":          "assistant",
				"model":         "claude-opus-4-7-20251031",
				"stop_reason":   "end_turn",
				"stop_sequence": nil,
				"content":       []map[string]any{{"type": "text", "text": "hello"}},
				"usage":         map[string]any{"input_tokens": 10, "output_tokens": 5},
			}
			_ = json.NewEncoder(w).Encode(respBody)
		}))
		defer server.Close()

		client := anthropicAPI.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		)
		p := &Provider{client: client}

		// Bind the observer — record headers seen.
		var seen http.Header
		p.SetResponseObserver(func(h http.Header) {
			seen = h
		})

		resp, err := p.Chat(context.Background(), provider.ChatRequest{
			Provider:  "anthropic",
			Model:     "claude-opus-4-7-20251031",
			Messages:  []provider.Message{{Role: "user", Content: "hi"}},
			MaxTokens: 128,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Message.Content).To(Equal("hello"))

		Expect(seen).NotTo(BeNil(),
			"Chat MUST invoke the response observer on a 2xx — this is the success-path lift PR1 ships")
		Expect(seen.Get("request-id")).To(Equal("req_chat_success_2xx"))
		Expect(seen.Get("anthropic-ratelimit-requests-remaining")).To(Equal("999"),
			"observer MUST receive the rate-limit headers so the Quota adapter can parse them")
	})

	It("Chat does NOT invoke the observer on a 5xx error (error path preserved)", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// 500 with NO rate-limit headers and NO request-id.
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"boom"}}`))
		}))
		defer server.Close()

		client := anthropicAPI.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
			option.WithMaxRetries(0),
		)
		p := &Provider{client: client}

		observerCalled := false
		p.SetResponseObserver(func(http.Header) {
			observerCalled = true
		})

		_, err := p.Chat(context.Background(), provider.ChatRequest{
			Provider:  "anthropic",
			Model:     "claude-opus-4-7-20251031",
			Messages:  []provider.Message{{Role: "user", Content: "hi"}},
			MaxTokens: 128,
		})
		Expect(err).To(HaveOccurred(),
			"5xx must surface as an error from Chat (error-path contract unchanged)")
		Expect(observerCalled).To(BeFalse(),
			"observer MUST NOT fire on the error path — the existing error-path RateLimit extraction at anthropic.go:680 owns 4xx/5xx; this guarantee prevents double-recording when both paths run")
	})
})

var _ = Describe("Quota.Bind end-to-end (observer hook + Quota adapter)", func() {
	It("wires the Quota adapter to a Provider's response observer, so Chat 2xx populates the live Snapshot", func() {
		reset := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("content-type", "application/json")
			w.Header().Set("request-id", "req_bind_e2e")
			w.Header().Set("anthropic-ratelimit-requests-limit", "1000")
			w.Header().Set("anthropic-ratelimit-requests-remaining", "750")
			w.Header().Set("anthropic-ratelimit-requests-reset", reset.UTC().Format(time.RFC3339))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"id": "msg_bind_e2e",
				"type": "message",
				"role": "assistant",
				"model": "claude-opus-4-7-20251031",
				"stop_reason": "end_turn",
				"content": [{"type":"text","text":"ok"}],
				"usage": {"input_tokens": 1, "output_tokens": 1}
			}`))
		}))
		defer server.Close()

		client := anthropicAPI.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		)
		p := &Provider{client: client}

		// Bind the adapter. Pre-Chat: Snapshot is NotConfigured.
		adapter := NewQuota(quota.HashAccount("test-key"))
		adapter.Bind(p)

		before, _ := adapter.Remaining(context.Background(), "anthropic", "claude-opus-4-7-20251031")
		Expect(before.NotConfigured).NotTo(BeNil())

		// Drive a Chat — observer fires, adapter flips variant.
		_, err := p.Chat(context.Background(), provider.ChatRequest{
			Provider:  "anthropic",
			Model:     "claude-opus-4-7-20251031",
			Messages:  []provider.Message{{Role: "user", Content: "hi"}},
			MaxTokens: 128,
		})
		Expect(err).NotTo(HaveOccurred())

		after, err := adapter.Remaining(context.Background(), "anthropic", "claude-opus-4-7-20251031")
		Expect(err).NotTo(HaveOccurred())
		Expect(after.IsValid()).To(BeTrue())
		Expect(after.RateLimit).NotTo(BeNil(),
			"Bind + Chat 2xx end-to-end MUST flip the live Snapshot from NotConfigured to RateLimit — this is the user-visible PR1 acceptance")
		Expect(after.RateLimit.Requests.Remaining).To(Equal(750))
		Expect(after.RateLimit.TightestPercentRemaining).To(Equal(75),
			"single window 750/1000 = 75% tightest")
		Expect(after.AccountHash).To(Equal(quota.HashAccount("test-key")),
			"Snapshot carries the bound account hash so callers can partition")
	})
})
