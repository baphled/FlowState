package config_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

// AppConfig timeout helpers were added to expose the engine's previously
// hardcoded StreamTimeout / ToolTimeout / background_output budgets via
// YAML. The contract is intentionally permissive: empty/missing/invalid
// strings collapse to zero so the engine reverts to its compiled-in
// defaults, and a nil receiver also returns zero (App test fixtures
// construct App with Config=nil and exercise the delegate creation path
// that calls these helpers).
var _ = Describe("AppConfig timeout helpers", func() {
	It("ParsedStreamTimeout parses a valid duration string", func() {
		cfg := &config.AppConfig{StreamTimeout: "15m"}
		Expect(cfg.ParsedStreamTimeout()).To(Equal(15 * time.Minute))
	})

	It("ParsedToolTimeout parses a valid duration string", func() {
		cfg := &config.AppConfig{ToolTimeout: "30s"}
		Expect(cfg.ParsedToolTimeout()).To(Equal(30 * time.Second))
	})

	It("ParsedBackgroundOutputTimeout parses a valid duration string", func() {
		cfg := &config.AppConfig{BackgroundOutputTimeout: "5m"}
		Expect(cfg.ParsedBackgroundOutputTimeout()).To(Equal(5 * time.Minute))
	})

	It("returns zero when the field is empty", func() {
		cfg := &config.AppConfig{}
		Expect(cfg.ParsedStreamTimeout()).To(BeZero())
		Expect(cfg.ParsedToolTimeout()).To(BeZero())
		Expect(cfg.ParsedBackgroundOutputTimeout()).To(BeZero())
	})

	It("returns zero (and does not panic) for an invalid duration string", func() {
		cfg := &config.AppConfig{StreamTimeout: "not-a-duration"}
		Expect(cfg.ParsedStreamTimeout()).To(BeZero())
	})

	It("returns zero on a nil receiver", func() {
		var cfg *config.AppConfig
		Expect(cfg.ParsedStreamTimeout()).To(BeZero())
		Expect(cfg.ParsedToolTimeout()).To(BeZero())
		Expect(cfg.ParsedBackgroundOutputTimeout()).To(BeZero())
	})
})
