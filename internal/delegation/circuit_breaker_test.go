package delegation

import (
	"sync"
	"time"

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

var _ = Describe("CircuitBreaker time-windowed failures", func() {
	var cb *CircuitBreaker

	Describe("failure window expiry", func() {
		It("forgets failures after the window expires", func() {
			cb = NewCircuitBreaker(3, WithFailureWindow(100*time.Millisecond))
			cb.RecordFailure()
			cb.RecordFailure()
			cb.RecordFailure()
			Expect(cb.State()).To(Equal(CircuitOpen))

			time.Sleep(120 * time.Millisecond)

			Expect(cb.Allow()).To(BeTrue())
			Expect(cb.State()).To(Equal(CircuitClosed))
			Expect(cb.Failures()).To(BeZero())
		})

		It("does not forget failures within the window", func() {
			cb = NewCircuitBreaker(3, WithFailureWindow(10*time.Second))
			cb.RecordFailure()
			cb.RecordFailure()
			cb.RecordFailure()
			Expect(cb.State()).To(Equal(CircuitOpen))

			time.Sleep(10 * time.Millisecond)

			Expect(cb.Allow()).To(BeFalse())
			Expect(cb.State()).To(Equal(CircuitOpen))
		})

		It("allows requests immediately when window is disabled", func() {
			cb = NewCircuitBreaker(3)
			cb.RecordFailure()
			cb.RecordFailure()
			cb.RecordFailure()
			Expect(cb.State()).To(Equal(CircuitOpen))

			Expect(cb.Allow()).To(BeFalse())
		})
	})

	Describe("auto-reset after timeout", func() {
		It("transitions to HalfOpen after timeout expires", func() {
			cb = NewCircuitBreaker(3, WithHalfOpenTimeout(100*time.Millisecond))
			cb.RecordFailure()
			cb.RecordFailure()
			cb.RecordFailure()
			Expect(cb.State()).To(Equal(CircuitOpen))

			time.Sleep(120 * time.Millisecond)

			Expect(cb.Allow()).To(BeTrue())
			Expect(cb.State()).To(Equal(CircuitHalfOpen))
		})

		It("allows exactly one request after auto-reset", func() {
			cb = NewCircuitBreaker(3, WithHalfOpenTimeout(100*time.Millisecond))
			cb.RecordFailure()
			cb.RecordFailure()
			cb.RecordFailure()

			time.Sleep(120 * time.Millisecond)

			Expect(cb.Allow()).To(BeTrue())
			Expect(cb.Allow()).To(BeFalse())
		})

		It("does not auto-reset within timeout", func() {
			cb = NewCircuitBreaker(3, WithHalfOpenTimeout(10*time.Second))
			cb.RecordFailure()
			cb.RecordFailure()
			cb.RecordFailure()
			Expect(cb.State()).To(Equal(CircuitOpen))

			time.Sleep(10 * time.Millisecond)

			Expect(cb.Allow()).To(BeFalse())
		})

		It("auto-resets when timeout is disabled", func() {
			cb = NewCircuitBreaker(3)
			cb.RecordFailure()
			cb.RecordFailure()
			cb.RecordFailure()
			Expect(cb.State()).To(Equal(CircuitOpen))

			Expect(cb.Allow()).To(BeFalse())
		})
	})

	Describe("combined failure window and auto-reset", func() {
		It("resets both failures and state after both timeouts expire", func() {
			cb = NewCircuitBreaker(3,
				WithFailureWindow(50*time.Millisecond),
				WithHalfOpenTimeout(50*time.Millisecond),
			)
			cb.RecordFailure()
			cb.RecordFailure()
			cb.RecordFailure()
			Expect(cb.State()).To(Equal(CircuitOpen))
			Expect(cb.Failures()).To(Equal(3))

			time.Sleep(100 * time.Millisecond)

			Expect(cb.Allow()).To(BeTrue())
			Expect(cb.State()).To(Equal(CircuitClosed))
			Expect(cb.Failures()).To(BeZero())
		})
	})
})

var _ = Describe("CircuitBreaker RecordTypedFailure", func() {
	var cb *CircuitBreaker

	BeforeEach(func() {
		cb = NewCircuitBreaker(5)
	})

	Describe("non-retriable errors", func() {
		It("opens circuit immediately on first non-retriable failure", func() {
			Expect(cb.State()).To(Equal(CircuitClosed))
			Expect(cb.Failures()).To(BeZero())

			cb.RecordTypedFailure(false)

			Expect(cb.State()).To(Equal(CircuitOpen))
			Expect(cb.Failures()).To(Equal(1))
		})

		It("opens circuit immediately regardless of maxFailures threshold", func() {
			cb = NewCircuitBreaker(100)
			cb.RecordTypedFailure(false)

			Expect(cb.State()).To(Equal(CircuitOpen))
		})

		It("opens circuit from HalfOpen on non-retriable failure", func() {
			for range 5 {
				cb.RecordFailure()
			}
			cb.Reset()
			Expect(cb.State()).To(Equal(CircuitHalfOpen))

			cb.RecordTypedFailure(false)
			Expect(cb.State()).To(Equal(CircuitOpen))
		})
	})

	Describe("retriable errors", func() {
		It("does not open circuit before reaching maxFailures", func() {
			cb.RecordTypedFailure(true)
			Expect(cb.State()).To(Equal(CircuitClosed))
			Expect(cb.Failures()).To(Equal(1))

			cb.RecordTypedFailure(true)
			Expect(cb.State()).To(Equal(CircuitClosed))
			Expect(cb.Failures()).To(Equal(2))
		})

		It("opens circuit after reaching maxFailures", func() {
			for range 5 {
				cb.RecordTypedFailure(true)
			}
			Expect(cb.State()).To(Equal(CircuitOpen))
			Expect(cb.Failures()).To(Equal(5))
		})

		It("opens circuit from HalfOpen on retriable failure", func() {
			for range 5 {
				cb.RecordFailure()
			}
			cb.Reset()
			cb.Allow()

			cb.RecordTypedFailure(true)
			Expect(cb.State()).To(Equal(CircuitOpen))
		})
	})
})
