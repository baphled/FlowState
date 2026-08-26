package zai

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

// fullZAIRateLimitHeaders builds a header set in the Z.AI dialect —
// note seconds-int reset values rather than duration strings.
func fullZAIRateLimitHeaders() http.Header {
	h := http.Header{}
	h.Set("request-id", "zai_req_success") // Z.AI uses bare request-id (not x-request-id)
	h.Set("x-ratelimit-limit-requests", "60")
	h.Set("x-ratelimit-remaining-requests", "55")
	h.Set("x-ratelimit-reset-requests", "120") // seconds-int (Z.AI dialect)
	h.Set("x-ratelimit-limit-tokens", "128000")
	h.Set("x-ratelimit-remaining-tokens", "100000")
	h.Set("x-ratelimit-reset-tokens", "300")
	return h
}

var _ = Describe("Z.AI Quota adapter (Quota Plan PR3, plan matrix line 146)", func() {
	var adapter *Quota

	BeforeEach(func() {
		adapter = NewQuota(quota.HashAccount("test-key"))
	})

	It("starts with NotConfigured{awaiting-first-response}", func() {
		snap, _ := adapter.Remaining(context.Background(), providerName, "glm-4.7")
		Expect(snap.NotConfigured).NotTo(BeNil())
		Expect(snap.NotConfigured.Reason).To(Equal("awaiting-first-response"))
		Expect(snap.Provider).To(Equal(providerName))
	})

	It("RecordResponse flips variant to RateLimit on Z.AI-dialect headers (seconds-int reset, bare request-id)", func() {
		adapter.RecordResponse(providerName, "glm-4.7", fullZAIRateLimitHeaders(), provider.Usage{})

		snap, _ := adapter.Remaining(context.Background(), providerName, "glm-4.7")
		Expect(snap.RateLimit).NotTo(BeNil(),
			"Z.AI graduates from awaiting-pr3 → real RateLimit adapter; the openaicompat shared parser accepts both OpenAI and Z.AI dialects")
		Expect(snap.RateLimit.Requests.Remaining).To(Equal(55))
		Expect(snap.RateLimit.Tokens.Remaining).To(Equal(100000))
	})

	It("Bind end-to-end: Chat 2xx populates the live Snapshot", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			for k, vs := range fullZAIRateLimitHeaders() {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl-zai-bind",
				"object":"chat.completion",
				"created":1700000000,
				"model":"glm-4.7",
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
			Model:    "glm-4.7",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		Expect(err).NotTo(HaveOccurred())

		snap, _ := adapter.Remaining(context.Background(), providerName, "glm-4.7")
		Expect(snap.RateLimit).NotTo(BeNil())
		Expect(snap.RateLimit.Requests.Remaining).To(Equal(55))
	})

	It("conforms to the quota.Quota interface", func() {
		var iface quota.Quota = adapter
		_ = iface
	})
})
