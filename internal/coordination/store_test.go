package coordination_test

import (
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/coordination"
)

var _ = Describe("MemoryStore", func() {
	var store coordination.Store

	BeforeEach(func() {
		store = coordination.NewMemoryStore()
	})

	Describe("Get", func() {
		Context("when the key does not exist", func() {
			It("returns an error", func() {
				_, err := store.Get("nonexistent")
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("Set and Get", func() {
		It("round-trips data correctly", func() {
			err := store.Set("mykey", []byte("myvalue"))
			Expect(err).NotTo(HaveOccurred())

			val, err := store.Get("mykey")
			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal([]byte("myvalue")))
		})
	})

	Describe("List", func() {
		BeforeEach(func() {
			Expect(store.Set("chainA/plan", []byte("plan-a"))).To(Succeed())
			Expect(store.Set("chainA/review", []byte("review-a"))).To(Succeed())
			Expect(store.Set("chainB/plan", []byte("plan-b"))).To(Succeed())
		})

		Context("when keys match the prefix", func() {
			It("returns matching keys", func() {
				keys, err := store.List("chainA/")
				Expect(err).NotTo(HaveOccurred())
				Expect(keys).To(ConsistOf("chainA/plan", "chainA/review"))
			})
		})

		Context("when no keys match the prefix", func() {
			It("returns an empty slice", func() {
				keys, err := store.List("chainC/")
				Expect(err).NotTo(HaveOccurred())
				Expect(keys).To(BeEmpty())
			})
		})
	})

	Describe("Delete", func() {
		Context("when the key exists", func() {
			It("removes the key", func() {
				Expect(store.Set("todelete", []byte("val"))).To(Succeed())

				err := store.Delete("todelete")
				Expect(err).NotTo(HaveOccurred())

				_, err = store.Get("todelete")
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when the key does not exist", func() {
			It("returns an error", func() {
				err := store.Delete("nonexistent")
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("Concurrent access", func() {
		It("handles concurrent Set from multiple goroutines without races", func() {
			var wg sync.WaitGroup
			const goroutines = 50

			wg.Add(goroutines)
			for i := range goroutines {
				go func(n int) {
					defer GinkgoRecover()
					defer wg.Done()

					key := fmt.Sprintf("concurrent/%d", n)
					Expect(store.Set(key, []byte(fmt.Sprintf("value-%d", n)))).To(Succeed())
				}(i)
			}
			wg.Wait()

			keys, err := store.List("concurrent/")
			Expect(err).NotTo(HaveOccurred())
			Expect(keys).To(HaveLen(goroutines))
		})
	})

	Describe("Chain ID namespace isolation", func() {
		It("keeps chainA/key and chainB/key independent", func() {
			Expect(store.Set("chainA/requirements", []byte("req-a"))).To(Succeed())
			Expect(store.Set("chainB/requirements", []byte("req-b"))).To(Succeed())

			valA, err := store.Get("chainA/requirements")
			Expect(err).NotTo(HaveOccurred())
			Expect(valA).To(Equal([]byte("req-a")))

			valB, err := store.Get("chainB/requirements")
			Expect(err).NotTo(HaveOccurred())
			Expect(valB).To(Equal([]byte("req-b")))

			keysA, err := store.List("chainA/")
			Expect(err).NotTo(HaveOccurred())
			Expect(keysA).To(ConsistOf("chainA/requirements"))

			keysB, err := store.List("chainB/")
			Expect(err).NotTo(HaveOccurred())
			Expect(keysB).To(ConsistOf("chainB/requirements"))
		})
	})
})
