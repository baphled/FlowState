package config_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

// QDRANT_URL env-var precedence
//
// Bug: `flowstate run` warned operators to set QDRANT_URL to enable vector
// recall, but the binary never read the env var — only YAML. The vault-server
// binary already honoured QDRANT_URL, so the warning string was true on one
// binary and a lie on the other. This spec pins the resolver-level behaviour
// the warning was promising: env var falls back when the YAML qdrant.url
// field is empty, matching the precedent set by SystemPromptBudgetEnv.
var _ = Describe("AppConfig.ResolvedQdrantURL", func() {
	BeforeEach(func() {
		_ = os.Unsetenv(config.QdrantURLEnv)
	})

	AfterEach(func() {
		_ = os.Unsetenv(config.QdrantURLEnv)
	})

	It("returns the empty string on a nil receiver", func() {
		var cfg *config.AppConfig
		Expect(cfg.ResolvedQdrantURL()).To(Equal(""))
	})

	It("returns empty when neither env nor YAML are set", func() {
		cfg := &config.AppConfig{}
		Expect(cfg.ResolvedQdrantURL()).To(Equal(""))
	})

	It("returns the YAML field when set", func() {
		cfg := &config.AppConfig{}
		cfg.Qdrant.URL = "http://yaml-host:6333"
		Expect(cfg.ResolvedQdrantURL()).To(Equal("http://yaml-host:6333"))
	})

	It("falls back to the QDRANT_URL env var when YAML is empty", func() {
		Expect(os.Setenv(config.QdrantURLEnv, "http://env-host:6333")).To(Succeed())
		cfg := &config.AppConfig{}
		Expect(cfg.ResolvedQdrantURL()).To(Equal("http://env-host:6333"),
			"QDRANT_URL env var must be honoured by flowstate run, matching the warning text and the vault-server contract")
	})

	It("YAML wins when both YAML and env are set (explicit config beats fallback)", func() {
		Expect(os.Setenv(config.QdrantURLEnv, "http://env-host:6333")).To(Succeed())
		cfg := &config.AppConfig{}
		cfg.Qdrant.URL = "http://yaml-host:6333"
		Expect(cfg.ResolvedQdrantURL()).To(Equal("http://yaml-host:6333"),
			"a deliberate YAML override must beat an env fallback")
	})

	It("treats whitespace-only env values as unset", func() {
		Expect(os.Setenv(config.QdrantURLEnv, "   ")).To(Succeed())
		cfg := &config.AppConfig{}
		Expect(cfg.ResolvedQdrantURL()).To(Equal(""))
	})
})
