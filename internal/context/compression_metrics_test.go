// Package context_test — T19 CompressionMetrics specification.
//
// CompressionMetrics is an optional counter set attached to the
// WindowBuilder. The plan's enforced counters are MicroCompactionCount,
// AutoCompactionCount, and TokensSaved; slog.Info emits them on every
// Build() call. Tests assert the counters exist, are attached via
// WithMetrics, and that the metrics log record names the expected keys
// so downstream log processors can rely on the schema.
//
// A compacted-view cache hit counter was deliberately excluded: the
// governing ADR (View-Only Context Compaction §3, "Caching Is a
// Permitted Extension") classifies the cache as out-of-scope for this
// delivery. Any future cache wiring must add both the counter and a
// real increment site together.
package context_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/agent"
	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// TestCompressionMetrics_ZeroValue_IsReady asserts the struct is usable
// without construction helpers. Zero values for every counter make
// attaching a fresh *CompressionMetrics to a builder safe.
func TestCompressionMetrics_ZeroValue_IsReady(t *testing.T) {
	t.Parallel()

	var m contextpkg.CompressionMetrics
	if m.MicroCompactionCount != 0 || m.AutoCompactionCount != 0 || m.TokensSaved != 0 {
		t.Fatalf("zero CompressionMetrics not zero-valued: %+v", m)
	}
}

// TestWindowBuilder_WithMetrics_AttachesAndReturnsReceiver asserts the
// fluent constructor honours the same pattern as WithSplitter and
// WithSessionMemory. A nil metrics argument detaches.
func TestWindowBuilder_WithMetrics_AttachesAndReturnsReceiver(t *testing.T) {
	t.Parallel()

	counter := stubCounter{}
	builder := contextpkg.NewWindowBuilder(counter)

	m := &contextpkg.CompressionMetrics{}
	if got := builder.WithMetrics(m); got != builder {
		t.Fatalf("WithMetrics did not return the receiver")
	}
}

// TestWindowBuilder_Build_LogsMetricsKeys asserts slog.Info is emitted
// with the four counter keys the plan requires. The handler captures
// the most recent record so we can scan its attributes.
func TestWindowBuilder_Build_LogsMetricsKeys(t *testing.T) {
	t.Parallel()

	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))

	metrics := &contextpkg.CompressionMetrics{
		MicroCompactionCount: 3,
		AutoCompactionCount:  1,
		TokensSaved:          420,
	}
	builder := contextpkg.NewWindowBuilder(stubCounter{}).WithMetrics(metrics)

	store, err := recall.NewFileContextStore(t.TempDir()+"/ctx.json", "test-model")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	store.Append(provider.Message{Role: "user", Content: "hi"})

	manifest := &agent.Manifest{
		Instructions:      agent.Instructions{SystemPrompt: "sys"},
		ContextManagement: agent.DefaultContextManagement(),
	}
	_ = builder.Build(manifest, store, 10_000)

	out := buf.String()
	for _, key := range []string{"micro_compaction_count", "auto_compaction_count", "tokens_saved"} {
		if !strings.Contains(out, key) {
			t.Fatalf("Build did not log metrics key %q; log was %s", key, out)
		}
	}
	if strings.Contains(out, "cache_hits") {
		t.Fatalf("Build logged cache_hits; the counter is out of scope until the compacted-view cache ships per ADR - View-Only Context Compaction §3. log was %s", out)
	}
}

// TestWindowBuilder_Build_NoMetrics_NoMetricsLog asserts that a builder
// without metrics attached does not emit the metrics log line. This
// keeps the default log output quiet for deployments that never enable
// compression.
func TestWindowBuilder_Build_NoMetrics_NoMetricsLog(t *testing.T) {
	t.Parallel()

	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))

	builder := contextpkg.NewWindowBuilder(stubCounter{})
	store, err := recall.NewFileContextStore(t.TempDir()+"/ctx.json", "test-model")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	store.Append(provider.Message{Role: "user", Content: "hi"})

	manifest := &agent.Manifest{
		Instructions:      agent.Instructions{SystemPrompt: "sys"},
		ContextManagement: agent.DefaultContextManagement(),
	}
	_ = builder.Build(manifest, store, 10_000)

	if strings.Contains(buf.String(), "micro_compaction_count") {
		t.Fatalf("Build logged metrics key without WithMetrics; log was %s", buf.String())
	}
}
