package engine_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
)

// P2 (September 2026) — cap tool results before persistence. Tool
// results larger than the 8K cap must be truncated head-first with a
// deterministic truncation marker so oversized payloads (coordination
// store reads, search_nodes-style results) cannot bloat the persisted
// context window.
// Context remediation Task 2 (September 2026) — cap tool results
// appended to the live in-flight messages slice mid-loop. Mirrors the
// persist cap at 3x so the two compose without double markers.
var _ = Describe("capInFlightToolResult", func() {
	It("passes content at or under the cap through unchanged", func() {
		content := strings.Repeat("a", engine.MaxInFlightToolResultChars)
		Expect(engine.CapInFlightToolResultForTest(content)).To(Equal(content))
	})

	It("truncates oversized content to the cap plus a truncation marker", func() {
		content := strings.Repeat("x", 100*1024)
		got := engine.CapInFlightToolResultForTest(content)

		Expect(len(got)).To(BeNumerically("<=", engine.MaxInFlightToolResultChars+256),
			"a 100K-char result must enter the in-flight slice as cap + marker, never the raw payload")
		Expect(got).To(ContainSubstring("[truncated: tool result exceeded"),
			"the deterministic marker must signal truncation to the model and diagnostics")
	})

	It("is idempotent for already-capped content", func() {
		once := engine.CapInFlightToolResultForTest(strings.Repeat("y", 50*1024))
		Expect(engine.CapInFlightToolResultForTest(once)).To(Equal(once))
	})

	It("leaves persist-capped content untouched without a double marker", func() {
		persistCapped := engine.CapPersistedToolResultForTest(strings.Repeat("z", 50*1024))
		Expect(persistCapped).To(ContainSubstring("[truncated: tool result exceeded"))
		got := engine.CapInFlightToolResultForTest(persistCapped)
		Expect(got).To(Equal(persistCapped),
			"persist cap (8K) then in-flight cap (24K) must compose idempotently")
	})
})

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
