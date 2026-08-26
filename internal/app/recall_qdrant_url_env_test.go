package app

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

// QDRANT_URL env-var fallback at the app seam.
//
// Smoke discovery on 2026-04-27: `flowstate run` warned operators to set
// QDRANT_URL but only ever read cfg.Qdrant.URL from YAML. Operators who
// followed the warning landed in "recall broker disabled" with no further
// signal — a contract lie versus the vault-server binary which already
// honoured QDRANT_URL.
//
// These specs pin the seam between config-resolution and broker
// initialisation: the broker, distiller, and learning-store init paths
// must all consult cfg.ResolvedQdrantURL() so a QDRANT_URL-only operator
// gets the same wiring as a YAML-only operator.
var _ = Describe("flowstate run honours QDRANT_URL env var", func() {
	BeforeEach(func() {
		_ = os.Unsetenv(config.QdrantURLEnv)
	})

	AfterEach(func() {
		_ = os.Unsetenv(config.QdrantURLEnv)
	})

	It("resolves the env var when YAML qdrant.url is empty", func() {
		Expect(os.Setenv(config.QdrantURLEnv, "http://env-only:6333")).To(Succeed())
		cfg := &config.AppConfig{} // YAML deliberately empty.
		Expect(cfg.ResolvedQdrantURL()).To(Equal("http://env-only:6333"),
			"resolver must surface QDRANT_URL when YAML qdrant.url is empty — operators expect parity with the vault-server binary")
	})

	It("YAML wins when both YAML and env are populated", func() {
		Expect(os.Setenv(config.QdrantURLEnv, "http://env-host:6333")).To(Succeed())
		cfg := &config.AppConfig{}
		cfg.Qdrant.URL = "http://yaml-host:6333"
		Expect(cfg.ResolvedQdrantURL()).To(Equal("http://yaml-host:6333"),
			"a deliberate YAML qdrant.url must beat the env-var fallback")
	})

	It("the broker init path consults the resolver, not the raw YAML field", func() {
		// Pin the actual wiring fix: every site that gates Qdrant
		// initialisation must consult cfg.ResolvedQdrantURL() rather
		// than reading cfg.Qdrant.URL directly. We probe this by
		// setting only the env var, asserting the resolver returns
		// non-empty, and then asserting that the in-package gating
		// helper agrees.
		Expect(os.Setenv(config.QdrantURLEnv, "http://env-only:6333")).To(Succeed())
		cfg := &config.AppConfig{}

		Expect(cfg.ResolvedQdrantURL()).NotTo(BeEmpty())
		Expect(qdrantEnabled(cfg)).To(BeTrue(),
			"qdrantEnabled must mirror the resolver — every gating site funnels through the same helper, so an env-only operator gets the broker wired")
	})

	It("the broker init path stays disabled when neither YAML nor env are set", func() {
		cfg := &config.AppConfig{}
		Expect(cfg.ResolvedQdrantURL()).To(Equal(""))
		Expect(qdrantEnabled(cfg)).To(BeFalse(),
			"with no Qdrant URL anywhere, the gating helper must report disabled so the broker takes the Qdrant-less branch")
	})
})
