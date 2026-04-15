package context_test

import (
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
	})
})
