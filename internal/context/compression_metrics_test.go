package context_test

import (
	"bytes"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// T19 CompressionMetrics specification.
//
// CompressionMetrics is an optional counter set attached to the
// WindowBuilder. The plan's enforced counters are MicroCompactionCount,
// AutoCompactionCount, and TokensSaved; slog.Info emits them on every
// Build() call. Specs assert the counters exist, are attached via
// WithMetrics, and that the metrics log record names the expected keys
// so downstream log processors can rely on the schema.
//
// A compacted-view cache hit counter was deliberately excluded: the
// governing ADR (View-Only Context Compaction §3, "Caching Is a Permitted
// Extension") classifies the cache as out-of-scope for this delivery.
// Any future cache wiring must add both the counter and a real increment
// site together.
var _ = Describe("CompressionMetrics & WindowBuilder.WithMetrics", func() {
	It("zero-valued CompressionMetrics is ready to attach without construction helpers", func() {
		var m contextpkg.CompressionMetrics
		Expect(m.MicroCompactionCount).To(Equal(0))
		Expect(m.AutoCompactionCount).To(Equal(0))
		Expect(m.TokensSaved).To(Equal(0))
	})

	It("WithMetrics returns the receiver (fluent constructor)", func() {
		builder := contextpkg.NewWindowBuilder(stubCounter{})
		m := &contextpkg.CompressionMetrics{}
		Expect(builder.WithMetrics(m)).To(BeIdenticalTo(builder))
	})

	It("Build logs every metrics key the plan requires (and never logs cache_hits)", func() {
		prev := slog.Default()
		DeferCleanup(func() { slog.SetDefault(prev) })

		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
		slog.SetDefault(slog.New(handler))

		metrics := &contextpkg.CompressionMetrics{
			MicroCompactionCount: 3,
			AutoCompactionCount:  1,
			TokensSaved:          420,
		}
		builder := contextpkg.NewWindowBuilder(stubCounter{}).WithMetrics(metrics)

		store, err := recall.NewFileContextStore(GinkgoT().TempDir()+"/ctx.json", "test-model")
		Expect(err).NotTo(HaveOccurred())
		store.Append(provider.Message{Role: "user", Content: "hi"})

		manifest := &agent.Manifest{
			Instructions:      agent.Instructions{SystemPrompt: "sys"},
			ContextManagement: agent.DefaultContextManagement(),
		}
		_ = builder.Build(manifest, store, 10_000)

		out := buf.String()
		for _, key := range []string{"micro_compaction_count", "auto_compaction_count", "tokens_saved"} {
			Expect(out).To(ContainSubstring(key),
				"Build did not log metrics key %q; log was %s", key, out)
		}
		Expect(out).NotTo(ContainSubstring("cache_hits"),
			"compacted-view cache is out of scope per ADR — View-Only Context Compaction §3")
	})

	It("Build does not log metrics keys when no CompressionMetrics is attached", func() {
		prev := slog.Default()
		DeferCleanup(func() { slog.SetDefault(prev) })

		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
		slog.SetDefault(slog.New(handler))

		builder := contextpkg.NewWindowBuilder(stubCounter{})
		store, err := recall.NewFileContextStore(GinkgoT().TempDir()+"/ctx.json", "test-model")
		Expect(err).NotTo(HaveOccurred())
		store.Append(provider.Message{Role: "user", Content: "hi"})

		manifest := &agent.Manifest{
			Instructions:      agent.Instructions{SystemPrompt: "sys"},
			ContextManagement: agent.DefaultContextManagement(),
		}
		_ = builder.Build(manifest, store, 10_000)

		Expect(buf.String()).NotTo(ContainSubstring("micro_compaction_count"),
			"Build logged metrics key without WithMetrics")
	})
})
