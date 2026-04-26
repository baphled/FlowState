package config_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

// AppConfig.SystemPromptBudget exists so operators can override the
// model-context fallback the engine returns when no provider/model
// resolver is wired. The historical 4096 default silently truncated
// ~70% of an 11-skill always-active system prompt; ResolvedSystemPromptBudget
// honours an env var > YAML field > zero (engine inherits 16K default).
var _ = Describe("AppConfig.ResolvedSystemPromptBudget", func() {
	BeforeEach(func() {
		_ = os.Unsetenv(config.SystemPromptBudgetEnv)
	})

	AfterEach(func() {
		_ = os.Unsetenv(config.SystemPromptBudgetEnv)
	})

	It("returns zero on a nil receiver so engine inherits the compiled-in default", func() {
		var cfg *config.AppConfig
		Expect(cfg.ResolvedSystemPromptBudget()).To(BeZero())
	})

	It("returns zero when neither env nor YAML carry a positive value", func() {
		cfg := &config.AppConfig{}
		Expect(cfg.ResolvedSystemPromptBudget()).To(BeZero())
	})

	It("returns the YAML field when set", func() {
		cfg := &config.AppConfig{SystemPromptBudget: 32768}
		Expect(cfg.ResolvedSystemPromptBudget()).To(Equal(32768))
	})

	It("env var overrides the YAML field", func() {
		Expect(os.Setenv(config.SystemPromptBudgetEnv, "65536")).To(Succeed())
		cfg := &config.AppConfig{SystemPromptBudget: 32768}
		Expect(cfg.ResolvedSystemPromptBudget()).To(Equal(65536))
	})

	It("falls back to YAML when env value is invalid", func() {
		Expect(os.Setenv(config.SystemPromptBudgetEnv, "not-an-int")).To(Succeed())
		cfg := &config.AppConfig{SystemPromptBudget: 32768}
		Expect(cfg.ResolvedSystemPromptBudget()).To(Equal(32768))
	})

	It("falls back to zero when env value is non-positive", func() {
		Expect(os.Setenv(config.SystemPromptBudgetEnv, "0")).To(Succeed())
		cfg := &config.AppConfig{}
		Expect(cfg.ResolvedSystemPromptBudget()).To(BeZero())
	})

	It("ignores empty env values and uses YAML", func() {
		Expect(os.Setenv(config.SystemPromptBudgetEnv, "")).To(Succeed())
		cfg := &config.AppConfig{SystemPromptBudget: 12345}
		Expect(cfg.ResolvedSystemPromptBudget()).To(Equal(12345))
	})
})
