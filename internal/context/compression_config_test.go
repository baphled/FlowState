package context_test

import (
	"math"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	flowctx "github.com/baphled/flowstate/internal/context"
)

var _ = Describe("CompressionConfig", func() {
	Describe("DefaultCompressionConfig", func() {
		It("disables every layer by default", func() {
			cfg := flowctx.DefaultCompressionConfig()

			Expect(cfg.MicroCompaction.Enabled).To(BeFalse())
			Expect(cfg.AutoCompaction.Enabled).To(BeFalse())
			Expect(cfg.SessionMemory.Enabled).To(BeFalse())
		})

		It("sets auto-compaction threshold to 0.75", func() {
			cfg := flowctx.DefaultCompressionConfig()

			// 0.75 is the bound default shared with
			// internal/agent/manifest.go CompactionThreshold.
			Expect(cfg.AutoCompaction.Threshold).To(Equal(0.75))
		})

		It("seeds micro-compaction defaults", func() {
			cfg := flowctx.DefaultCompressionConfig()

			Expect(cfg.MicroCompaction.HotTailSize).To(Equal(5))
			Expect(cfg.MicroCompaction.TokenThreshold).To(Equal(1000))
			Expect(cfg.MicroCompaction.PlaceholderTokens).To(Equal(50))
			Expect(cfg.MicroCompaction.StorageDir).To(Equal("~/.flowstate/compacted"))
		})

		It("seeds session-memory defaults", func() {
			cfg := flowctx.DefaultCompressionConfig()

			Expect(cfg.SessionMemory.StorageDir).To(Equal("~/.flowstate/session-memory"))
		})

		// Item 7 — wait_timeout bounds the pre-exit block for the L3
		// knowledge-extraction goroutine. The previous constant in
		// internal/cli/run.go used 35 seconds (30s extractor timeout +
		// 5s disk-write margin); that remains the default when the
		// operator does not override it in YAML.
		It("defaults session-memory wait timeout to 35s", func() {
			cfg := flowctx.DefaultCompressionConfig()

			Expect(cfg.SessionMemory.WaitTimeout).To(Equal(35 * time.Second))
		})
	})

	// H4 — Reject `hot_tail_size: 0` at config validation when
	// micro-compaction is enabled. findColdBoundary treats zero as
	// "everything is cold", which quietly spills the entire window on
	// every turn. Catch it at load time instead.
	Describe("Validate", func() {
		It("accepts the default configuration", func() {
			cfg := flowctx.DefaultCompressionConfig()

			Expect(cfg.Validate()).To(Succeed())
		})

		It("accepts micro-compaction enabled with a sensible hot tail", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.MicroCompaction.Enabled = true
			cfg.MicroCompaction.HotTailSize = 3

			Expect(cfg.Validate()).To(Succeed())
		})

		// Item 4 — idle_ttl bounds how long an unused session splitter
		// is kept in the engine cache before the background sweeper
		// evicts it. A zero or negative value would either evict every
		// entry on every sweep pass or never evict any, neither of
		// which is the operator's intent. Catch the misconfiguration
		// at load time.
		It("defaults micro-compaction idle_ttl to 30 minutes", func() {
			cfg := flowctx.DefaultCompressionConfig()

			Expect(cfg.MicroCompaction.IdleTTL).To(Equal(30 * time.Minute))
		})

		It("rejects zero idle_ttl when micro-compaction is enabled", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.MicroCompaction.Enabled = true
			cfg.MicroCompaction.HotTailSize = 3
			cfg.MicroCompaction.IdleTTL = 0

			err := cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("idle_ttl"))
			Expect(err.Error()).To(ContainSubstring("micro_compaction"))
		})

		It("rejects negative idle_ttl when micro-compaction is enabled", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.MicroCompaction.Enabled = true
			cfg.MicroCompaction.HotTailSize = 3
			cfg.MicroCompaction.IdleTTL = -1 * time.Second

			Expect(cfg.Validate()).To(HaveOccurred())
		})

		It("ignores idle_ttl when micro-compaction is disabled", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.MicroCompaction.Enabled = false
			cfg.MicroCompaction.IdleTTL = 0

			Expect(cfg.Validate()).To(Succeed())
		})

		It("accepts hot_tail_size of zero when micro-compaction is disabled", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.MicroCompaction.Enabled = false
			cfg.MicroCompaction.HotTailSize = 0

			Expect(cfg.Validate()).To(Succeed())
		})

		It("rejects hot_tail_size of zero when micro-compaction is enabled", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.MicroCompaction.Enabled = true
			cfg.MicroCompaction.HotTailSize = 0

			err := cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("hot_tail_size"))
			Expect(err.Error()).To(ContainSubstring("micro_compaction"))
		})

		It("rejects negative hot_tail_size when micro-compaction is enabled", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.MicroCompaction.Enabled = true
			cfg.MicroCompaction.HotTailSize = -1

			Expect(cfg.Validate()).To(HaveOccurred())
		})

		// M5 — auto-compaction threshold must be constrained to the
		// (0.0, 1.0] interval. Silently accepting 0.0001 or 1.5 (both
		// representable, neither useful) produces a layer that fires
		// every turn or never fires, with no diagnostic. NaN is a
		// further trap since comparisons against it are always false;
		// the auto-compaction ratio check would therefore never
		// trigger. Catching these at load keeps the runtime path free
		// of silent misconfigurations.
		DescribeTable("rejects out-of-range auto-compaction thresholds",
			func(threshold float64, wantSubstring string) {
				cfg := flowctx.DefaultCompressionConfig()
				cfg.AutoCompaction.Enabled = true
				cfg.AutoCompaction.Threshold = threshold

				err := cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("auto_compaction"))
				Expect(err.Error()).To(ContainSubstring("threshold"))
				if wantSubstring != "" {
					Expect(err.Error()).To(ContainSubstring(wantSubstring))
				}
			},
			Entry("zero threshold", 0.0, ""),
			Entry("negative threshold", -0.1, ""),
			Entry("threshold above 1.0", 1.5, ""),
			Entry("NaN threshold", math.NaN(), "NaN"),
		)

		It("accepts a threshold of 0.5 when auto-compaction is enabled", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.AutoCompaction.Enabled = true
			cfg.AutoCompaction.Threshold = 0.5

			Expect(cfg.Validate()).To(Succeed())
		})

		It("accepts a threshold of exactly 1.0 when auto-compaction is enabled", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.AutoCompaction.Enabled = true
			cfg.AutoCompaction.Threshold = 1.0

			Expect(cfg.Validate()).To(Succeed())
		})

		It("ignores out-of-range thresholds when auto-compaction is disabled", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.AutoCompaction.Enabled = false
			cfg.AutoCompaction.Threshold = 1.5

			Expect(cfg.Validate()).To(Succeed())
		})

		// M6 — the knowledge extractor issues a chat request per turn
		// to distill session memory. Ollama (and every OpenAI-compat
		// provider) rejects empty `model` with HTTP 400, and silently
		// defaulting to a provider-specific fallback has historically
		// hidden the misconfiguration. Require the model explicitly
		// at load when the feature is enabled.
		It("rejects session memory enabled without an explicit model", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.SessionMemory.Enabled = true
			cfg.SessionMemory.Model = ""

			err := cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("session_memory"))
			Expect(err.Error()).To(ContainSubstring("model"))
		})

		// The session_memory.model validation previously stated the
		// constraint ("must be a non-empty chat model identifier")
		// without pointing operators at the config key they need to
		// edit. Operators reading the error in isolation had to trace
		// it back to the file format to learn where to apply the fix.
		// Pin the remediation hint so the error stays actionable.
		It("names the config key when session memory model is missing", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.SessionMemory.Enabled = true
			cfg.SessionMemory.Model = ""

			err := cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("compression.session_memory.model"))
		})

		It("rejects a whitespace-only session memory model", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.SessionMemory.Enabled = true
			cfg.SessionMemory.Model = "   "

			Expect(cfg.Validate()).To(HaveOccurred())
		})

		It("accepts session memory enabled with an explicit model", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.SessionMemory.Enabled = true
			cfg.SessionMemory.Model = "llama3.1"

			Expect(cfg.Validate()).To(Succeed())
		})

		It("ignores missing session memory model when session memory is disabled", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.SessionMemory.Enabled = false
			cfg.SessionMemory.Model = ""

			Expect(cfg.Validate()).To(Succeed())
		})

		// Item 7 — wait_timeout must be positive when session-memory is
		// enabled. A zero or negative value would either skip the wait
		// entirely (orphaning the extractor at os.Exit) or be rejected
		// by the engine as nonsensical; catching it at load keeps the
		// failure mode explicit.
		It("rejects zero session-memory wait timeout when session-memory is enabled", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.SessionMemory.Enabled = true
			cfg.SessionMemory.Model = "llama3.1"
			cfg.SessionMemory.WaitTimeout = 0

			err := cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("wait_timeout"))
			Expect(err.Error()).To(ContainSubstring("session_memory"))
		})

		It("rejects negative session-memory wait timeout when session-memory is enabled", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.SessionMemory.Enabled = true
			cfg.SessionMemory.Model = "llama3.1"
			cfg.SessionMemory.WaitTimeout = -1 * time.Second

			Expect(cfg.Validate()).To(HaveOccurred())
		})

		It("ignores session-memory wait timeout when session-memory is disabled", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.SessionMemory.Enabled = false
			cfg.SessionMemory.WaitTimeout = 0

			Expect(cfg.Validate()).To(Succeed())
		})

		It("accepts a custom session-memory wait timeout when session-memory is enabled", func() {
			cfg := flowctx.DefaultCompressionConfig()
			cfg.SessionMemory.Enabled = true
			cfg.SessionMemory.Model = "llama3.1"
			cfg.SessionMemory.WaitTimeout = 60 * time.Second

			Expect(cfg.Validate()).To(Succeed())
		})
	})
})
