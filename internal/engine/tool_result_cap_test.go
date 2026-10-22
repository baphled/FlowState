//go:build e2e

package engine_test

import (
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
)

// P2 (September 2026) — cap tool results before persistence. Tool
// results larger than the 8K cap must be truncated head-first with a
// deterministic truncation marker so oversized payloads (coordination
// store reads, search_nodes-style results) cannot bloat the persisted
// context window.
var _ = Describe("capPersistedToolResult", func() {
	It("passes content at or under the cap through unchanged", func() {
		content := strings.Repeat("a", engine.MaxPersistedToolResultChars)
		Expect(engine.CapPersistedToolResultForTest(content)).To(Equal(content))
	})

	It("truncates oversized content to the cap plus a truncation marker", func() {
		content := strings.Repeat("x", 100*1024)
		got := engine.CapPersistedToolResultForTest(content)

		Expect(len(got)).To(BeNumerically("<=", engine.MaxPersistedToolResultChars+256),
			"a 100K-char result must persist as cap + marker, never the raw payload")
		Expect(got).To(ContainSubstring("[truncated: tool result exceeded"),
			"the deterministic marker must signal truncation to the model and diagnostics")
	})

	It("is idempotent for already-capped content", func() {
		once := engine.CapPersistedToolResultForTest(strings.Repeat("y", 50*1024))
		Expect(engine.CapPersistedToolResultForTest(once)).To(Equal(once))
	})
})

// Benchmarks for capPersistedToolResult (migrated from zz_perffix_bench_test.go).
// Package-level: the canonical file is engine_test, so drive the
// exported for-test wrapper instead of the unexported core.

func benchmarkCapPersisted(b *testing.B, s string) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = engine.CapPersistedToolResultForTest(s)
	}
}

func BenchmarkCapPersistedToolResultSmall(b *testing.B) {
	benchmarkCapPersisted(b, strings.Repeat("x", 1024))
}

func BenchmarkCapPersistedToolResultLarge(b *testing.B) {
	benchmarkCapPersisted(b, strings.Repeat("x", 1<<20))
}
