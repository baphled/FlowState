package delegation

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/coordination"
)

var _ = Describe("RejectionTracker", func() {
	var (
		store   coordination.Store
		tracker *RejectionTracker
		ctx     context.Context
	)

	BeforeEach(func() {
		store = coordination.NewMemoryStore()
		tracker = NewRejectionTracker(store, 3)
		ctx = context.Background()
	})

	Describe("Record", func() {
		It("increments the count on first call", func() {
			count, err := tracker.Record(ctx, "chain-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(1))
		})

		It("returns N after N calls", func() {
			for i := 1; i <= 3; i++ {
				count, err := tracker.Record(ctx, "chain-1")
				Expect(err).NotTo(HaveOccurred())
				Expect(count).To(Equal(i))
			}
		})

		It("tracks different chains independently", func() {
			_, err := tracker.Record(ctx, "chain-a")
			Expect(err).NotTo(HaveOccurred())
			_, err = tracker.Record(ctx, "chain-a")
			Expect(err).NotTo(HaveOccurred())

			count, err := tracker.Record(ctx, "chain-b")
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(1))
		})
	})

	Describe("Count", func() {
		It("returns 0 for an unknown chain", func() {
			count, err := tracker.Count(ctx, "unknown")
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(BeZero())
		})

		It("returns the correct value after Record calls", func() {
			_, _ = tracker.Record(ctx, "chain-1")
			_, _ = tracker.Record(ctx, "chain-1")

			count, err := tracker.Count(ctx, "chain-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(2))
		})
	})

	Describe("ExhaustedFor", func() {
		It("returns false when count is below max", func() {
			_, _ = tracker.Record(ctx, "chain-1")
			_, _ = tracker.Record(ctx, "chain-1")

			exhausted, err := tracker.ExhaustedFor(ctx, "chain-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(exhausted).To(BeFalse())
		})

		It("returns true when count reaches max", func() {
			_, _ = tracker.Record(ctx, "chain-1")
			_, _ = tracker.Record(ctx, "chain-1")
			_, _ = tracker.Record(ctx, "chain-1")

			exhausted, err := tracker.ExhaustedFor(ctx, "chain-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(exhausted).To(BeTrue())
		})

		It("returns true when count exceeds max", func() {
			for range 5 {
				_, _ = tracker.Record(ctx, "chain-1")
			}

			exhausted, err := tracker.ExhaustedFor(ctx, "chain-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(exhausted).To(BeTrue())
		})
	})

	Describe("Reset", func() {
		It("clears the count back to 0", func() {
			_, _ = tracker.Record(ctx, "chain-1")
			_, _ = tracker.Record(ctx, "chain-1")

			err := tracker.Reset(ctx, "chain-1")
			Expect(err).NotTo(HaveOccurred())

			count, err := tracker.Count(ctx, "chain-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(BeZero())
		})
	})

	Describe("NewRejectionTracker", func() {
		It("uses defaultMaxRejections when max is zero", func() {
			t := NewRejectionTracker(store, 0)
			for range defaultMaxRejections {
				_, _ = t.Record(ctx, "chain-x")
			}

			exhausted, err := t.ExhaustedFor(ctx, "chain-x")
			Expect(err).NotTo(HaveOccurred())
			Expect(exhausted).To(BeTrue())
		})
	})

	Describe("thread safety", func() {
		It("handles concurrent Record calls without race", func() {
			var wg sync.WaitGroup
			numGoroutines := 50

			for range numGoroutines {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, _ = tracker.Record(ctx, "chain-concurrent")
				}()
			}

			wg.Wait()

			count, err := tracker.Count(ctx, "chain-concurrent")
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(numGoroutines))
		})
	})
})
