package ollamacloud

import (
	"context"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/quota"
)

func fullRateLimitHeaders() http.Header {
	h := http.Header{}
	h.Set("x-request-id", "req_ollamacloud_success")
	h.Set("x-ratelimit-limit-requests", "100")
	h.Set("x-ratelimit-remaining-requests", "90")
	h.Set("x-ratelimit-reset-requests", "30s")
	h.Set("x-ratelimit-limit-tokens", "100000")
	h.Set("x-ratelimit-remaining-tokens", "50000")
	h.Set("x-ratelimit-reset-tokens", "60s")
	return h
}

var _ = Describe("Ollama Cloud Quota adapter (Quota Plan PR3, plan matrix line 148)", func() {
	var adapter *Quota

	BeforeEach(func() {
		adapter = NewQuota(quota.HashAccount("test-key"))
	})

	It("starts with NotConfigured{awaiting-first-response} (the upstream proxy may or may not emit rate-limit headers; see quota_live_test.go for the live-key smoke test)", func() {
		snap, _ := adapter.Remaining(context.Background(), providerName, "llama3.3:70b")
		Expect(snap.NotConfigured).NotTo(BeNil())
		Expect(snap.NotConfigured.Reason).To(Equal("awaiting-first-response"))
		Expect(snap.Provider).To(Equal(providerName))
	})

	It("RecordResponse flips variant to RateLimit when the proxy DOES emit the headers", func() {
		adapter.RecordResponse(providerName, "llama3.3:70b", fullRateLimitHeaders(), provider.Usage{})

		snap, _ := adapter.Remaining(context.Background(), providerName, "llama3.3:70b")
		Expect(snap.RateLimit).NotTo(BeNil(),
			"Ollama Cloud graduates from awaiting-pr3 → real RateLimit adapter when the upstream proxy emits the headers (PR3 acceptance is gated on the live-key smoke test confirming this)")
		Expect(snap.RateLimit.Requests.Remaining).To(Equal(90))
	})

	It("Stays NotConfigured when the proxy emits NO rate-limit headers (operator-visible honesty)", func() {
		// Ollama Cloud proxies are heterogeneous — some emit headers,
		// some don't. The no-signal case must preserve NotConfigured
		// rather than synthesising a phantom RateLimit.
		h := http.Header{}
		h.Set("content-type", "application/json")
		adapter.RecordResponse(providerName, "llama3.3:70b", h, provider.Usage{})

		snap, _ := adapter.Remaining(context.Background(), providerName, "llama3.3:70b")
		Expect(snap.NotConfigured).NotTo(BeNil(),
			"NoSignal → NotConfigured preserved (chip tooltip honestly surfaces 'awaiting-first-response')")
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
				"id":"chatcmpl-ollamacloud-bind",
				"object":"chat.completion",
				"created":1700000000,
				"model":"llama3.3:70b",
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
			Model:    "llama3.3:70b",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		Expect(err).NotTo(HaveOccurred())

		snap, _ := adapter.Remaining(context.Background(), providerName, "llama3.3:70b")
		Expect(snap.RateLimit).NotTo(BeNil())
		Expect(snap.RateLimit.Requests.Remaining).To(Equal(90))
	})

	It("conforms to the quota.Quota interface", func() {
		var iface quota.Quota = adapter
		_ = iface
	})
})
