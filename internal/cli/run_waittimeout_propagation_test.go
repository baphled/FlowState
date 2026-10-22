package cli_test

// Item 7 — compression.session_memory.wait_timeout propagation.
//
// Pre-Item 7 the pre-exit block used a hard-coded 35s constant in
// internal/cli/run.go. This file pins the replacement contract: the
// CLI must honour the value under CompressionConfig.SessionMemory.
// WaitTimeout when it is > 0, and otherwise fall back to the documented
// default. Config-level validation (reject <= 0) lives in
// internal/context/compression_config_test.go.

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/config"
	flowctx "github.com/baphled/flowstate/internal/context"
)

var _ = Describe("ResolveBackgroundExtractionWait", func() {
	// UsesConfiguredValue: a custom YAML value reaches the CLI wait
	// helper without ever colliding with the 35s literal.
	It("returns the configured CompressionConfig.SessionMemory.WaitTimeout when > 0", func() {
		cfg := &config.AppConfig{Compression: flowctx.DefaultCompressionConfig()}
		cfg.Compression.SessionMemory.WaitTimeout = 90 * time.Second
		a := &app.App{Config: cfg}

		got := cli.ResolveBackgroundExtractionWaitForTest(a)

		Expect(got).To(Equal(90*time.Second), "wait timeout = %v; want 90s", got)
	})

	// FallsBackWhenConfigNil: the embedded-test path where the App
	// has no loaded AppConfig. The helper must not panic and must
	// return the documented default.
	It("falls back to the default when AppConfig is nil", func() {
		a := &app.App{}

		got := cli.ResolveBackgroundExtractionWaitForTest(a)

		Expect(got).To(Equal(cli.DefaultBackgroundExtractionWaitForTest),
			"wait timeout = %v; want default %v", got, cli.DefaultBackgroundExtractionWaitForTest)
	})

	// FallsBackWhenZeroInConfig: guards against a caller constructing
	// a CompressionConfig by hand (skipping DefaultCompressionConfig)
	// and leaving WaitTimeout at its zero value. The CLI must treat
	// that as "unspecified" rather than "wait zero".
	It("falls back to the default when WaitTimeout is zero in config", func() {
		cfg := &config.AppConfig{Compression: flowctx.CompressionConfig{}}
		a := &app.App{Config: cfg}

		got := cli.ResolveBackgroundExtractionWaitForTest(a)

		Expect(got).To(Equal(cli.DefaultBackgroundExtractionWaitForTest),
			"wait timeout = %v; want default %v", got, cli.DefaultBackgroundExtractionWaitForTest)
	})
})
