package streaming_test

import (
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
)

var _ = Describe("MemorySwarmStore", func() {
	Describe("NewMemorySwarmStore", func() {
		It("returns an empty store", func() {
			store := streaming.NewMemorySwarmStore(15)
			Expect(store).NotTo(BeNil())
			Expect(store.All()).To(BeEmpty())
		})

		It("applies the default capacity when capacity <= 0", func() {
			store := streaming.NewMemorySwarmStore(0)
			Expect(store.Capacity()).To(Equal(15))
			negative := streaming.NewMemorySwarmStore(-5)
			Expect(negative.Capacity()).To(Equal(15))
		})

		It("honours a positive capacity argument", func() {
			store := streaming.NewMemorySwarmStore(7)
			Expect(store.Capacity()).To(Equal(7))
		})
	})

	Describe("Append", func() {
		It("records a single event", func() {
			store := streaming.NewMemorySwarmStore(15)
			ev := streaming.SwarmEvent{
				ID:        "evt-1",
				Type:      streaming.EventDelegation,
				Status:    "started",
				Timestamp: time.Unix(1_700_000_000, 0),
				AgentID:   "qa-agent",
			}

			store.Append(ev)

			all := store.All()
			Expect(all).To(HaveLen(1))
			Expect(all[0]).To(Equal(ev))
		})

		It("evicts the oldest events when capacity is exceeded", func() {
			store := streaming.NewMemorySwarmStore(15)
			for idx := range 17 {
				store.Append(streaming.SwarmEvent{
					ID:   fmt.Sprintf("evt-%d", idx),
					Type: streaming.EventToolCall,
				})
			}

			all := store.All()
			Expect(all).To(HaveLen(15))
			// Oldest two (evt-0, evt-1) were evicted; evt-2 is now the head.
			Expect(all[0].ID).To(Equal("evt-2"))
			Expect(all[len(all)-1].ID).To(Equal("evt-16"))
		})
	})

	Describe("All", func() {
		It("returns a defensive copy callers can mutate safely", func() {
			store := streaming.NewMemorySwarmStore(15)
			store.Append(streaming.SwarmEvent{ID: "evt-1"})

			out := store.All()
			out[0].ID = "mutated"

			// The store's internal slice is untouched.
			Expect(store.All()[0].ID).To(Equal("evt-1"))
		})
	})

	Describe("Clear", func() {
		It("removes all events from the store", func() {
			store := streaming.NewMemorySwarmStore(15)
			store.Append(streaming.SwarmEvent{ID: "evt-1"})
			store.Append(streaming.SwarmEvent{ID: "evt-2"})

			store.Clear()

			Expect(store.All()).To(BeEmpty())
		})
	})

	Describe("concurrent Append", func() {
		It("does not race under heavy concurrent writes", func() {
			store := streaming.NewMemorySwarmStore(15)
			const goroutines = 10
			const perGoroutine = 100

			var wg sync.WaitGroup
			wg.Add(goroutines)
			for g := range goroutines {
				go func(gid int) {
					defer wg.Done()
					for i := range perGoroutine {
						store.Append(streaming.SwarmEvent{
							ID:   fmt.Sprintf("g%d-i%d", gid, i),
							Type: streaming.EventToolCall,
						})
					}
				}(g)
			}
			wg.Wait()

			// With 1000 appends and capacity 15, the store must hold exactly
			// 15 events and All() must not panic.
			Expect(store.All()).To(HaveLen(15))
		})
	})
})
