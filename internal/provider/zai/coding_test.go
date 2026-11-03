package zai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
)

// codingSrv is a canned openai coding-completions server capturing
// the Authorization header and request path.
func codingSrv() (*httptest.Server, *atomic.Value, *atomic.Value) {
	var auth, path atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		path.Store(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"x"}}]}`))
	}))
	return srv, &auth, &path
}

var _ = Describe("coding (OpenAI-compatible default) route", func() {
	It("chats via the chat-completions shape", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
		}))
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding())
		resp, err := p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Content).To(Equal("hi"))
	})

	It("sends Bearer auth to /chat/completions on the openai coding route", func() {
		srv, auth, _ := codingSrv()
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding())
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(auth.Load()).To(Equal("Bearer k"))
	})

	It("emits text deltas from chat.completion.chunk events", func() {
		sse := "data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"y\"}}]}\n" +
			"data: [DONE]\n\n"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(sse))
		}))
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding())
		var got string
		for ev := range p.Stream(context.Background(), &provider.ChatRequest{}) {
			got += ev.Content
		}
		Expect(got).To(Equal("hey"))
	})

	It("uses the coding paas endpoint URL when no base URL is overridden", func() {
		srv, _, path := codingSrv()
		defer srv.Close()
		old := codingOpenAIBaseURL
		codingOpenAIBaseURL = srv.URL + "/api/coding/paas/v4"
		defer func() { codingOpenAIBaseURL = old }()
		p := New("k", WithCoding())
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(path.Load()).To(Equal("/api/coding/paas/v4/chat/completions"))
	})

	It("clamps max_tokens into range on the openai coding route", func() {
		var body atomic.Value
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b := make([]byte, 4096)
			n, _ := r.Body.Read(b)
			body.Store(string(b[:n]))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"x"}}]}`))
		}))
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding())
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{MaxTokens: 500000})
		Expect(body.Load()).To(ContainSubstring(`"max_tokens":131072`))
	})

	It("surfaces a typed APIError on non-2xx coding responses", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"1113","message":"Insufficient balance or no resource package"}}`))
		}))
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding())
		_, err := p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(err.Error()).To(ContainSubstring("1113"))
	})

	It("wraps the non-2xx coding error", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"1113","message":"Insufficient balance or no resource package"}}`))
		}))
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding())
		_, err := p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("WithCodingRoute", func() {
	It("falls back to the openai route for unknown values", func() {
		srv, _, path := codingSrv()
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding(), WithCodingRoute("bogus"))
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(path.Load()).To(Equal("/chat/completions"))
	})
})
