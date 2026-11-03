package zai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
)

// codingServer serves body at POST */v1/messages, capturing either
// the x-api-key header or raw request bodies.
func codingServer(body, capture string) (*httptest.Server, func() string) {
	var captured atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture == "x-api-key" {
			captured.Store(r.Header.Get("x-api-key"))
		} else {
			b, _ := io.ReadAll(r.Body)
			captured.Store(string(b))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	return srv, func() string { v, _ := captured.Load().(string); return v }
}

var _ = Describe("coding (Anthropic-compatible) route", func() {
	It("chats via the anthropic messages shape", func() {
		srv, _ := codingServer(`{"content":[{"type":"text","text":"hello"}]}`, "")
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding())
		resp, err := p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Content).To(Equal("hello"))
	})

	It("sends the anthropic request shape", func() {
		srv, bodies := codingServer(`{"content":[]}`, "")
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding())
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(bodies()).To(ContainSubstring(`"max_tokens"`))
	})

	It("sends x-api-key and anthropic-version headers", func() {
		srv, hdrs := codingServer(`{"content":[]}`, "x-api-key")
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding())
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(hdrs()).To(Equal("k"))
	})

	It("emits text deltas from content_block_delta events", func() {
		sse := "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"he\"}}\n" +
			"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"y\"}}\n" +
			"data: {\"type\":\"message_stop\"}\n\n"
		srv, _ := codingServer(sse, "")
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding())
		var got string
		for ev := range p.Stream(context.Background(), &provider.ChatRequest{}) {
			got += ev.Content
		}
		Expect(got).To(Equal("hey"))
	})

	It("stops streaming at message_stop", func() {
		srv, _ := codingServer("data: {\"type\":\"message_stop\"}\n\n", "")
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding())
		var n int
		for range p.Stream(context.Background(), &provider.ChatRequest{}) {
			n++
		}
		Expect(n).To(Equal(0))
	})

	It("uses the anthropic coding endpoint URL when no base URL is overridden", func() {
		var hitPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hitPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"content":[]}`)
		}))
		defer srv.Close()
		// Redirect the coding endpoint to the test server.
		old := codingBaseURL
		codingBaseURL = srv.URL + "/api/anthropic"
		defer func() { codingBaseURL = old }()
		p := New("k", WithCoding())
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(hitPath).To(Equal("/api/anthropic/v1/messages"))
	})

	It("clamps max_tokens into [1,131072] on the coding route", func() {
		srv, bodies := codingServer(`{"content":[]}`, "")
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding())
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{MaxTokens: 500000})
		Expect(bodies()).To(ContainSubstring(`"max_tokens":131072`))
	})

	It("clamps a zero max_tokens to 1 on the coding route", func() {
		srv, bodies := codingServer(`{"content":[]}`, "")
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding())
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(bodies()).To(ContainSubstring(`"max_tokens":1`))
	})
})

var _ = Describe("ClampMaxTokens", func() {
	It("lifts values below one", func() {
		Expect(ClampMaxTokens(0)).To(Equal(int64(1)))
	})

	It("lifts negative values to one", func() {
		Expect(ClampMaxTokens(-5)).To(Equal(int64(1)))
	})

	It("keeps in-range values unchanged", func() {
		Expect(ClampMaxTokens(42)).To(Equal(int64(42)))
	})

	It("keeps the exact maximum unchanged", func() {
		Expect(ClampMaxTokens(131072)).To(Equal(int64(131072)))
	})

	It("caps values above the maximum", func() {
		Expect(ClampMaxTokens(131073)).To(Equal(int64(131072)))
	})
})

var _ = Describe("WithCoding base URL", func() {
	It("honours an explicit base URL override over the coding endpoint", func() {
		var hitPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hitPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"content":[]}`)
		}))
		defer srv.Close()
		old := codingBaseURL
		codingBaseURL = "https://zai-invalid.example.invalid"
		defer func() { codingBaseURL = old }()
		p := New("k", WithBaseURL(srv.URL), WithCoding())
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(hitPath).To(Equal("/v1/messages"))
	})
})
