package api_test

import (
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
		It("closes the subscriber channel on unsubscribe", func() {
			ch, unsub := broker.Subscribe("sess-4")
			unsub()
			Eventually(ch, time.Second).Should(BeClosed())
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
