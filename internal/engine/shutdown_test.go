// Package engine_test — H3 regression coverage for graceful shutdown of
// per-session HotColdSplitters and in-flight knowledge extractions.
//
// runServe's previous shutdown path called only http.Server.Shutdown,
// which waits for active HTTP handlers to return but does nothing
// about engine-owned goroutines: session splitters' persist workers
// and L3 extraction goroutines were killed mid-flight at process
// exit, orphaning `.tmp` files on disk. After H3 the engine has a
// Shutdown(ctx) that drains both, and runServe calls it after
// server.Shutdown.
package engine_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Engine.Shutdown (H3)", func() {
	It("stops all session splitters and clears the cache", func() {
		eng, _ := newMicroCompactionTestEngine(GinkgoT())
		ctx := context.Background()

		sessions := []string{"h3-session-a", "h3-session-b", "h3-session-c"}
		for _, s := range sessions {
			eng.BuildContextWindowForTesting(ctx, s, "seed")
		}
		for _, s := range sessions {
			Expect(eng.SessionSplitterForTest(s)).NotTo(BeNil(),
				"splitter for %q missing before Shutdown", s)
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		Expect(eng.Shutdown(shutdownCtx)).To(Succeed(), "Shutdown returned error")

		for _, s := range sessions {
			Expect(eng.SessionSplitterForTest(s)).To(BeNil(),
				"splitter for %q still present after Shutdown", s)
		}
	})

	It("is idempotent across two consecutive Shutdown calls", func() {
		eng, _ := newMicroCompactionTestEngine(GinkgoT())
		ctx := context.Background()
		eng.BuildContextWindowForTesting(ctx, "h3-idempotent", "seed")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		Expect(eng.Shutdown(shutdownCtx)).To(Succeed(), "first Shutdown")
		Expect(eng.Shutdown(shutdownCtx)).To(Succeed(), "second Shutdown")
	})

	It("succeeds cleanly when no splitters were ever built", func() {
		eng, _ := newMicroCompactionTestEngine(GinkgoT())

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		Expect(eng.Shutdown(shutdownCtx)).To(Succeed(),
			"Shutdown on empty engine returned an error")
	})
})
