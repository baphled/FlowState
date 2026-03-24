package plan_test

import (
	. "github.com/onsi/ginkgo/v2"
)

// NOTE: mockStreamer is defined in harness_test.go. Use a local stub or rename if needed for isolation.

var _ = Describe("IncrementalGenerator", func() {
	// Test variables will be defined per test block as needed

	BeforeEach(func() {
		// streamer = &mockStreamer{...} // Not needed for skeleton; will define per test
		// gen = &plan.IncrementalGenerator{Streamer: streamer}
	})

	It("generates all phases in order", func() {
		// TODO: Implement test
	})

	It("validates non-empty output per phase", func() {
		// TODO: Implement test
	})

	It("retries on empty phase output", func() {
		// TODO: Implement test
	})

	It("aggregates final plan correctly", func() {
		// TODO: Implement test
	})

	It("handles context cancellation", func() {
		// TODO: Implement test
	})
})
