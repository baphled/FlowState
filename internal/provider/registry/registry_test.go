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

	"github.com/baphled/flowstate/internal/config"
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

var _ = Describe("Registry", func() {
	// The resolver reads providers.yaml (providers.zai.api_key), so
	// specs that assume env-only state must pin an empty config
	// dir; otherwise a developer machine's real key leaks in and
	// flips the no-key expectations.
	BeforeEach(func() {
		GinkgoT().Setenv("FLOWSTATE_CONFIG_DIR", GinkgoT().TempDir())
	})

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
		_, err := registry.Resolve("z.ai")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("zai config wiring", func() {
	var configDir string

	BeforeEach(func() {
		configDir = GinkgoT().TempDir()
		GinkgoT().Setenv("FLOWSTATE_CONFIG_DIR", configDir)
		GinkgoT().Setenv("ZAI_API_KEY", "cfg-key")
	})

	writeConfig := func(yaml string) {
		if err := os.WriteFile(filepath.Join(configDir, config.FileName), []byte(yaml), 0o600); err != nil {
			panic(err)
		}
	}
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

	Context("with config plan coding and no ZAI_PLAN", func() {
		var observedCoding bool
		BeforeEach(func() {
			GinkgoT().Setenv("ZAI_API_KEY", "")
			GinkgoT().Setenv("ZAI_PLAN", "")
			writeConfig("providers:\n  zai:\n    plan: coding\n    api_key: config-key\n")
			observedCoding = resolveCoding()
		})
		It("selects the anthropic route from config", func() { Expect(observedCoding).To(BeTrue()) })
	})

	Context("with no plan in config", func() {
		var observedCoding bool
		BeforeEach(func() {
			writeConfig("providers:\n  zai:\n    model: glm-5.3\n")
			observedCoding = resolveCoding()
		})
		It("defaults to the coding plan", func() { Expect(observedCoding).To(BeTrue()) })
	})

	Context("with no zai config at all", func() {
		var observedCoding bool
		BeforeEach(func() {
			GinkgoT().Setenv("ZAI_API_KEY", "env-key")
			writeConfig("providers:\n  default: zai\n")
			observedCoding = resolveCoding()
		})
		It("defaults to the coding plan", func() { Expect(observedCoding).To(BeTrue()) })
	})

	Context("with plan explicitly general", func() {
		var observedCoding bool
		BeforeEach(func() {
			writeConfig("providers:\n  zai:\n    plan: general\n")
			observedCoding = resolveCoding()
		})
		It("honours the explicit general plan", func() { Expect(observedCoding).To(BeFalse()) })
	})

	Context("with ZAI_PLAN=coding and config plan general", func() {
		var observedCoding bool
		BeforeEach(func() {
			GinkgoT().Setenv("ZAI_PLAN", "coding")
			writeConfig("providers:\n  zai:\n    plan: general\n")
			observedCoding = resolveCoding()
		})
		It("lets env override config to coding", func() { Expect(observedCoding).To(BeTrue()) })
	})

	Context("with ZAI_PLAN=general and config plan coding", func() {
		var observedCoding bool
		BeforeEach(func() {
			GinkgoT().Setenv("ZAI_PLAN", "general")
			writeConfig("providers:\n  zai:\n    plan: coding\n")
			observedCoding = resolveCoding()
		})
		It("lets env override config away from coding", func() { Expect(observedCoding).To(BeFalse()) })
	})
})

var _ = Describe("anthropic resolution", func() {
	var configDir string

	BeforeEach(func() {
		configDir = GinkgoT().TempDir()
		GinkgoT().Setenv("FLOWSTATE_CONFIG_DIR", configDir)
		GinkgoT().Setenv("FLOWSTATE_CLAUDE_BIN", "")
	})

	writeConfig := func(yaml string) {
		if err := os.WriteFile(filepath.Join(configDir, config.FileName), []byte(yaml), 0o600); err != nil {
			panic(err)
		}
	}
	resolveWith := func(authType string) (provider.Provider, error) {
		writeConfig("providers:\n  anthropic:\n    auth_type: " + authType + "\n")
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

	It("resolves auth_type oauth to the subscription type", func() {
		p, err := resolveWith("oauth")
		if err != nil {
			panic(err)
		}
		Expect(p).To(BeAssignableToTypeOf(&anthropic.Subscription{}))
	})

	Describe("auth_type api_key", func() {
		const envKey = "sk-ant-synthetic-env-key"
		const cfgKey = "sk-ant-synthetic-config-key"
		var served chan http.Header

		BeforeEach(func() {
			GinkgoT().Setenv("ANTHROPIC_API_KEY", "")
			GinkgoT().Setenv("ANTHROPIC_BASE_URL", "")
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

		It("keeps the oauth route on the subscription provider when ANTHROPIC_API_KEY is set", func() {
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

		It("sends the config key when the environment has none", func() {
			srv := anthropicServer()
			writeConfig("providers:\n  anthropic:\n    auth_type: api_key\n    api_key: " + cfgKey +
				"\n    host: " + srv.URL + "\n")
			p, err := registry.Resolve("anthropic")
			if err != nil {
				panic(err)
			}
			Expect(keyOf(p)).To(Equal(cfgKey))
		})

		It("prefers the environment key over the config key", func() {
			srv := anthropicServer()
			GinkgoT().Setenv("ANTHROPIC_API_KEY", envKey)
			writeConfig("providers:\n  anthropic:\n    auth_type: api_key\n    api_key: " + cfgKey +
				"\n    host: " + srv.URL + "\n")
			p, err := registry.Resolve("anthropic")
			if err != nil {
				panic(err)
			}
			Expect(keyOf(p)).To(Equal(envKey))
		})

		It("fails without a key, naming the environment variable", func() {
			_, err := resolveWith("api_key")
			Expect(err).To(MatchError(ContainSubstring("ANTHROPIC_API_KEY")))
		})

		It("fails without a key, naming the config field", func() {
			_, err := resolveWith("api_key")
			Expect(err).To(MatchError(ContainSubstring("providers.anthropic.api_key")))
		})

		It("lets ANTHROPIC_BASE_URL win over the config host", func() {
			srv := anthropicServer()
			GinkgoT().Setenv("ANTHROPIC_API_KEY", envKey)
			GinkgoT().Setenv("ANTHROPIC_BASE_URL", srv.URL)
			writeConfig("providers:\n  anthropic:\n    auth_type: api_key\n    host: http://127.0.0.1:1\n")
			p, err := registry.Resolve("anthropic")
			if err != nil {
				panic(err)
			}
			Expect(keyOf(p)).To(Equal(envKey))
		})

		It("uses the config host when no base-URL variable is set", func() {
			srv := anthropicServer()
			GinkgoT().Setenv("ANTHROPIC_API_KEY", envKey)
			writeConfig("providers:\n  anthropic:\n    auth_type: api_key\n    host: " + srv.URL + "\n")
			p, err := registry.Resolve("anthropic")
			if err != nil {
				panic(err)
			}
			Expect(keyOf(p)).To(Equal(envKey))
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

	It("names both supported values for an unknown auth_type", func() {
		_, err := resolveWith("magic")
		Expect(err).To(MatchError(ContainSubstring("supported: oauth, api_key")))
	})

	It("rejects an unknown auth_type", func() {
		_, err := resolveWith("magic")
		Expect(err).To(MatchError(ContainSubstring(`unknown anthropic auth_type "magic"`)))
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

	It("resolves the subscription route beside the older harness's config.yaml", func() {
		harness := "providers:\n  default: zai\n  anthropic:\n    auth_type: api_key\n    api_key: harness-key\n"
		if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(harness), 0o600); err != nil {
			panic(err)
		}
		p, err := registry.Resolve("anthropic")
		if err != nil {
			panic(err)
		}
		Expect(p).To(BeAssignableToTypeOf(&anthropic.Subscription{}))
	})

	It("reports a malformed config file instead of falling back to the subscription route", func() {
		writeConfig("providers: [unclosed\n")
		_, err := registry.Resolve("anthropic")
		Expect(err).To(MatchError(config.ErrInvalid))
	})
})
