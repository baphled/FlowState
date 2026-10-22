package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/quota"
)

// fullRateLimitHeaders builds an http.Header carrying the six
// documented openai-compat rate-limit headers + scalar request-id /
// retry-after, so the success-path lift spec can assert every field
// populates.
func fullRateLimitHeaders() http.Header {
	h := http.Header{}
	h.Set("x-request-id", "req_openai_success")
	h.Set("retry-after", "30")
	h.Set("x-ratelimit-limit-requests", "1000")
	h.Set("x-ratelimit-remaining-requests", "999")
	h.Set("x-ratelimit-reset-requests", "1s")
	h.Set("x-ratelimit-limit-tokens", "100000")
	h.Set("x-ratelimit-remaining-tokens", "75000")
	h.Set("x-ratelimit-reset-tokens", "60s")
	return h
}

var _ = Describe("OpenAI Quota adapter (Quota Plan PR3, plan §Architecture line 230 + matrix line 144)", func() {
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
			snap, err := adapter.Remaining(ctx, "openai", "gpt-4o")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.IsValid()).To(BeTrue())
			Expect(snap.NotConfigured).NotTo(BeNil())
			Expect(snap.NotConfigured.Reason).To(Equal("awaiting-first-response"),
				"PR3 graduates openai from awaiting-pr3 → awaiting-first-response (mirror of anthropic/quota.go)")
			Expect(snap.AccountHash).To(Equal("abc123def456"))
			Expect(snap.Provider).To(Equal("openai"))
			Expect(snap.Model).To(Equal("gpt-4o"),
				"Remaining stamps the requested modelID into the returned Snapshot for audit")
		})

		It("conforms to the quota.Quota interface (compile-time check via package-level var _)", func() {
			var iface quota.Quota = adapter
			_ = iface
		})
	})

	Context("RecordResponse flips the variant to RateLimit", func() {
		It("parses rate-limit headers into a RateLimit variant on the first call", func() {
			adapter.RecordResponse("openai", "gpt-4o", fullRateLimitHeaders(), provider.Usage{})

			snap, err := adapter.Remaining(ctx, "openai", "gpt-4o")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.IsValid()).To(BeTrue())
			Expect(snap.RateLimit).NotTo(BeNil(),
				"first successful RecordResponse with headers MUST flip the variant from NotConfigured to RateLimit — this is the load-bearing PR3 lift the chip depends on")
			Expect(snap.NotConfigured).To(BeNil(),
				"discriminant invariant — RateLimit set means NotConfigured cleared")

			Expect(snap.RateLimit.Requests.Limit).To(Equal(1000))
			Expect(snap.RateLimit.Requests.Remaining).To(Equal(999))
			Expect(snap.RateLimit.Tokens.Limit).To(Equal(100000))
			Expect(snap.RateLimit.Tokens.Remaining).To(Equal(75000))
		})

		It("computes TightestPercentRemaining as the minimum across non-sentinel windows", func() {
			h := http.Header{}
			// Requests: 999/1000 = 99%
			h.Set("x-ratelimit-limit-requests", "1000")
			h.Set("x-ratelimit-remaining-requests", "999")
			h.Set("x-ratelimit-reset-requests", "1s")
			// Tokens: 1000/100000 = 1% (this is the tightest)
			h.Set("x-ratelimit-limit-tokens", "100000")
			h.Set("x-ratelimit-remaining-tokens", "1000")
			h.Set("x-ratelimit-reset-tokens", "60s")

			adapter.RecordResponse("openai", "gpt-4o", h, provider.Usage{})
			snap, _ := adapter.Remaining(ctx, "openai", "gpt-4o")
			Expect(snap.RateLimit).NotTo(BeNil())
			Expect(snap.RateLimit.TightestPercentRemaining).To(Equal(1),
				"tightest window is Tokens at 1000/100000 = 1%; Requests at 99%% must NOT be the tightest")
		})

		It("ignores responses with no rate-limit headers (keeps prior Snapshot — no-signal-no-regression)", func() {
			// Seed a populated Snapshot.
			adapter.RecordResponse("openai", "gpt-4o", fullRateLimitHeaders(), provider.Usage{})
			snapBefore, _ := adapter.Remaining(ctx, "openai", "gpt-4o")
			Expect(snapBefore.RateLimit).NotTo(BeNil())

			// Now record a header-less response (e.g. a custom proxy
			// that strips headers).
			adapter.RecordResponse("openai", "gpt-4o", http.Header{}, provider.Usage{})
			snapAfter, _ := adapter.Remaining(ctx, "openai", "gpt-4o")
			Expect(snapAfter.RateLimit).NotTo(BeNil(),
				"a header-less response MUST preserve the prior Snapshot rather than blanking — mirrors anthropic adapter")
		})

		It("ignores responses with only non-rate-limit headers", func() {
			h := http.Header{}
			h.Set("content-type", "application/json")
			h.Set("x-arbitrary", "noise")
			adapter.RecordResponse("openai", "gpt-4o", h, provider.Usage{})
			snap, _ := adapter.Remaining(ctx, "openai", "gpt-4o")
			Expect(snap.NotConfigured).NotTo(BeNil(),
				"stays NotConfigured (no first lift)")
		})
	})

	Context("Stale-on-read (plan A2 fold, lines 396-397)", func() {
		It("flips Stale=true when TightestResetAt has passed", func() {
			h := http.Header{}
			h.Set("x-ratelimit-limit-requests", "100")
			h.Set("x-ratelimit-remaining-requests", "50")
			// Reset is "0s" — already-in-the-past at read time.
			h.Set("x-ratelimit-reset-requests", "0s")
			adapter.RecordResponse("openai", "gpt-4o", h, provider.Usage{})
			// Give the wall-clock a beat past the reset.
			time.Sleep(10 * time.Millisecond)

			snap, _ := adapter.Remaining(ctx, "openai", "gpt-4o")
			Expect(snap.RateLimit).NotTo(BeNil())
			Expect(snap.Stale).To(BeTrue(),
				"a Snapshot whose TightestResetAt is in the past MUST be flagged Stale per plan A2 fold")
		})

		It("keeps Stale=false when TightestResetAt is in the future", func() {
			adapter.RecordResponse("openai", "gpt-4o", fullRateLimitHeaders(), provider.Usage{})
			snap, _ := adapter.Remaining(ctx, "openai", "gpt-4o")
			Expect(snap.Stale).To(BeFalse())
		})
	})
})

