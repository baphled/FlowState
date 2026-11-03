package registry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/registry"
	"github.com/baphled/flowstate/internal/provider/zai"
)

func tctx() context.Context { return context.Background() }

func resolveForRoute() (provider.Provider, error) { return registry.Resolve("z.ai") }

var _ = Describe("zai coding route precedence", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("FLOWSTATE_CONFIG_DIR", GinkgoT().TempDir())
		GinkgoT().Setenv("ZAI_API_KEY", "k")
		GinkgoT().Setenv("ZAI_BASE_URL", "")
		GinkgoT().Setenv("ZAI_PLAN", "")
		GinkgoT().Setenv("ZAI_CODING_ROUTE", "")
	})

	routeServer := func() (*httptest.Server, *atomic.Value) {
		var path atomic.Value
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path.Store(r.Method + " " + r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"content":[]}`))
		}))
		return srv, &path
	}

	It("routes coding plans to the openai coding endpoint by default", func() {
		srv, path := routeServer()
		defer srv.Close()
		zai.TestHookSetCodingBaseURL(srv.URL + "/api/coding/paas/v4")
		GinkgoT().Setenv("ZAI_PLAN", "coding")
		p, err := resolveForRoute()
		Expect(err).NotTo(HaveOccurred())
		_, err = p.Chat(tctx(), &provider.ChatRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(path.Load()).To(Equal("POST /api/coding/paas/v4/chat/completions"))
	})

	It("selects the anthropic endpoint via ZAI_CODING_ROUTE=anthropic", func() {
		srv, path := routeServer()
		zai.TestHookSetAnthropicBaseURL(srv.URL + "/api/anthropic")
		GinkgoT().Setenv("ZAI_PLAN", "coding")
		GinkgoT().Setenv("ZAI_CODING_ROUTE", "anthropic")
		p, err := resolveForRoute()
		Expect(err).NotTo(HaveOccurred())
		_, err = p.Chat(tctx(), &provider.ChatRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(path.Load()).To(Equal("POST /api/anthropic/v1/messages"))
	})

	It("lets an explicit ZAI_BASE_URL win over the plan-tag default", func() {
		srv, path := routeServer()
		defer srv.Close()
		zai.TestHookSetCodingBaseURL("https://zai-invalid.example.invalid")
		GinkgoT().Setenv("ZAI_PLAN", "coding")
		GinkgoT().Setenv("ZAI_BASE_URL", srv.URL+"/custom")
		p, err := resolveForRoute()
		Expect(err).NotTo(HaveOccurred())
		_, err = p.Chat(tctx(), &provider.ChatRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(path.Load()).To(Equal("POST /custom/chat/completions"))
	})
})
