package openzen

import (
	"context"
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

// fullRateLimitHeaders — same shape as openai/quota_test.go.
func fullRateLimitHeaders() http.Header {
	h := http.Header{}
	h.Set("x-request-id", "req_openzen_success")
	h.Set("x-ratelimit-limit-requests", "500")
	h.Set("x-ratelimit-remaining-requests", "400")
	h.Set("x-ratelimit-reset-requests", "5s")
	h.Set("x-ratelimit-limit-tokens", "200000")
	h.Set("x-ratelimit-remaining-tokens", "150000")
	h.Set("x-ratelimit-reset-tokens", "60s")
	return h
}

var _ = Describe("OpenZen Quota adapter (Quota Plan PR3, plan matrix line 145)", func() {
	var adapter *Quota

	BeforeEach(func() {
		adapter = NewQuota(quota.HashAccount("test-key"))
	})

	It("starts with NotConfigured{awaiting-first-response} and flips on first 2xx RecordResponse", func() {
		snap, err := adapter.Remaining(context.Background(), providerName, "claude-sonnet-4-5")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.NotConfigured).NotTo(BeNil())
		Expect(snap.NotConfigured.Reason).To(Equal("awaiting-first-response"))
		Expect(snap.Provider).To(Equal(providerName))

		adapter.RecordResponse(providerName, "claude-sonnet-4-5", fullRateLimitHeaders(), provider.Usage{})
		after, _ := adapter.Remaining(context.Background(), providerName, "claude-sonnet-4-5")
		Expect(after.RateLimit).NotTo(BeNil(),
			"OpenZen PR3 graduates from awaiting-pr3 → real RateLimit adapter")
		Expect(after.RateLimit.Requests.Remaining).To(Equal(400))
		Expect(after.RateLimit.Tokens.Remaining).To(Equal(150000))
	})

	It("Bind end-to-end: Chat 2xx populates the live Snapshot", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			for k, vs := range fullRateLimitHeaders() {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl-openzen-bind",
				"object":"chat.completion",
				"created":1700000000,
				"model":"claude-sonnet-4-5",
				"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
			}`))
		}))
		defer server.Close()

		client := openaiAPI.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		)
		p := &Provider{client: client}

		adapter.Bind(p)
		_, err := p.Chat(context.Background(), provider.ChatRequest{
			Provider: providerName,
			Model:    "claude-sonnet-4-5",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		Expect(err).NotTo(HaveOccurred())

		snap, _ := adapter.Remaining(context.Background(), providerName, "claude-sonnet-4-5")
		Expect(snap.RateLimit).NotTo(BeNil())
		Expect(snap.RateLimit.Requests.Remaining).To(Equal(400))
	})

	It("Observer does NOT fire on a 5xx (error-path stays sole owner)", func() {
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
		called := false
		p.SetResponseObserver(func(http.Header) { called = true })

		_, err := p.Chat(context.Background(), provider.ChatRequest{
			Provider: providerName,
			Model:    "claude-sonnet-4-5",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		Expect(err).To(HaveOccurred())
		Expect(called).To(BeFalse(),
			"5xx must not invoke the success-path observer")
	})

	It("conforms to the quota.Quota interface", func() {
		var iface quota.Quota = adapter
		_ = iface
	})

	It("Stale-on-read past TightestResetAt", func() {
		h := http.Header{}
		h.Set("x-ratelimit-limit-requests", "10")
		h.Set("x-ratelimit-remaining-requests", "5")
		h.Set("x-ratelimit-reset-requests", "0s")
		adapter.RecordResponse(providerName, "m", h, provider.Usage{})
		time.Sleep(10 * time.Millisecond)

		snap, _ := adapter.Remaining(context.Background(), providerName, "m")
		Expect(snap.RateLimit).NotTo(BeNil())
		Expect(snap.Stale).To(BeTrue())
	})
})
