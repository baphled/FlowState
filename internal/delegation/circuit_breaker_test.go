package delegation

import (
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CircuitBreaker", func() {
	var cb *CircuitBreaker

	BeforeEach(func() {
		cb = NewCircuitBreaker(3)
	})

	Describe("initial state", func() {
		It("starts in Closed state", func() {
			Expect(cb.State()).To(Equal(CircuitClosed))
		})

		It("starts with zero failures", func() {
			Expect(cb.Failures()).To(BeZero())
		})
	})

	Describe("Allow", func() {
		Context("in Closed state", func() {
			It("returns true", func() {
				Expect(cb.Allow()).To(BeTrue())
			})

			It("returns true for multiple attempts", func() {
				Expect(cb.Allow()).To(BeTrue())
				Expect(cb.Allow()).To(BeTrue())
				Expect(cb.Allow()).To(BeTrue())
			})
		})

		Context("when at max failures", func() {
			BeforeEach(func() {
				cb.RecordFailure()
				cb.RecordFailure()
				cb.RecordFailure()
			})

			It("transitions to Open state", func() {
				Expect(cb.State()).To(Equal(CircuitOpen))
			})

			It("returns false in Open state", func() {
				Expect(cb.Allow()).To(BeFalse())
			})
		})

		Context("after Reset", func() {
			BeforeEach(func() {
				cb.RecordFailure()
				cb.RecordFailure()
				cb.RecordFailure()
				cb.Reset()
			})

			It("transitions to HalfOpen state", func() {
				Expect(cb.State()).To(Equal(CircuitHalfOpen))
			})

			It("allows exactly one attempt", func() {
				Expect(cb.Allow()).To(BeTrue())
				Expect(cb.Allow()).To(BeFalse())
			})
		})

		Context("in HalfOpen after success", func() {
			BeforeEach(func() {
				cb.RecordFailure()
				cb.RecordFailure()
				cb.RecordFailure()
				cb.Reset()
				cb.Allow()
				cb.RecordSuccess()
			})

			It("transitions to Closed state", func() {
				Expect(cb.State()).To(Equal(CircuitClosed))
			})

			It("resets failure count", func() {
				Expect(cb.Failures()).To(BeZero())
			})
		})

		Context("in HalfOpen after failure", func() {
			BeforeEach(func() {
				cb.RecordFailure()
				cb.RecordFailure()
				cb.RecordFailure()
				cb.Reset()
				cb.Allow()
				cb.RecordFailure()
			})

			It("transitions back to Open state", func() {
				Expect(cb.State()).To(Equal(CircuitOpen))
			})
		})
	})

	Describe("RecordFailure", func() {
		It("increments failure count", func() {
			Expect(cb.Failures()).To(BeZero())
			cb.RecordFailure()
			Expect(cb.Failures()).To(Equal(1))
			cb.RecordFailure()
			Expect(cb.Failures()).To(Equal(2))
		})

		It("transitions to Open after max failures", func() {
			cb.RecordFailure()
			Expect(cb.State()).To(Equal(CircuitClosed))
			cb.RecordFailure()
			Expect(cb.State()).To(Equal(CircuitClosed))
			cb.RecordFailure()
			Expect(cb.State()).To(Equal(CircuitOpen))
		})
	})

	Describe("RecordSuccess", func() {
		It("resets failure count to zero", func() {
			cb.RecordFailure()
			cb.RecordFailure()
			Expect(cb.Failures()).To(Equal(2))
			cb.RecordSuccess()
			Expect(cb.Failures()).To(BeZero())
		})

		It("keeps state as Closed when already closed", func() {
			cb.RecordSuccess()
			Expect(cb.State()).To(Equal(CircuitClosed))
		})

		It("transitions from HalfOpen to Closed", func() {
			cb.RecordFailure()
			cb.RecordFailure()
			cb.RecordFailure()
			cb.Reset()
			cb.Allow()
			cb.RecordSuccess()
			Expect(cb.State()).To(Equal(CircuitClosed))
		})
	})

	Describe("Reset", func() {
		It("transitions to HalfOpen", func() {
			cb.Reset()
			Expect(cb.State()).To(Equal(CircuitHalfOpen))
		})

		It("does not reset failure count", func() {
			cb.RecordFailure()
			cb.RecordFailure()
			cb.Reset()
			Expect(cb.Failures()).To(Equal(2))
		})
	})

	Describe("thread safety", func() {
		It("handles concurrent RecordFailure without race", func() {
			var wg sync.WaitGroup
			numGoroutines := 100

			for range numGoroutines {
				wg.Add(1)
				go func() {
					defer wg.Done()
					cb.RecordFailure()
				}()
			}

			wg.Wait()
		})

		It("handles concurrent Allow without race", func() {
			var wg sync.WaitGroup
			numGoroutines := 100

			for range numGoroutines {
				wg.Add(1)
				go func() {
					defer wg.Done()
					cb.Allow()
				}()
			}

			wg.Wait()
		})

		It("handles concurrent RecordSuccess without race", func() {
			var wg sync.WaitGroup
			numGoroutines := 100

			for range numGoroutines {
				wg.Add(1)
				go func() {
					defer wg.Done()
					cb.RecordSuccess()
				}()
			}

			wg.Wait()
		})

		It("handles concurrent State calls without race", func() {
			var wg sync.WaitGroup
			numGoroutines := 100

			for range numGoroutines {
				wg.Add(1)
				go func() {
					defer wg.Done()
					cb.State()
				}()
			}

			wg.Wait()
		})
	})
})
