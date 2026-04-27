package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("resolveAgentName", func() {
	It("returns the trimmed agent when non-empty", func() {
		Expect(resolveAgentName("  executor ", "fallback")).To(Equal("executor"))
	})

	It("returns the fallback when agent is empty", func() {
		Expect(resolveAgentName("", "executor")).To(Equal("executor"))
	})

	It("returns the fallback when agent is whitespace-only", func() {
		Expect(resolveAgentName("   ", "executor")).To(Equal("executor"))
	})

	It("falls back to historical worker default when both inputs are empty", func() {
		Expect(resolveAgentName("", "")).To(Equal("worker"))
	})
})
