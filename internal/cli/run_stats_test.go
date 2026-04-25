package cli_test

// Item 2 — --stats flag for `flowstate run`.
//
// Ephemeral CLI processes do not share a Prometheus registry with
// `flowstate serve`, so the compression counters visible at /metrics
// only reflect the serve engine. The --stats flag is the ad-hoc
// escape hatch: print a one-line summary to stderr after the run so
// operators can see per-turn compression numbers without standing up
// a metrics scrape.

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/cli"
	flowctx "github.com/baphled/flowstate/internal/context"
)

var _ = Describe("WriteCompressionStats", func() {
	// EmitsAllFields pins the compact one-line format: the line carries
	// all four compression counters in a fixed order, so a grep-friendly
	// consumer can rely on key=value placement.
	It("emits all four counters in the compact one-line format", func() {
		metrics := flowctx.CompressionMetrics{
			MicroCompactionCount: 3,
			AutoCompactionCount:  1,
			TokensSaved:          250,
			OverheadTokens:       40,
		}

		var buf bytes.Buffer
		cli.WriteCompressionStatsForTest(&buf, metrics)

		got := buf.String()
		for _, want := range []string{
			"micro=3",
			"auto=1",
			"tokens_saved=250",
			"overhead=40",
		} {
			Expect(got).To(ContainSubstring(want),
				"stats line missing %q; got %q", want, got)
		}
		Expect(got).To(HavePrefix("compression:"),
			"stats line must start with compression:; got %q", got)
		Expect(got).To(HaveSuffix("\n"),
			"stats line must end with newline for pipeline friendliness; got %q", got)
	})

	// ZeroMetrics exercises the zero-value path: a run with no
	// compression activity still emits a consistent line so operators
	// scripting against --stats get reliable key=value pairs whether or
	// not compression fired.
	It("emits a consistent line on zero metrics", func() {
		var buf bytes.Buffer
		cli.WriteCompressionStatsForTest(&buf, flowctx.CompressionMetrics{})

		got := buf.String()
		for _, want := range []string{
			"micro=0",
			"auto=0",
			"tokens_saved=0",
			"overhead=0",
		} {
			Expect(got).To(ContainSubstring(want),
				"stats line missing %q on zero metrics; got %q", want, got)
		}
	})
})
