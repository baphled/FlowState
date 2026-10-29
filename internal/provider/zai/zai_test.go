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

// collectStream drains a stream into its concatenated content, panicking on
// any event error so fixture failures surface loudly during arrangement.
func collectStream(p *zai.Provider, req *provider.ChatRequest) string {
	var sb strings.Builder
	for ev := range p.Stream(context.Background(), req) {
		if ev.Err != nil {
			panic(ev.Err)
		}
		sb.WriteString(ev.Content)
	}
	return sb.String()
}

// firstStreamErr returns the first error observed on a stream, or nil.
func firstStreamErr(p *zai.Provider, req *provider.ChatRequest) error {
	var gotErr error
	for ev := range p.Stream(context.Background(), req) {
		if ev.Err != nil {
			gotErr = ev.Err
		}
	}
	return gotErr
}

func chat(p *zai.Provider) (*provider.ChatResponse, error) {
	return p.Chat(context.Background(), &provider.ChatRequest{Model: "glm-4.6"})
}

var _ = Describe("Zai provider", func() {
	It("rejects construction from env when the api key is missing", func() {
		_, err := zai.NewFromEnv()
		Expect(err).To(HaveOccurred())
	})

	It("constructs from env and reports no error, satisfying the provider port", func() {
		GinkgoT().Setenv("ZAI_API_KEY", "k")
		p, err := zai.NewFromEnv()
		Expect(err).NotTo(HaveOccurred())
		var _ provider.Provider = p
		Expect(p).NotTo(BeNil())
	})

	It("reports the z.ai identity", func() {
		_, p := newTestServer(func(http.ResponseWriter, *http.Request) {})
		var _ provider.Provider = p
		Expect(p.Name()).To(Equal("z.ai"))
	})

	It("exposes a non-empty model list", func() {
		_, p := newTestServer(func(http.ResponseWriter, *http.Request) {})
		var _ provider.Provider = p
		Expect(p.Models()).NotTo(BeEmpty())
	})

	It("sends auth, model and body to chat completions and parses content", func() {
		var gotAuth, gotPathOK, gotBodyOK string
		_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			if strings.HasSuffix(r.URL.Path, "/chat/completions") {
				gotPathOK = "path-ok"
			}
			b, _ := io.ReadAll(r.Body)
			if strings.Contains(string(b), `"model":"glm-4.6"`) {
				gotBodyOK = "body-ok"
			}
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
		Expect([4]any{resp.Content, gotAuth, gotPathOK, gotBodyOK}).
			To(Equal([4]any{"hello there", "Bearer test-key", "path-ok", "body-ok"}))
	})

	It("wraps api failures in the typed APIError and the unauthorized sentinel", func() {
		_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"1002","message":"invalid api key"}}`))
		})
		_, err := chat(p)
		var apiErr *zai.APIError
		Expect([2]any{errors.As(err, &apiErr), errors.Is(err, zai.ErrUnauthorized)}).
			To(Equal([2]any{true, true}))
	})

	It("populates APIError status, code and message from the api response", func() {
		_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"1002","message":"invalid api key"}}`))
		})
		_, err := chat(p)
		var apiErr *zai.APIError
		if !errors.As(err, &apiErr) {
			panic("expected *zai.APIError")
		}
		Expect([3]any{apiErr.Status, apiErr.Code, apiErr.Message}).
			To(Equal([3]any{http.StatusUnauthorized, "1002", "invalid api key"}))
	})

	It("does not silently retry failed chat calls", func() {
		calls := 0
		_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"boom"}}`))
		})
		_, err := chat(p)
		Expect([2]any{err != nil, calls}).To(Equal([2]any{true, 1}))
	})

	It("requests event streaming when streaming chat deltas", func() {
		var accept string
		_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			accept = r.Header.Get("Accept")
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
		content := collectStream(p, &provider.ChatRequest{Model: "glm-4.6"})
		Expect([2]any{strings.Contains(accept, "text/event-stream"), content}).To(Equal([2]any{true, "hello"}))
	})

	It("surfaces typed errors from a failed stream", func() {
		_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"1302","message":"rate limited"}}`))
		})
		gotErr := firstStreamErr(p, &provider.ChatRequest{Model: "glm-4.6"})
		var apiErr *zai.APIError
		if !errors.As(gotErr, &apiErr) {
			panic("expected *zai.APIError from stream")
		}
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
		Eventually(done).WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(BeClosed())
		close(release)
	})
})
