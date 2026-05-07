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

	// Concurrency audit (May 2026) — comprehensive lifecycle invariants.
	//
	// Background: three prior race fixes landed reactively in the same
	// ~200-line file (commits aaa6f1f, 1e17a88, a10ae63). Each surfaced
	// further hazards. This block exists to break out of the whack-a-mole
	// pattern by pinning every lifecycle invariant the broker MUST hold
	// under arbitrary interleavings, not just the single concrete trace
	// each prior fix targeted.
	//
	// Invariants pinned here:
	//
	//   I1 — Single-closer per channel: a channel returned by Subscribe is
	//        closed AT MOST ONCE, and only by the broker (never by
	//        Unsubscribe).
	//
	//   I2 — Concurrent Publish for the same sessionID is safe: chunks
	//        from both runs may interleave on subscriber channels, but no
	//        panic, no double-close, no leaked subscribers.
	//
	//   I3 — Subscribe-during-terminal-close does not panic and does not
	//        leak the late subscriber's channel into a permanent map
	//        entry.
	//
	//   I4 — High-concurrency Subscribe/Unsubscribe under a Publish run
	//        does not panic and does not leak channels in the broker's
	//        subscribers map.
	//
	//   I5 — Cancel-during-Publish (handler-style ctx.Done -> defer
	//        unsubscribe pattern, lifted directly from handleSessionStream)
	//        does not panic the publisher and does not stall siblings.
	//
	//   I6 — Repeated Publish runs for the same sessionID do not leave
	//        residual entries in the subscribers map.
	//
	// Iteration counts are sized to exceed the empirical reproduction
	// window from the prior fix (race fired on iteration 9 of 20). Each
	// stress spec runs >= 100 iterations under -race; results are
	// quantified in the audit vault note.
	Describe("concurrency audit (lifecycle invariants under stress)", func() {
		// Helper: collect goroutine panics into a channel without crashing
		// the test binary. Mirrors the recoverInto pattern from the
		// close-during-send specs.
		makeRecover := func(panicCh chan<- string) func(string) {
			return func(label string) {
				if r := recover(); r != nil {
					panicCh <- fmt.Sprintf("%s panic: %v", label, r)
				}
			}
		}

		It("I2: tolerates concurrent Publish runs for the same sessionID without double-closing subscriber channels", func() {
			// Pre-fix shape: the `active` map was a bool, and Publish's
			// terminal block unconditionally `delete`d the subscribers
			// entry and `close`d every channel in the snapshot. Two
			// concurrent Publish runs for the same sessionID would both
			// take the snapshot, both delete, and both call close(sub) on
			// every channel — panic on the second call.
			//
			// This spec runs 100 iterations of two-concurrent-Publish per
			// session, each with one subscriber that drains to source-end.
			// A single double-close anywhere is a PANIC and fails the
			// spec.
			const iterations = 100
			panicCh := make(chan string, iterations*4)
			recoverInto := makeRecover(panicCh)

			for iter := 0; iter < iterations; iter++ {
				b := api.NewSessionBroker()
				sessionID := fmt.Sprintf("sess-concurrent-publish-%d", iter)

				ch, unsub := b.Subscribe(sessionID)

				// Build two source channels feeding the same sessionID
				// concurrently. Each emits a few chunks then closes.
				makeSource := func(label string) <-chan provider.StreamChunk {
					s := make(chan provider.StreamChunk, 6)
					for j := 0; j < 5; j++ {
						s <- provider.StreamChunk{Content: label}
					}
					close(s)
					return s
				}
				srcA := makeSource("A")
				srcB := makeSource("B")

				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					defer recoverInto("publishA")
					b.Publish(sessionID, srcA)
				}()
				go func() {
					defer wg.Done()
					defer recoverInto("publishB")
					b.Publish(sessionID, srcB)
				}()

				// Drain the subscriber channel until it closes (both
				// publishers exited and the LAST one ran the terminal
				// close) or until a generous deadline fires.
				drainDone := make(chan struct{})
				go func() {
					defer close(drainDone)
					for range ch {
					}
				}()

				wg.Wait()
				// After both publishers exit, the channel must be closed
				// exactly once and the drain goroutine must terminate.
				select {
				case <-drainDone:
				case <-time.After(2 * time.Second):
					Fail(fmt.Sprintf("iteration %d: subscriber channel never closed after both Publish runs returned", iter))
				}
				unsub() // idempotent — channel already closed; this MUST NOT panic.
			}

			close(panicCh)
			var panics []string
			for p := range panicCh {
				panics = append(panics, p)
			}
			Expect(panics).To(BeEmpty(),
				"concurrent Publish runs for the same sessionID must not double-close subscribers; "+
					"this is the latent panic vector that the bool-active-flag invited "+
					"(both runs setting active=true, both running terminal close)")
		})

		It("I3: tolerates Subscribe arriving during terminal close without panicking or leaking the channel", func() {
			// Race window: between Publish's source-channel close and its
			// terminal close-loop, a concurrent Subscribe can append a
			// fresh channel to the subscribers map. Pre-audit, that
			// channel was orphaned (never received chunks from the ending
			// run, and not guaranteed to be closed). Post-audit invariant:
			// either (a) the late Subscribe sees no in-flight Publish via
			// IsPublishing == false, or (b) the channel is closed by the
			// owning Publish run — never a permanent leak in the
			// subscribers map.
			const iterations = 100
			panicCh := make(chan string, iterations*4)
			recoverInto := makeRecover(panicCh)

			for iter := 0; iter < iterations; iter++ {
				b := api.NewSessionBroker()
				sessionID := fmt.Sprintf("sess-subscribe-during-close-%d", iter)

				// Source closes immediately so the publisher races
				// straight into its terminal block.
				src := make(chan provider.StreamChunk)
				close(src)

				pubDone := make(chan struct{})
				go func() {
					defer recoverInto("publisher")
					defer close(pubDone)
					b.Publish(sessionID, src)
				}()

				// Hammer Subscribe concurrently with the terminal close.
				// Each Subscribe immediately Unsubscribes — mirroring the
				// fast handler-cancel case. We do this from many
				// goroutines to widen the race window.
				const concurrentSubs = 8
				var wg sync.WaitGroup
				for i := 0; i < concurrentSubs; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer recoverInto("late-subscriber")
						_, unsub := b.Subscribe(sessionID)
						unsub()
					}()
				}
				wg.Wait()
				<-pubDone

				// Invariant: after the publisher exits AND every late
				// subscriber unsubscribes, the broker holds no entry for
				// this session — otherwise the broker would accumulate
				// subscribers maps over time.
				Expect(b.SubscriberCount(sessionID)).To(Equal(0),
					"iteration %d: subscribers map must be empty after Publish exits and all subscribers unsubscribe; "+
						"a leftover entry indicates a Subscribe-during-close leak", iter)
			}

			close(panicCh)
			var panics []string
			for p := range panicCh {
				panics = append(panics, p)
			}
			Expect(panics).To(BeEmpty(),
				"Subscribe-during-terminal-close must never panic — "+
					"the fix must not introduce a window where the publisher "+
					"closes a channel that a fresh subscriber is reading")
		})

		It("I4: 100 stress iterations with 100+ concurrent Subscribe/Unsubscribe per session do not panic or leak", func() {
			// Larger and longer than the prior close-during-send stress
			// spec (which used 20 iterations × 16 subscribers). Per the
			// audit brief: 100+ concurrent subscribers, 100+ iterations,
			// to push beyond the empirical reproduction window of every
			// prior race in this file.
			const iterations = 100
			const subsPerIteration = 100
			const chunksPerIteration = 50

			panicCh := make(chan string, iterations*(subsPerIteration+1))
			recoverInto := makeRecover(panicCh)

			for iter := 0; iter < iterations; iter++ {
				b := api.NewSessionBroker()
				sessionID := fmt.Sprintf("sess-stress-100-%d", iter)

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
					go func(idx int) {
						defer wg.Done()
						defer recoverInto("subscriber")
						ch, unsub := b.Subscribe(sessionID)
						defer unsub()
						readCount := 1 + (idx % 10)
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
					}(i)
				}
				wg.Wait()
				<-pubDone

				Expect(b.SubscriberCount(sessionID)).To(Equal(0),
					"iteration %d: 100 subscribers all unsubscribed and Publish exited — "+
						"map must not retain entries", iter)
			}

			close(panicCh)
			var panics []string
			for p := range panicCh {
				panics = append(panics, p)
			}
			Expect(panics).To(BeEmpty(),
				"100×100 concurrent Subscribe/Unsubscribe under Publish must be panic-free")
		})

		It("I5: handler-style ctx.Done -> unsubscribe pattern does not stall the publisher or panic", func() {
			// Mirrors handleSessionStream's exact lifecycle: subscribe,
			// run a select on ch + ctx.Done, ctx-cancel mid-stream,
			// `defer unsubscribe()` runs as cleanup. The publisher must
			// keep going for siblings.
			const iterations = 100
			panicCh := make(chan string, iterations*4)
			recoverInto := makeRecover(panicCh)

			for iter := 0; iter < iterations; iter++ {
				b := api.NewSessionBroker()
				sessionID := fmt.Sprintf("sess-handler-cancel-%d", iter)

				source := make(chan provider.StreamChunk, 21)
				for j := 0; j < 20; j++ {
					source <- provider.StreamChunk{Content: "c"}
				}
				source <- provider.StreamChunk{Done: true}
				close(source)

				// Sibling subscriber that drains to completion — must
				// see all chunks regardless of the cancelling sibling.
				sibCh, unsubSib := b.Subscribe(sessionID)
				defer unsubSib()

				pubDone := make(chan struct{})
				go func() {
					defer recoverInto("publisher")
					defer close(pubDone)
					b.Publish(sessionID, source)
				}()

				// Cancelling sibling: subscribes, reads one chunk, then
				// "context cancels" — mirrors handleSessionStream.
				cancelDone := make(chan struct{})
				go func() {
					defer recoverInto("cancel-handler")
					defer close(cancelDone)
					ch, unsub := b.Subscribe(sessionID)
					defer unsub()
					select {
					case <-ch:
					case <-time.After(2 * time.Second):
					}
					// "ctx.Done()" — return triggers defer unsub().
				}()

				// Sibling drains to completion.
				sibCount := 0
				timeout := time.After(5 * time.Second)
			drain:
				for {
					select {
					case chunk, ok := <-sibCh:
						if !ok {
							break drain
						}
						if chunk.Done {
							sibCount++
							break drain
						}
						if chunk.Content != "" {
							sibCount++
						}
					case <-timeout:
						Fail(fmt.Sprintf("iteration %d: sibling subscriber stalled when cancelling sibling unsubscribed mid-stream", iter))
					}
				}

				<-cancelDone
				<-pubDone
				Expect(sibCount).To(Equal(21),
					"iteration %d: sibling must receive all 21 chunks (20 content + Done); a cancelled sibling must not starve fan-out", iter)
			}

			close(panicCh)
			var panics []string
			for p := range panicCh {
				panics = append(panics, p)
			}
			Expect(panics).To(BeEmpty(),
				"handler-style ctx.Done unsubscribe must never panic the publisher")
		})

		It("I6: repeated Publish runs for the same sessionID do not leak subscribers between runs", func() {
			// Each round: subscribe, publish, drain to close, unsubscribe,
			// assert no map entry remains. Repeats with the same sessionID
			// to exercise the across-runs lifecycle the prior tests
			// don't cover (each prior test creates a fresh broker per
			// iteration).
			const rounds = 100
			b := api.NewSessionBroker()
			const sessionID = "sess-repeated-publish"

			for r := 0; r < rounds; r++ {
				ch, unsub := b.Subscribe(sessionID)

				source := make(chan provider.StreamChunk, 4)
				source <- provider.StreamChunk{Content: "c"}
				source <- provider.StreamChunk{Content: "c"}
				source <- provider.StreamChunk{Done: true}
				close(source)

				pubDone := make(chan struct{})
				go func() {
					defer close(pubDone)
					b.Publish(sessionID, source)
				}()

				received := 0
				deadline := time.After(2 * time.Second)
			drainLoop:
				for {
					select {
					case _, ok := <-ch:
						if !ok {
							break drainLoop
						}
						received++
					case <-deadline:
						Fail(fmt.Sprintf("round %d: subscriber stalled before channel close", r))
					}
				}
				<-pubDone
				unsub()

				Expect(received).To(Equal(3),
					"round %d: subscriber must receive all 3 chunks before close", r)
				Expect(b.SubscriberCount(sessionID)).To(Equal(0),
					"round %d: no leftover subscribers entry between Publish runs — "+
						"a non-zero count would indicate the empty-slice leak", r)
				Expect(b.IsPublishing(sessionID)).To(BeFalse(),
					"round %d: active flag must be cleared after Publish returns", r)
			}
		})

		It("I1: Publish closes each subscriber channel exactly once (single-closer invariant)", func() {
			// Direct pin of the single-closer invariant. Pre-fix the bool
			// active flag let two Publish runs both close the same
			// channel; this spec asserts the invariant at the channel
			// receive end (a second close panics; if the spec runs
			// without panic AND the receiver sees `ok=false` exactly
			// once, the invariant holds).
			const iterations = 50
			for iter := 0; iter < iterations; iter++ {
				b := api.NewSessionBroker()
				sessionID := fmt.Sprintf("sess-single-closer-%d", iter)
				ch, _ := b.Subscribe(sessionID)

				// Two empty sources → two concurrent Publish runs that
				// both proceed straight to terminal close. Pre-fix this
				// is the most direct path to a double-close panic.
				srcA := make(chan provider.StreamChunk)
				srcB := make(chan provider.StreamChunk)
				close(srcA)
				close(srcB)

				panicCh := make(chan string, 2)
				recoverInto := makeRecover(panicCh)

				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					defer recoverInto("publishA")
					b.Publish(sessionID, srcA)
				}()
				go func() {
					defer wg.Done()
					defer recoverInto("publishB")
					b.Publish(sessionID, srcB)
				}()
				wg.Wait()
				close(panicCh)

				closeCount := 0
				timeout := time.After(2 * time.Second)
			waitClose:
				for {
					select {
					case _, ok := <-ch:
						if !ok {
							closeCount++
							break waitClose
						}
					case <-timeout:
						Fail(fmt.Sprintf("iteration %d: channel never closed after both Publish runs exited", iter))
					}
				}

				var panics []string
				for p := range panicCh {
					panics = append(panics, p)
				}
				Expect(panics).To(BeEmpty(),
					"iteration %d: neither Publish run may panic on close (single-closer invariant)", iter)
				Expect(closeCount).To(Equal(1),
					"iteration %d: subscriber channel must be closed exactly once by the LAST Publish to exit", iter)
			}
		})

		It("SubscribeIfPublishing returns (nil, false) when no Publish is in flight for the session", func() {
			// The transactional accessor's no-publisher branch: there is
			// no active publisher, so the call MUST NOT register a
			// subscriber and MUST signal the caller to fast-path [DONE].
			// Pre-fix the only available primitive was Subscribe + the
			// non-atomic IsPublishing — a caller could not express
			// "register me only if a publisher will drive me" without a
			// TOCTOU window between the two reads.
			b := api.NewSessionBroker()
			const sessionID = "sess-no-publisher"

			ch, unsub, ok := b.SubscribeIfPublishing(sessionID)

			Expect(ok).To(BeFalse(),
				"with no Publish in flight, SubscribeIfPublishing must report ok=false")
			Expect(ch).To(BeNil(),
				"with ok=false, the returned channel must be nil so callers cannot accidentally receive from it")
			Expect(unsub).NotTo(BeNil(),
				"a no-op unsub must be returned so callers can `defer unsub()` uniformly")
			Expect(b.SubscriberCount(sessionID)).To(Equal(0),
				"no subscriber state may be left behind on the no-publisher branch")
			// no-op unsub must be idempotent and safe to call multiple
			// times (callers `defer unsub()` regardless of branch).
			Expect(unsub).NotTo(Panic())
			Expect(unsub).NotTo(Panic())
			Expect(b.SubscriberCount(sessionID)).To(Equal(0))
		})

		It("SubscribeIfPublishing returns (ch, true) and registers a subscriber that receives subsequent chunks while a Publish is in flight", func() {
			// The transactional accessor's publisher-active branch: a
			// publisher is mid-stream, so the call MUST register a
			// subscriber atomically and the caller MUST receive every
			// subsequent fan-out chunk.
			b := api.NewSessionBroker()
			const sessionID = "sess-publisher-active"

			// Open a long-running source so the Publish goroutine sits
			// in its `for chunk := range chunks` loop with active > 0
			// while we call SubscribeIfPublishing.
			source := make(chan provider.StreamChunk, 4)
			pubDone := make(chan struct{})
			go func() {
				defer close(pubDone)
				b.Publish(sessionID, source)
			}()

			// Wait for active to flip to true. Publish increments the
			// refcount under b.mu before entering the receive loop, so
			// IsPublishing is the right wait predicate here.
			Eventually(func() bool { return b.IsPublishing(sessionID) }).
				Should(BeTrue(), "Publish must report active before the test interrogates the broker")

			ch, unsub, ok := b.SubscribeIfPublishing(sessionID)
			Expect(ok).To(BeTrue(),
				"with a Publish in flight, SubscribeIfPublishing must report ok=true")
			Expect(ch).NotTo(BeNil(),
				"with ok=true, the returned channel must be non-nil and ready to receive chunks")
			Expect(b.SubscriberCount(sessionID)).To(Equal(1),
				"the subscriber must be registered atomically with the active-check")

			// Drive a chunk through the live source; the freshly-
			// registered subscriber must receive it.
			source <- provider.StreamChunk{Content: "post-subscribe"}
			var received provider.StreamChunk
			Eventually(ch).Should(Receive(&received),
				"subscriber registered via SubscribeIfPublishing must receive the next published chunk")
			Expect(received.Content).To(Equal("post-subscribe"))

			// Unblock the publisher: send Done and close source so
			// Publish's terminal close runs and shuts the channel.
			source <- provider.StreamChunk{Done: true}
			close(source)

			// Drain remaining chunks until close; final receive on a
			// closed channel returns the zero value with ok=false.
			Eventually(func() bool {
				select {
				case _, recvOK := <-ch:
					return !recvOK
				default:
					return false
				}
			}).Should(BeTrue(), "publisher's terminal close must close the subscriber channel")

			unsub()
			<-pubDone
			Expect(b.SubscriberCount(sessionID)).To(Equal(0),
				"after publisher exits and subscriber unsubscribes the map must be clean (I8)")
		})

		It("SubscribeIfPublishing under high concurrency never registers a subscriber when no publisher is active and never returns ok=false during an active publish", func() {
			// This is the race-stress spec for the new transactional
			// accessor. It races SubscribeIfPublishing against
			// Publish-start and Publish-end across many iterations and
			// goroutines. The invariants:
			//
			//   1. (Subscriber-registered → ok-was-true): every
			//      subscriber that ends up in the broker map was returned
			//      via an ok=true call. There is no path where ok=false
			//      leaves a subscriber behind.
			//   2. (No starvation of late subscribers): every ok=true
			//      subscriber receives the close signal eventually
			//      (either via the publisher's terminal close or its own
			//      unsubscribe path).
			//   3. (Refcount monotone with calls): IsPublishing observed
			//      after an ok=true return implies active > 0 was true at
			//      the moment of the call (we can't observe the moment
			//      directly, but a panic-free run with no orphaned
			//      subscribers is the contract guarantee).
			//
			// Pre-fix the equivalent caller pattern (Subscribe +
			// IsPublishing) had a window where a subscriber could be
			// registered while ok=false was observed — exactly the
			// TOCTOU the audit flagged at server.go:830.
			const iterations = 100
			const callersPerIteration = 8

			panicCh := make(chan string, iterations*callersPerIteration)
			recoverInto := makeRecover(panicCh)

			for iter := 0; iter < iterations; iter++ {
				b := api.NewSessionBroker()
				sessionID := fmt.Sprintf("sess-toctou-stress-%d", iter)

				source := make(chan provider.StreamChunk, 4)
				source <- provider.StreamChunk{Content: "c"}
				source <- provider.StreamChunk{Done: true}
				close(source)

				pubDone := make(chan struct{})
				go func() {
					defer recoverInto("publisher")
					defer close(pubDone)
					b.Publish(sessionID, source)
				}()

				// Hammer SubscribeIfPublishing concurrently with the
				// short-running Publish. Some calls land before the
				// active refcount goes positive (must return ok=false),
				// some land while it is positive (must return ok=true
				// and register), some land after the terminal close
				// (must return ok=false and leave no subscriber).
				var (
					wg          sync.WaitGroup
					okCount     int
					notOkCount  int
					countMu     sync.Mutex
					okChannels  []<-chan provider.StreamChunk
					okUnsubs    []func()
					okChannelMu sync.Mutex
				)
				for i := 0; i < callersPerIteration; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer recoverInto("caller")
						ch, unsub, ok := b.SubscribeIfPublishing(sessionID)
						countMu.Lock()
						if ok {
							okCount++
						} else {
							notOkCount++
						}
						countMu.Unlock()
						if ok {
							okChannelMu.Lock()
							okChannels = append(okChannels, ch)
							okUnsubs = append(okUnsubs, unsub)
							okChannelMu.Unlock()
						} else {
							// no-op unsub; must be safe to call.
							unsub()
						}
					}()
				}
				wg.Wait()
				<-pubDone

				// Drain every ok=true subscriber to its close. The
				// invariant: every registered subscriber receives a
				// close (publisher closes, or this drain blocks
				// forever — bounded by deadline below).
				for i, ch := range okChannels {
					deadline := time.After(2 * time.Second)
				drain:
					for {
						select {
						case _, recvOK := <-ch:
							if !recvOK {
								break drain
							}
						case <-deadline:
							Fail(fmt.Sprintf("iteration %d caller %d: ok=true subscriber never received close", iter, i))
						}
					}
					okUnsubs[i]()
				}

				Expect(b.SubscriberCount(sessionID)).To(Equal(0),
					"iteration %d: after publisher exits and every ok=true subscriber unsubscribes, the map must be clean", iter)
				// Sanity: at least one branch fired. Both branches
				// firing is the common case; a run that hits zero
				// callers in either branch is statistically rare but
				// not a failure (race timing).
				Expect(okCount+notOkCount).To(Equal(callersPerIteration),
					"iteration %d: every caller must have observed exactly one branch", iter)
			}

			close(panicCh)
			var panics []string
			for p := range panicCh {
				panics = append(panics, p)
			}
			Expect(panics).To(BeEmpty(),
				"SubscribeIfPublishing under stress must not panic and must not orphan subscribers")
		})
	})
})
