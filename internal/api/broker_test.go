package api_test

import (
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("SessionBroker", func() {
	var broker *api.SessionBroker

	BeforeEach(func() {
		broker = api.NewSessionBroker()
	})

	Describe("Subscribe", func() {
		It("returns a receive channel and unsubscribe func", func() {
			ch, unsub := broker.Subscribe("sess-1")
			Expect(ch).NotTo(BeNil())
			Expect(unsub).NotTo(BeNil())
			unsub()
		})
	})

	Describe("Publish", func() {
		It("forwards chunks to a subscriber", func() {
			ch, unsub := broker.Subscribe("sess-1")
			defer unsub()

			source := make(chan provider.StreamChunk, 2)
			source <- provider.StreamChunk{Content: "hello"}
			source <- provider.StreamChunk{Done: true}
			close(source)

			done := make(chan struct{})
			go func() {
				broker.Publish("sess-1", source)
				close(done)
			}()

			var received []provider.StreamChunk
			for {
				select {
				case chunk, ok := <-ch:
					if !ok {
						goto collect
					}
					received = append(received, chunk)
				case <-time.After(2 * time.Second):
					Fail("timed out waiting for chunks")
				}
			}
		collect:
			<-done
			Expect(received).To(HaveLen(2))
			Expect(received[0].Content).To(Equal("hello"))
			Expect(received[1].Done).To(BeTrue())
		})

		It("closes subscriber channel when source closes", func() {
			ch, _ := broker.Subscribe("sess-2")

			source := make(chan provider.StreamChunk)
			close(source)

			go broker.Publish("sess-2", source)

			Eventually(ch, 2*time.Second).Should(BeClosed())
		})

		It("forwards chunks to multiple subscribers", func() {
			ch1, unsub1 := broker.Subscribe("sess-3")
			ch2, unsub2 := broker.Subscribe("sess-3")
			defer unsub1()
			defer unsub2()

			source := make(chan provider.StreamChunk, 1)
			source <- provider.StreamChunk{Content: "ping"}
			close(source)

			done := make(chan struct{})
			go func() {
				broker.Publish("sess-3", source)
				close(done)
			}()

			<-done

			Eventually(func() int {
				select {
				case <-ch1:
					return 1
				default:
					return 0
				}
			}, time.Second).Should(Equal(1))

			Eventually(func() int {
				select {
				case <-ch2:
					return 1
				default:
					return 0
				}
			}, time.Second).Should(Equal(1))
		})
	})

	Describe("Unsubscribe", func() {
		// Close-by-sender contract: Unsubscribe removes the subscriber from
		// the fan-out set but does NOT close the channel. The publisher is
		// the sole closer (on source-close), which eliminates the
		// close-during-send race that previously panicked Publish under a
		// concurrent unsubscribe (a Subscribe context cancellation racing the
		// `case sub <- chunk:` send inside deliverWithBackpressure).
		//
		// Pre-fix this spec asserted Eventually(ch).Should(BeClosed()) after
		// Unsubscribe. That contract is what created the dual-closer race
		// (both Unsubscribe and Publish closed the same channel). The
		// channel is now garbage-collected once nothing references it; the
		// only consumer of broker.Subscribe (handleSessionStream) breaks out
		// via ctx.Done() rather than relying on close, so this contract
		// change is invisible to production callers.
		It("removes the subscriber but does not close the channel", func() {
			ch, unsub := broker.Subscribe("sess-4")
			unsub()
			Consistently(ch, 100*time.Millisecond).ShouldNot(BeClosed(),
				"Unsubscribe must NOT close the subscriber channel — "+
					"that creates a close-during-send race against Publish; "+
					"the publisher is the sole closer")
		})
	})

	// Concurrency regression — broker close-during-send race.
	//
	// Pre-fix shape:
	//   - Publish snapshots the subscriber slice under lock, releases the
	//     lock, then iterates calling deliverWithBackpressure (sends on the
	//     subscriber channel WITHOUT the broker lock held).
	//   - Subscribe's unsubscribe func took the broker lock, removed itself
	//     from the slice, and called close(ch) on its channel.
	//
	// Race: between Publish's lock release and the actual `case sub <-
	// chunk:` send, a concurrent unsubscribe (e.g. handleSessionStream's
	// ctx.Done -> defer unsubscribe()) could close(ch). The next send then
	// panics with "send on closed channel".
	//
	// Fix shape: close-by-sender. Unsubscribe drops the subscriber from the
	// fan-out set but does not close the channel; Publish remains the sole
	// closer. The race window is closed structurally — there is no
	// close-during-send because there is no close-by-not-the-sender.
	//
	// This spec hammers the race window: many concurrent subscribers each
	// run a goroutine that subscribes, drains a few chunks, then
	// unsubscribes — all while a Publish loop emits many chunks. Without
	// the fix, one of the goroutines panics on "send on closed channel"
	// inside Publish, which the goroutine guard recovers and surfaces as
	// a test failure. Confirmed RED on HEAD 1e17a88 by running this spec
	// against the unmodified broker.go.
	Describe("close-during-send race (Unsubscribe vs Publish concurrency)", func() {
		It("does not panic when subscribers unsubscribe while Publish is mid-send", func() {
			const sessionID = "sess-race"
			const totalChunks = 500
			const concurrentSubs = 32

			// Buffered source so the publisher can run ahead of any single
			// subscriber. Publish pulls from this and fans out to all
			// active subscribers; the close-during-send race fires inside
			// the fan-out loop, not the source pull.
			source := make(chan provider.StreamChunk, totalChunks+1)
			for i := 0; i < totalChunks; i++ {
				source <- provider.StreamChunk{Content: "c"}
			}
			source <- provider.StreamChunk{Done: true}
			close(source)

			// Recover any panic from goroutines (publisher or subscribers)
			// so the spec fails cleanly instead of crashing the test
			// binary. The pre-fix panic is "send on closed channel" inside
			// Publish.
			panicCh := make(chan any, concurrentSubs+1)
			recoverInto := func(label string) {
				if r := recover(); r != nil {
					panicCh <- fmt.Sprintf("%s panic: %v", label, r)
				}
			}

			pubDone := make(chan struct{})
			go func() {
				defer recoverInto("publisher")
				defer close(pubDone)
				broker.Publish(sessionID, source)
			}()

			// Spawn many subscriber goroutines. Each one subscribes, reads
			// a small handful of chunks (so the publisher's send hits a
			// real receiver and the channel buffer doesn't trivially
			// absorb everything), then unsubscribes — exactly the
			// production lifecycle of a fresh SSE connection that hangs
			// up mid-stream.
			var wg sync.WaitGroup
			for i := 0; i < concurrentSubs; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer recoverInto("subscriber")
					ch, unsub := broker.Subscribe(sessionID)
					// Read a few chunks before unsubscribing. Doing some
					// reads forces the publisher into a state where it's
					// actively delivering to this subscriber; the
					// unsubscribe then races the next send.
					for j := 0; j < 5; j++ {
						select {
						case _, ok := <-ch:
							if !ok {
								return
							}
						case <-time.After(2 * time.Second):
							return
						}
					}
					unsub()
				}()
			}
			wg.Wait()

			// Drain the broker so Publish completes (otherwise the source
			// has chunks the publisher is still trying to deliver to a
			// now-empty subscriber set, which is fine — Publish iterates
			// to source close and returns).
			Eventually(pubDone, 10*time.Second).Should(BeClosed(),
				"Publish must terminate once the source channel closes")

			close(panicCh)
			var panics []string
			for p := range panicCh {
				panics = append(panics, fmt.Sprint(p))
			}
			Expect(panics).To(BeEmpty(),
				"Publish must not panic when subscribers unsubscribe mid-send; "+
					"the close-during-send race is a dual-closer bug "+
					"(both Unsubscribe and Publish closed the same channel)")
		})

		It("survives 20 stress iterations of concurrent Subscribe/Unsubscribe vs Publish without panicking", func() {
			// Iteration count chosen to exceed the brief's "fired on iteration 9 of 20"
			// reproduction window. Each iteration is a fresh broker so iterations are
			// independent; a single panic anywhere fails the spec.
			const iterations = 20
			const subsPerIteration = 16
			const chunksPerIteration = 200

			panicCh := make(chan any, iterations*(subsPerIteration+1))
			recoverInto := func(label string) {
				if r := recover(); r != nil {
					panicCh <- fmt.Sprintf("%s panic: %v", label, r)
				}
			}

			for iter := 0; iter < iterations; iter++ {
				b := api.NewSessionBroker()
				sessionID := fmt.Sprintf("sess-stress-%d", iter)

				source := make(chan provider.StreamChunk, chunksPerIteration+1)
				for j := 0; j < chunksPerIteration; j++ {
					source <- provider.StreamChunk{Content: "c"}
				}
				source <- provider.StreamChunk{Done: true}
				close(source)

				pubDone := make(chan struct{})
				go func() {
					defer recoverInto("publisher")
					defer close(pubDone)
					b.Publish(sessionID, source)
				}()

				var wg sync.WaitGroup
				for i := 0; i < subsPerIteration; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer recoverInto("subscriber")
						ch, unsub := b.Subscribe(sessionID)
						// Vary read counts to widen the race window —
						// some subscribers unsubscribe almost
						// immediately, others read deeper into the
						// stream.
						readCount := 1 + (i % 8)
						for j := 0; j < readCount; j++ {
							select {
							case _, ok := <-ch:
								if !ok {
									return
								}
							case <-time.After(2 * time.Second):
								return
							}
						}
						unsub()
					}()
				}
				wg.Wait()
				Eventually(pubDone, 10*time.Second).Should(BeClosed(),
					"iteration %d: Publish must terminate", iter)
			}

			close(panicCh)
			var panics []string
			for p := range panicCh {
				panics = append(panics, fmt.Sprint(p))
			}
			Expect(panics).To(BeEmpty(),
				"no iteration may panic — close-during-send race is fully closed")
		})
	})

	// Drop #4 — Broker observability.
	//
	// The original Publish loop did `select { case sub <- chunk: default: }`
	// — a fast subscriber got the chunk, a slow one silently dropped it.
	// No instance fired in our 92-second Phase 1d capture (we measured 0
	// drops), but it's a latent hazard once Drops #1 and #2 push chunk rates
	// higher: Anthropic thinking_delta + glm-4.6 reasoning_content add
	// hundreds of additional chunks per turn. The drops were also never
	// observable — neither frontend nor server-side metrics could detect
	// them, so the bug class was structurally invisible.
	//
	// Contract: silent drops are replaced with a bounded-blocking send. A
	// slow subscriber gets a small grace period; if the channel is still
	// full afterwards, the chunk is dropped BUT the drop is recorded on a
	// counter the broker exposes via DroppedCount(). The fan-out semantics
	// are preserved — one slow subscriber must not block siblings.
	Describe("backpressure observability", func() {
		It("starts with a zero drop counter on a fresh broker", func() {
			Expect(broker.DroppedCount()).To(BeZero(),
				"a freshly constructed broker has not dropped any chunks")
		})

		It("increments DroppedCount when a slow subscriber's channel stays full past the grace period", func() {
			// Subscribe but never read — the 64-buffer fills, then drops.
			_, unsub := broker.Subscribe("sess-slow")
			defer unsub()

			source := make(chan provider.StreamChunk, 200)
			// Send more chunks than the subscriber's 64-element buffer can
			// absorb. The first 64 fit; the remainder must drop AND be
			// counted.
			for i := 0; i < 100; i++ {
				source <- provider.StreamChunk{Content: "chunk"}
			}
			source <- provider.StreamChunk{Done: true}
			close(source)

			done := make(chan struct{})
			go func() {
				broker.Publish("sess-slow", source)
				close(done)
			}()
			Eventually(done, 5*time.Second).Should(BeClosed(),
				"Publish must not block forever on a permanently slow subscriber — siblings would starve")

			Expect(broker.DroppedCount()).To(BeNumerically(">", 0),
				"chunks dropped by a slow subscriber MUST be recorded on the broker counter; "+
					"silent drops were the structural-invisibility bug Drop #4 fixes")
		})

		It("delivers all chunks to a fast subscriber even when a parallel slow subscriber is causing drops", func() {
			// Fast subscriber: drains promptly. Slow subscriber: never reads.
			// The fast subscriber MUST receive every chunk — a stuck
			// subscriber must not starve siblings via fan-out blocking.
			fastCh, unsubFast := broker.Subscribe("sess-mixed")
			defer unsubFast()
			_, unsubSlow := broker.Subscribe("sess-mixed")
			defer unsubSlow()

			source := make(chan provider.StreamChunk, 50)
			for i := 0; i < 10; i++ {
				source <- provider.StreamChunk{Content: "c"}
			}
			source <- provider.StreamChunk{Done: true}
			close(source)

			go broker.Publish("sess-mixed", source)

			received := 0
			deadline := time.After(5 * time.Second)
		drain:
			for {
				select {
				case chunk, ok := <-fastCh:
					if !ok {
						break drain
					}
					if chunk.Content != "" || chunk.Done {
						received++
					}
					if chunk.Done {
						break drain
					}
				case <-deadline:
					Fail("fast subscriber stalled — slow sibling must not block fan-out")
				}
			}
			Expect(received).To(Equal(11),
				"the fast subscriber MUST receive every chunk regardless of the slow sibling's backpressure")
		})
	})
})
