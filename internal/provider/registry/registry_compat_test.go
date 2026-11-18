package registry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/registry"
	"github.com/baphled/flowstate/internal/provider/zai"
)

// Old-shape config regression (owner policy 2026-11-03): any config
// schema/format change must ship with a consumer update or stay
// backward-compatible — two configs side by side until cutover.
// These specs pin that a config.yaml written BEFORE the
// providers.zai.route key existed (plan only, no route, no host)
// loads cleanly on the current binary and resolves to the
// defaults-filled provider (coding plan -> openai coding endpoint
// default, as merged in #176/#177).
var _ = Describe("old config shape compatibility", func() {
	var configDir string
	var oldShapeConfig string

	BeforeEach(func() {
		configDir = GinkgoT().TempDir()
		GinkgoT().Setenv("FLOWSTATE_CONFIG_DIR", configDir)
		GinkgoT().Setenv("ZAI_API_KEY", "")
		GinkgoT().Setenv("ZAI_BASE_URL", "")
		GinkgoT().Setenv("ZAI_PLAN", "")
		GinkgoT().Setenv("ZAI_CODING_ROUTE", "")
		oldShapeConfig = "providers:\n" +
			"  zai:\n" +
			"    api_key: old-key\n" +
			"    host: ''\n" +
			"    model: glm-5.3\n" +
			"    plan: coding\n"
	})

	// oldShapeConfig is assigned in BeforeEach with the exact
	// pre-#176 shape: no route key, empty host, plan coding. Each
	// spec writes it directly so behavioural expectations stay
	// inside the specs per the changed-test contract.
	writeOldConfig := func() {
		if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(oldShapeConfig), 0o600); err != nil {
			panic(err)
		}
	}

	It("loads the old shape without route key and defaults fill the gaps", func() {
		writeOldConfig()
		p, resolveErr := registry.Resolve("z.ai")
		if resolveErr != nil {
			panic(resolveErr)
		}
		zp, ok := p.(*zai.Provider)
		if !ok {
			panic("registry did not resolve a zai provider")
		}
		Expect(zp.Coding()).To(BeTrue(), "plan: coding must still select the coding route")
	})

	It("sends old-shape coding traffic to the openai coding endpoint default", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"content":[]}`))
		}))
		defer srv.Close()
		zai.TestHookSetCodingBaseURL(srv.URL + "/api/coding/paas/v4")
		writeOldConfig()
		p, err := registry.Resolve("z.ai")
		Expect(err).NotTo(HaveOccurred())
		resp, err := p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Content).To(Equal("ok"))
	})
})
