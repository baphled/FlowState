package zai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/zai"
)

func TestZai(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Zai Provider Suite")
}

func newTestServer(handler http.HandlerFunc) (*httptest.Server, *zai.Provider) {
	srv := httptest.NewServer(handler)
	DeferCleanup(srv.Close)
	p := zai.New("test-key", zai.WithBaseURL(srv.URL))
	return srv, p
}

var _ = Describe("Zai provider", func() {
	It("rejects construction from env when the api key is missing", func() {
		_, err := zai.NewFromEnv()
		Expect(err).To(HaveOccurred())
	})

	It("constructs from env and satisfies the provider port", func() {
		GinkgoT().Setenv("ZAI_API_KEY", "k")
		p, err := zai.NewFromEnv()
		Expect(err).NotTo(HaveOccurred())
		var _ provider.Provider = p
	})

	It("reports the z.ai identity and a non-empty model list", func() {
		_, p := newTestServer(func(http.ResponseWriter, *http.Request) {})
		var _ provider.Provider = p
		Expect(p.Name()).To(Equal("z.ai"))
		Expect(p.Models()).NotTo(BeEmpty())
	})

	It("sends auth, model and body to chat completions and parses content", func() {
		var gotAuth, gotBody, gotPath string
		_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			gotPath = r.URL.Path
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "c1",
				"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "hello there"}}},
			})
		})
		resp, err := p.Chat(context.Background(), &provider.ChatRequest{
			Model:    "glm-4.6",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Content).To(Equal("hello there"))
		Expect(gotAuth).To(Equal("Bearer test-key"))
		Expect(gotPath).To(HaveSuffix("/chat/completions"))
		Expect(gotBody).To(ContainSubstring(`"model":"glm-4.6"`))
	})

	It("surfaces api errors as typed errors", func() {
		_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"1002","message":"invalid api key"}}`))
		})
		_, err := p.Chat(context.Background(), &provider.ChatRequest{Model: "glm-4.6"})
		var apiErr *zai.APIError
		Expect(errors.As(err, &apiErr)).To(BeTrue())
		Expect(apiErr.Status).To(Equal(http.StatusUnauthorized))
		Expect(apiErr.Code).To(Equal("1002"))
		Expect(apiErr.Message).To(Equal("invalid api key"))
		Expect(errors.Is(err, zai.ErrUnauthorized)).To(BeTrue())
	})

	It("does not silently retry failed chat calls", func() {
		calls := 0
		_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"boom"}}`))
		})
		_, err := p.Chat(context.Background(), &provider.ChatRequest{Model: "glm-4.6"})
		Expect(err).To(HaveOccurred())
		Expect(calls).To(Equal(1))
	})

	It("yields stream deltas in order as concatenated content", func() {
		_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Header.Get("Accept")).To(ContainSubstring("text/event-stream"))
			w.Header().Set("Content-Type", "text/event-stream")
			fl := w.(http.Flusher)
			for _, chunk := range []string{
				`data: {"choices":[{"delta":{"content":"he"}}]}`,
				`data: {"choices":[{"delta":{"content":"llo"}}]}`,
				`data: [DONE]`,
			} {
				_, _ = w.Write([]byte(chunk + "\n\n"))
				fl.Flush()
			}
		})
		var sb strings.Builder
		for ev := range p.Stream(context.Background(), &provider.ChatRequest{Model: "glm-4.6"}) {
			Expect(ev.Err).NotTo(HaveOccurred())
			sb.WriteString(ev.Content)
		}
		Expect(sb.String()).To(Equal("hello"))
	})

	It("surfaces typed errors from a failed stream", func() {
		_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"1302","message":"rate limited"}}`))
		})
		var gotErr error
		for ev := range p.Stream(context.Background(), &provider.ChatRequest{Model: "glm-4.6"}) {
			if ev.Err != nil {
				gotErr = ev.Err
			}
		}
		var apiErr *zai.APIError
		Expect(errors.As(gotErr, &apiErr)).To(BeTrue())
		Expect(apiErr.Status).To(Equal(http.StatusTooManyRequests))
	})

	It("stops streaming when the context is cancelled", func() {
		release := make(chan struct{})
		_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			<-release
		})
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		done := make(chan struct{})
		go func() {
			for range p.Stream(ctx, &provider.ChatRequest{Model: "glm-4.6"}) {
			}
			close(done)
		}()
		cancel()
		Eventually(done).WithTimeout(5 * time.Second).Should(BeClosed())
		close(release)
	})
})
