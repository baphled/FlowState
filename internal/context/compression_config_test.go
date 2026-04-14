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
})
