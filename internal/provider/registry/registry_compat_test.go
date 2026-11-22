package registry_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/registry"
	"github.com/baphled/flowstate/internal/provider/zai"
)

// A coding-plan z.ai key without any route or base URL variable must
// resolve to the openai coding endpoint default (merged in #176/#177), and an
// unset ZAI_PLAN must mean the coding plan: the general endpoint 429s every
// coding-plan key.
var _ = Describe("zai plan defaults", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("ZAI_API_KEY", "k")
		GinkgoT().Setenv("ZAI_BASE_URL", "")
		GinkgoT().Setenv("ZAI_PLAN", "")
		GinkgoT().Setenv("ZAI_CODING_ROUTE", "")
	})

	resolveCoding := func() bool {
		p, err := registry.Resolve("z.ai")
		if err != nil {
			panic(err)
		}
		zp, ok := p.(*zai.Provider)
		if !ok {
			panic("registry did not resolve a zai provider")
		}
		return zp.Coding()
	}

	It("defaults to the coding plan when ZAI_PLAN is unset", func() {
		Expect(resolveCoding()).To(BeTrue())
	})

	It("selects the coding plan for ZAI_PLAN=coding", func() {
		GinkgoT().Setenv("ZAI_PLAN", "coding")
		Expect(resolveCoding()).To(BeTrue())
	})

	It("honours ZAI_PLAN=general", func() {
		GinkgoT().Setenv("ZAI_PLAN", "general")
		Expect(resolveCoding()).To(BeFalse())
	})

	It("sends default coding traffic to the openai coding endpoint default", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"content":[]}`))
		}))
		defer srv.Close()
		zai.TestHookSetCodingBaseURL(srv.URL + "/api/coding/paas/v4")
		p, err := registry.Resolve("z.ai")
		Expect(err).NotTo(HaveOccurred())
		resp, err := p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Content).To(Equal("ok"))
	})
})
