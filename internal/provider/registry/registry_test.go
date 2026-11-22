package registry_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
	"github.com/baphled/flowstate/internal/provider/registry"
	"github.com/baphled/flowstate/internal/provider/zai"
)

// markerEnv switches the test binary into a fake claude that only leaves a
// marker file; it runs before flag parsing so the binary can stand in for
// FLOWSTATE_CLAUDE_BIN on every OS.
const markerEnv = "FLOWSTATE_REGISTRY_MARKER"

func init() {
	if f := os.Getenv(markerEnv); f != "" {
		_ = os.WriteFile(f, []byte("ran"), 0o600)
		os.Exit(0)
	}
}

func TestRegistry(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Provider Registry Suite")
}

// The resolver reads the environment only, so EVERY spec in this package
// starts with no ambient provider settings: a developer's exported key or
// route must not flip an expectation, and no spec may reach a live
// provider with it. A spec that needs a setting sets its own afterwards.
var _ = BeforeEach(func() {
	for _, name := range []string{"ZAI_API_KEY", "ZAI_BASE_URL", "ZAI_PLAN", "ZAI_CODING_ROUTE",
		"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "FLOWSTATE_ANTHROPIC_AUTH", "FLOWSTATE_CLAUDE_BIN"} {
		GinkgoT().Setenv(name, "")
	}
})

var _ = Describe("Registry", func() {
	It("resolves z.ai from env to the concrete provider type", func() {
		GinkgoT().Setenv("ZAI_API_KEY", "k")
		p, err := registry.Resolve("z.ai")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(BeAssignableToTypeOf(&zai.Provider{}))
	})

	It("resolves z.ai from env reporting the provider name", func() {
		GinkgoT().Setenv("ZAI_API_KEY", "k")
		p, err := registry.Resolve("z.ai")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Name()).To(Equal("z.ai"))
	})

	It("errors for an unknown provider name", func() {
		_, err := registry.Resolve("nope")
		Expect(err).To(HaveOccurred())
	})

	It("errors when z.ai is resolved without an api key", func() {
		GinkgoT().Setenv("ZAI_API_KEY", "")
		_, err := registry.Resolve("z.ai")
		Expect(err).To(MatchError(ContainSubstring("ZAI_API_KEY is not set")))
	})
})

var _ = Describe("a providers.yaml left in the old locations", func() {
	const legacy = "providers:\n  zai:\n    api_key: file-key\n    plan: general\n" +
		"  anthropic:\n    auth_type: api_key\n    api_key: file-key\n"

	// plantLegacyFile writes the file in every location the prototype read
	// and points HOME, XDG_CONFIG_HOME and the old directory variable at it.
	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		for _, sub := range []string{"flowstate", ".config/flowstate", "."} {
			target := filepath.Join(dir, sub)
			if err := os.MkdirAll(target, 0o750); err != nil {
				panic(err)
			}
			for _, name := range []string{"providers.yaml", "config.yaml"} {
				if err := os.WriteFile(filepath.Join(target, name), []byte(legacy), 0o600); err != nil {
					panic(err)
				}
			}
		}
		GinkgoT().Setenv("HOME", dir)
		GinkgoT().Setenv("XDG_CONFIG_HOME", dir)
		GinkgoT().Setenv("APPDATA", dir)
		GinkgoT().Setenv("FLOWSTATE_CONFIG_DIR", dir)
	})

	It("does not supply the z.ai key", func() {
		_, err := registry.Resolve("z.ai")
		Expect(err).To(MatchError(ContainSubstring("ZAI_API_KEY is not set")))
	})

	It("does not supply the z.ai plan", func() {
		GinkgoT().Setenv("ZAI_API_KEY", "env-key")
		p, err := registry.Resolve("z.ai")
		if err != nil {
			panic(err)
		}
		zp, ok := p.(*zai.Provider)
		if !ok {
			panic("registry did not resolve a zai provider")
		}
		Expect(zp.Coding()).To(BeTrue())
	})

	It("does not select the anthropic API-key route", func() {
		p, err := registry.Resolve("anthropic")
		if err != nil {
			panic(err)
		}
		Expect(p).To(BeAssignableToTypeOf(&anthropic.Subscription{}))
	})
})

