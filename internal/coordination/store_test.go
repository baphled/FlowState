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

		It("handles concurrent Set and Exists from multiple goroutines without races", func() {
			// Race-detector probe: producers Set known keys while checkers
			// repeatedly call Exists. Exists must never panic, return an
			// error, or deadlock under contention. The final assertion
			// confirms every produced key is reported present.
			var wg sync.WaitGroup
			const producers = 25
			const checkers = 25
			const opsPerChecker = 20

			wg.Add(producers)
			for i := range producers {
				go func(n int) {
					defer GinkgoRecover()
					defer wg.Done()

					key := fmt.Sprintf("contention/%d", n)
					Expect(store.Set(key, []byte("v"))).To(Succeed())
				}(i)
			}

			wg.Add(checkers)
			for i := range checkers {
				go func(n int) {
					defer GinkgoRecover()
					defer wg.Done()

					for j := range opsPerChecker {
						key := fmt.Sprintf("contention/%d", (n+j)%producers)
						_, err := store.Exists(key)
						Expect(err).NotTo(HaveOccurred(),
							"Exists must never error under read/write contention with Set")
					}
				}(i)
			}
			wg.Wait()

			// All producers eventually committed — every key must now be present.
			for i := range producers {
				ok, err := store.Exists(fmt.Sprintf("contention/%d", i))
				Expect(err).NotTo(HaveOccurred())
				Expect(ok).To(BeTrue())
			}
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

	Describe("Exists", func() {
		Context("when the key is absent", func() {
			It("returns (false, nil) — soft-miss, no ErrKeyNotFound wrapping", func() {
				ok, err := store.Exists("nonexistent")
				Expect(err).NotTo(HaveOccurred(),
					"soft-miss API must not wrap ErrKeyNotFound — callers asked 'does it exist', not 'give me the value'")
				Expect(ok).To(BeFalse())
			})
		})

		Context("when the key is present", func() {
			It("returns (true, nil) without touching the value", func() {
				Expect(store.Set("present", []byte("v"))).To(Succeed())

				ok, err := store.Exists("present")
				Expect(err).NotTo(HaveOccurred())
				Expect(ok).To(BeTrue())
			})

			It("reports an empty []byte value as present", func() {
				Expect(store.Set("empty", []byte{})).To(Succeed())

				ok, err := store.Exists("empty")
				Expect(err).NotTo(HaveOccurred())
				Expect(ok).To(BeTrue(),
					"zero-length value is still a present key; Exists answers presence, not non-emptiness")
			})
		})

		Context("after Delete", func() {
			It("returns (false, nil) for the deleted key", func() {
				Expect(store.Set("delme", []byte("v"))).To(Succeed())
				Expect(store.Delete("delme")).To(Succeed())

				ok, err := store.Exists("delme")
				Expect(err).NotTo(HaveOccurred())
				Expect(ok).To(BeFalse())
			})
		})
	})
})