var _ = Describe("OpenAI Provider success-path observer wiring (Quota Plan PR3 — Chat + Stream)", func() {
	It("Chat invokes the response observer on a 2xx with the response headers", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			for k, vs := range fullRateLimitHeaders() {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusOK)
			respBody := map[string]any{
				"id":      "chatcmpl-success",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   "gpt-4o",
				"choices": []map[string]any{{
					"index":         0,
					"message":       map[string]any{"role": "assistant", "content": "hello"},
					"finish_reason": "stop",
				}},
				"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6},
			}
			_ = json.NewEncoder(w).Encode(respBody)
		}))
		defer server.Close()

		client := openaiAPI.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		)
		p := &Provider{client: client}

		var seen http.Header
		p.SetResponseObserver(func(h http.Header) {
			seen = h
		})

		resp, err := p.Chat(context.Background(), provider.ChatRequest{
			Provider: "openai",
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Message.Content).To(Equal("hello"))

		Expect(seen).NotTo(BeNil(),
			"Chat MUST invoke the response observer on a 2xx — this is the success-path lift PR3 ships")
		Expect(seen.Get("x-request-id")).To(Equal("req_openai_success"))
		Expect(seen.Get("x-ratelimit-remaining-requests")).To(Equal("999"))
	})

	It("Chat does NOT invoke the observer on a 5xx error (error-path stays sole owner)", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"boom","type":"server_error"}}`))
		}))
		defer server.Close()

		client := openaiAPI.NewClient(
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
			Provider: "openai",
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		Expect(err).To(HaveOccurred(),
			"5xx must surface as an error from Chat (error-path contract unchanged)")
		Expect(observerCalled).To(BeFalse(),
			"observer MUST NOT fire on the error path — preserves the error-path RateLimit extraction's sole ownership of 4xx/5xx")
	})

	It("Chat WITHOUT a bound observer still flows the 2xx body unchanged (no observer means no WithResponseInto overhead)", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusOK)
			respBody := map[string]any{
				"id":      "chatcmpl-nullobs",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   "gpt-4o",
				"choices": []map[string]any{{
					"index":         0,
					"message":       map[string]any{"role": "assistant", "content": "ok"},
					"finish_reason": "stop",
				}},
				"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
			}
			_ = json.NewEncoder(w).Encode(respBody)
		}))
		defer server.Close()

		client := openaiAPI.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		)
		p := &Provider{client: client} // no observer bound

		resp, err := p.Chat(context.Background(), provider.ChatRequest{
			Provider: "openai",
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Message.Content).To(Equal("ok"),
			"Chat with no observer bound MUST flow unchanged — nil-observer is the back-compat path")
	})
})

var _ = Describe("OpenAI Quota.Bind end-to-end (observer hook + Quota adapter)", func() {
	It("wires the Quota adapter to a Provider's response observer, so Chat 2xx populates the live Snapshot", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("content-type", "application/json")
			w.Header().Set("x-request-id", "req_openai_bind_e2e")
			w.Header().Set("x-ratelimit-limit-requests", "1000")
			w.Header().Set("x-ratelimit-remaining-requests", "750")
			w.Header().Set("x-ratelimit-reset-requests", "60s")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"id": "chatcmpl-bind-e2e",
				"object": "chat.completion",
				"created": 1700000000,
				"model": "gpt-4o",
				"choices": [{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
				"usage": {"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
			}`))
		}))
		defer server.Close()

		client := openaiAPI.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		)
		p := &Provider{client: client}

		adapter := NewQuota(quota.HashAccount("test-key"))
		adapter.Bind(p)

		before, _ := adapter.Remaining(context.Background(), "openai", "gpt-4o")
		Expect(before.NotConfigured).NotTo(BeNil())

		_, err := p.Chat(context.Background(), provider.ChatRequest{
			Provider: "openai",
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		Expect(err).NotTo(HaveOccurred())

		after, err := adapter.Remaining(context.Background(), "openai", "gpt-4o")
		Expect(err).NotTo(HaveOccurred())
		Expect(after.IsValid()).To(BeTrue())
		Expect(after.RateLimit).NotTo(BeNil(),
			"Bind + Chat 2xx end-to-end MUST flip the live Snapshot from NotConfigured to RateLimit — this is the user-visible PR3 acceptance")
		Expect(after.RateLimit.Requests.Remaining).To(Equal(750))
		Expect(after.RateLimit.TightestPercentRemaining).To(Equal(75),
			"single window 750/1000 = 75%% tightest")
		Expect(after.AccountHash).To(Equal(quota.HashAccount("test-key")),
			"Snapshot carries the bound account hash so callers can partition")
	})
})