var _ = Describe("z.ai environment resolution", func() {
	It("names ZAI_API_KEY when it is unset", func() {
		_, err := registry.Resolve("zai")
		Expect(err).To(MatchError(ContainSubstring("ZAI_API_KEY")))
	})

	It("resolves the zai alias with a key", func() {
		GinkgoT().Setenv("ZAI_API_KEY", "k")
		p, err := registry.Resolve("zai")
		if err != nil {
			panic(err)
		}
		Expect(p.Name()).To(Equal("z.ai"))
	})
})

var _ = Describe("anthropic resolution", func() {
	resolveWith := func(auth string) (provider.Provider, error) {
		GinkgoT().Setenv("FLOWSTATE_ANTHROPIC_AUTH", auth)
		return registry.Resolve("anthropic")
	}

	It("resolves anthropic to the subscription type by default", func() {
		p, err := registry.Resolve("anthropic")
		if err != nil {
			panic(err)
		}
		Expect(p).To(BeAssignableToTypeOf(&anthropic.Subscription{}))
	})

	It("reports the anthropic provider name", func() {
		p, err := registry.Resolve("anthropic")
		if err != nil {
			panic(err)
		}
		Expect(p.Name()).To(Equal("anthropic"))
	})

	It("resolves FLOWSTATE_ANTHROPIC_AUTH=oauth to the subscription type", func() {
		p, err := resolveWith("oauth")
		if err != nil {
			panic(err)
		}
		Expect(p).To(BeAssignableToTypeOf(&anthropic.Subscription{}))
	})

	Describe("FLOWSTATE_ANTHROPIC_AUTH=api_key", func() {
		const envKey = "sk-ant-synthetic-env-key"
		var served chan http.Header

		BeforeEach(func() {
			served = make(chan http.Header, 4)
		})

		// anthropicServer plays the Anthropic API and records request headers.
		anthropicServer := func() *httptest.Server {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				served <- r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"ok"}]}`)
			}))
			DeferCleanup(srv.Close)
			return srv
		}
		keyOf := func(p provider.Provider) string {
			if _, err := p.Chat(context.Background(), &provider.ChatRequest{}); err != nil {
				panic(err)
			}
			return (<-served).Get("x-api-key")
		}

		It("keeps FLOWSTATE_ANTHROPIC_AUTH=oauth on the subscription provider when ANTHROPIC_API_KEY is set", func() {
			GinkgoT().Setenv("ANTHROPIC_API_KEY", envKey)
			p, err := resolveWith("oauth")
			if err != nil {
				panic(err)
			}
			Expect(p).To(BeAssignableToTypeOf(&anthropic.Subscription{}))
		})

		It("resolves to an anthropic-named provider using the environment key", func() {
			GinkgoT().Setenv("ANTHROPIC_API_KEY", envKey)
			p, err := resolveWith("api_key")
			if err != nil {
				panic(err)
			}
			Expect(p.Name()).To(Equal("anthropic"))
		})

		It("resolves to the API-key type, not the subscription type", func() {
			GinkgoT().Setenv("ANTHROPIC_API_KEY", envKey)
			p, err := resolveWith("api_key")
			if err != nil {
				panic(err)
			}
			Expect(p).To(BeAssignableToTypeOf(&anthropic.APIKey{}))
		})

		It("sends the environment key", func() {
			GinkgoT().Setenv("ANTHROPIC_API_KEY", envKey)
			GinkgoT().Setenv("ANTHROPIC_BASE_URL", anthropicServer().URL)
			p, err := resolveWith("api_key")
			if err != nil {
				panic(err)
			}
			Expect(keyOf(p)).To(Equal(envKey))
		})

		It("fails without a key, naming the environment variable", func() {
			_, err := resolveWith("api_key")
			Expect(err).To(MatchError(ContainSubstring("ANTHROPIC_API_KEY")))
		})

		It("makes no network request while resolving", func() {
			srv := anthropicServer()
			GinkgoT().Setenv("ANTHROPIC_API_KEY", envKey)
			GinkgoT().Setenv("ANTHROPIC_BASE_URL", srv.URL)
			if _, err := resolveWith("api_key"); err != nil {
				panic(err)
			}
			Consistently(served).WithTimeout(100 * time.Millisecond).WithPolling(10 * time.Millisecond).ShouldNot(Receive())
		})

		It("does not leak the key into the missing-key error", func() {
			GinkgoT().Setenv("ANTHROPIC_API_KEY", "")
			_, err := resolveWith("api_key")
			Expect(err).To(MatchError(Not(ContainSubstring("sk-ant"))))
		})
	})

	It("resolves the subscription route when ANTHROPIC_API_KEY is set and the route is unset", func() {
		GinkgoT().Setenv("ANTHROPIC_API_KEY", "sk-ant-synthetic-ambient-key")
		p, err := registry.Resolve("anthropic")
		if err != nil {
			panic(err)
		}
		Expect(p).To(BeAssignableToTypeOf(&anthropic.Subscription{}))
	})

	It("treats an empty FLOWSTATE_ANTHROPIC_AUTH as the subscription route", func() {
		p, err := resolveWith("")
		if err != nil {
			panic(err)
		}
		Expect(p).To(BeAssignableToTypeOf(&anthropic.Subscription{}))
	})

	It("names both supported values for an unknown FLOWSTATE_ANTHROPIC_AUTH", func() {
		_, err := resolveWith("magic")
		Expect(err).To(MatchError(ContainSubstring("supported: oauth, api_key")))
	})

	It("names the variable and the value for an unknown FLOWSTATE_ANTHROPIC_AUTH", func() {
		_, err := resolveWith("magic")
		Expect(err).To(MatchError(ContainSubstring(`unknown FLOWSTATE_ANTHROPIC_AUTH "magic"`)))
	})

	It("does not resolve claude-code", func() {
		_, err := registry.Resolve("claude-code")
		Expect(err).To(MatchError(ContainSubstring("unknown provider")))
	})

	It("honours FLOWSTATE_CLAUDE_BIN as the binary to run", func() {
		missing := filepath.Join(GinkgoT().TempDir(), "custom-claude")
		GinkgoT().Setenv("FLOWSTATE_CLAUDE_BIN", missing)
		p, err := registry.Resolve("anthropic")
		if err != nil {
			panic(err)
		}
		_, err = p.Chat(context.Background(), &provider.ChatRequest{
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		Expect(err).To(MatchError(ContainSubstring(missing)))
	})

	It("does not run or require the binary at resolve time", func() {
		GinkgoT().Setenv("FLOWSTATE_CLAUDE_BIN", filepath.Join(GinkgoT().TempDir(), "absent-claude"))
		p, err := registry.Resolve("anthropic")
		if err != nil {
			panic(err)
		}
		Expect(p).NotTo(BeNil())
	})

	It("never runs the configured binary at resolve time", func() {
		self, err := os.Executable()
		if err != nil {
			panic(err)
		}
		marker := filepath.Join(GinkgoT().TempDir(), "ran")
		GinkgoT().Setenv("FLOWSTATE_CLAUDE_BIN", self)
		GinkgoT().Setenv(markerEnv, marker)
		if _, err = registry.Resolve("anthropic"); err != nil {
			panic(err)
		}
		Expect(marker).NotTo(BeAnExistingFile())
	})
})

var _ = Describe("resolved z.ai finish events", func() {
	// finishOf streams one turn from a fake z.ai server through the registry
	// and returns the last event.
	finishOf := func(route, body string) provider.StreamEvent {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, body)
		}))
		DeferCleanup(srv.Close)
		GinkgoT().Setenv("ZAI_API_KEY", "k")
		GinkgoT().Setenv("ZAI_PLAN", "coding")
		GinkgoT().Setenv("ZAI_CODING_ROUTE", route)
		GinkgoT().Setenv("ZAI_BASE_URL", srv.URL)
		p, err := registry.Resolve("z.ai")
		if err != nil {
			panic(err)
		}
		streamer, ok := p.(provider.Streamer)
		if !ok {
			panic("resolved z.ai provider is not a Streamer")
		}
		var last provider.StreamEvent
		for ev := range streamer.Stream(context.Background(), &provider.ChatRequest{}) {
			last = ev
		}
		return last
	}

	It("ends a completed default coding stream with a finish event", func() {
		last := finishOf("", "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\ndata: [DONE]\n")
		Expect(last.Kind).To(Equal(provider.StreamEventFinish))
	})

	It("ends a truncated default coding stream with the length reason", func() {
		last := finishOf("", "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"length\"}]}\ndata: [DONE]\n")
		Expect(last.FinishReason).To(Equal("length"))
	})

	It("ends a completed anthropic stream with a finish event", func() {
		last := finishOf("anthropic", "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\n"+
			"data: {\"type\":\"message_stop\"}\n\n")
		Expect(last).To(Equal(provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: "length"}))
	})
})
