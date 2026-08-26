package failover_test

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
)

// Bug #2 — context-cancel cascade.
//
// Before the fix, the candidate loop in StreamHook.Execute reused the same
// parent ctx for every attempt. When the first attempt cleaned up (cancel()
// calls propagating from its derived timeout context, or any other source
// cancelling the parent), every subsequent attempt short-circuited on
// ctx.Done() before reaching the provider's transport. These tests pin down
// the behavioural contract the fix must satisfy.
var _ = Describe("StreamHook context-cascade contract (Bug #2)", func() {
	var (
		manager  *failover.Manager
		registry *provider.Registry
		health   *failover.HealthManager
		sh       *failover.StreamHook
	)

	BeforeEach(func() {
		registry = provider.NewRegistry()
		health = failover.NewHealthManager()
		manager = failover.NewManager(registry, health, 2*time.Second)
		sh = failover.NewStreamHook(manager, nil, "")
	})

	// Contract (1): if the parent ctx was never cancelled but the first
	// provider returns a transport error AND its cleanup cancels the
	// per-attempt ctx (simulating a cleanup goroutine or derived cancel
	// propagating), every remaining candidate must still get a fresh usable
	// ctx and be attempted.
	Context("when first candidate's cleanup cancels the ctx it received", func() {
		var (
			attemptsMadeBy2 int32
			attemptsMadeBy3 int32
		)

		BeforeEach(func() {
			atomic.StoreInt32(&attemptsMadeBy2, 0)
			atomic.StoreInt32(&attemptsMadeBy3, 0)

			// Candidate #1: returns a transport error synchronously, but
			// BEFORE returning it starts a goroutine that will cancel a
			// context derived from the one it received. Today the
			// StreamHook derives timeoutCtx from the parent via
			// WithTimeout, and its deferred cancel() on this path severs
			// only its own derived ctx — but the bug manifests whenever
			// the parent is cancelled from any source between attempts.
			// We simulate that worst-case here by explicitly completing a
			// cancellation on the parent-linked ctx chain.
			registry.Register(&mockStreamProvider{
				name: "cand1",
				streamFn: func(ctx context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
					// Derive a child ctx and cancel it to simulate a
					// cleanup goroutine that raced ahead of the loop.
					// With the bug, subsequent WithTimeout(ctx,...) calls
					// on the shared parent will find ctx.Done() already
					// closed (because our test-double below provides a
					// parent that gets cancelled on first-attempt error).
					return nil, errors.New("cand1 transport error")
				},
			})

			// Candidates 2 and 3 only increment their counters if they
			// receive an uncancelled ctx — the exact observable that
			// Bug #2 breaks. If the parent ctx was cancelled upstream
			// and the hook propagates that cancellation into the
			// per-attempt ctx, the mock returns the ctx error without
			// touching the counter, mirroring real provider transports
			// that abort on `ctx.Err()` before any network I/O.
			registry.Register(&mockStreamProvider{
				name: "cand2",
				streamFn: func(ctx context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					atomic.AddInt32(&attemptsMadeBy2, 1)
					return nil, errors.New("cand2 transport error")
				},
			})

			registry.Register(&mockStreamProvider{
				name: "cand3",
				streamFn: func(ctx context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					atomic.AddInt32(&attemptsMadeBy3, 1)
					return nil, errors.New("cand3 transport error")
				},
			})

			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "cand1", Model: "m1"},
				{Provider: "cand2", Model: "m2"},
				{Provider: "cand3", Model: "m3"},
			})
		})

		It("attempts every remaining candidate with an uncancelled ctx", func() {
			// Create a parent ctx that will be cancelled as soon as the
			// first candidate's (buggy) cleanup propagates. We simulate
			// this by wrapping the base handler so that after it returns
			// an error, it cancels the parent — mirroring the real
			// cleanup race observed in session-1776031172813458779.
			parent, parentCancel := context.WithCancel(context.Background())
			defer parentCancel()

			wrapped := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				p, _ := registry.Get(req.Provider)
				ch, err := p.Stream(ctx, *req)
				if err != nil && req.Provider == "cand1" {
					// Simulate the cleanup cascade: first attempt's
					// failure leads to the parent ctx being cancelled
					// (stand-in for the real cleanup goroutine, TUI Esc
					// race, or derived-cancel propagation).
					parentCancel()
				}
				return ch, err
			}

			handler := sh.Execute(wrapped)
			_, err := handler(parent, &provider.ChatRequest{})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("all providers failed"))

			// The heart of the contract: candidates 2 and 3 must have
			// been reached and their transport code executed, not
			// short-circuited on a pre-cancelled ctx.
			Expect(atomic.LoadInt32(&attemptsMadeBy2)).To(Equal(int32(1)),
				"cand2 must be attempted despite parent ctx cancellation")
			Expect(atomic.LoadInt32(&attemptsMadeBy3)).To(Equal(int32(1)),
				"cand3 must be attempted despite parent ctx cancellation")
		})
	})

	// Contract (2): explicit user-cancel BEFORE the loop starts must abort
	// immediately — no `next` invocations at all. The observable is
	// whether the downstream handler was ever called; a fresh-ctx fix that
	// ignored cancellation entirely would show `next` invoked multiple
	// times here.
	Context("when the parent ctx is cancelled BEFORE the loop starts", func() {
		var nextCallCount int32

		BeforeEach(func() {
			atomic.StoreInt32(&nextCallCount, 0)
			// Two candidates so a cascade fix that loses the
			// parent-cancel signal would call next twice.
			registry.Register(&mockStreamProvider{
				name:     "cand1",
				streamFn: syncErrorStreamFn(errors.New("unused")),
			})
			registry.Register(&mockStreamProvider{
				name:     "cand2",
				streamFn: syncErrorStreamFn(errors.New("unused")),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "cand1", Model: "m1"},
				{Provider: "cand2", Model: "m2"},
			})
		})

		It("returns without invoking next for any candidate", func() {
			parent, cancel := context.WithCancel(context.Background())
			cancel() // explicit user cancel before invocation

			countingNext := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				atomic.AddInt32(&nextCallCount, 1)
				return baseHandler(registry)(ctx, req)
			}

			handler := sh.Execute(countingNext)
			_, err := handler(parent, &provider.ChatRequest{})

			Expect(err).To(HaveOccurred())
			Expect(atomic.LoadInt32(&nextCallCount)).To(Equal(int32(0)),
				"next must not be invoked when parent ctx is already cancelled")
		})
	})

	// Contract (3): a genuine parent deadline that has elapsed must be
	// honoured. Same observable as contract (2): next must not be invoked.
	Context("when the parent ctx deadline has already elapsed", func() {
		var nextCallCount int32

		BeforeEach(func() {
			atomic.StoreInt32(&nextCallCount, 0)
			registry.Register(&mockStreamProvider{
				name:     "cand1",
				streamFn: syncErrorStreamFn(errors.New("unused")),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "cand1", Model: "m1"},
			})
		})

		It("returns without invoking next", func() {
			parent, cancel := context.WithDeadline(context.Background(),
				time.Now().Add(-1*time.Second))
			defer cancel()

			countingNext := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				atomic.AddInt32(&nextCallCount, 1)
				return baseHandler(registry)(ctx, req)
			}

			handler := sh.Execute(countingNext)
			_, err := handler(parent, &provider.ChatRequest{})

			Expect(err).To(HaveOccurred())
			Expect(atomic.LoadInt32(&nextCallCount)).To(Equal(int32(0)),
				"expired deadline must prevent any next invocation")
		})
	})
})
