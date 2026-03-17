package context_test

import (
	"github.com/baphled/flowstate/internal/context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TokenBudget", func() {
	Describe("ApproximateCounter", func() {
		var counter *context.ApproximateCounter

		BeforeEach(func() {
			counter = context.NewApproximateCounter()
		})

		Context("Count", func() {
			It("returns positive count for non-empty text", func() {
				Expect(counter.Count("hello world")).To(BeNumerically(">", 0))
			})

			It("returns zero for empty text", func() {
				Expect(counter.Count("")).To(Equal(0))
			})
		})

		Context("ModelLimit", func() {
			It("returns 128000 for gpt-4o", func() {
				Expect(counter.ModelLimit("gpt-4o")).To(Equal(128000))
			})

			It("returns 200000 for claude models", func() {
				Expect(counter.ModelLimit("claude-sonnet")).To(Equal(200000))
			})

			It("returns 4096 for unknown models", func() {
				Expect(counter.ModelLimit("unknown-model")).To(Equal(4096))
			})
		})
	})

	Describe("TiktokenCounter", func() {
		var counter *context.TiktokenCounter

		BeforeEach(func() {
			counter = context.NewTiktokenCounter()
		})

		Context("Count", func() {
			It("returns positive count for non-empty text", func() {
				Expect(counter.Count("hello world")).To(BeNumerically(">", 0))
			})
		})

		Context("ModelLimit", func() {
			It("returns 128000 for gpt-4o", func() {
				Expect(counter.ModelLimit("gpt-4o")).To(Equal(128000))
			})

			It("returns 200000 for claude models", func() {
				Expect(counter.ModelLimit("claude-sonnet")).To(Equal(200000))
			})

			It("returns 4096 for unknown models", func() {
				Expect(counter.ModelLimit("unknown-model")).To(Equal(4096))
			})
		})
	})

	Describe("TokenBudget", func() {
		var budget *context.TokenBudget

		BeforeEach(func() {
			budget = context.NewTokenBudget(1000)
		})

		Context("Remaining", func() {
			It("returns total when nothing reserved", func() {
				Expect(budget.Remaining()).To(Equal(1000))
			})

			It("returns correct value after reservation", func() {
				budget.Reserve("system", 100)
				Expect(budget.Remaining()).To(Equal(900))
			})
		})

		Context("CanFit", func() {
			BeforeEach(func() {
				budget.Reserve("system", 100)
			})

			It("returns true when tokens fit", func() {
				Expect(budget.CanFit(900)).To(BeTrue())
			})

			It("returns false when tokens exceed remaining", func() {
				Expect(budget.CanFit(901)).To(BeFalse())
			})
		})

		Context("Reset", func() {
			It("restores remaining to total", func() {
				budget.Reserve("system", 500)
				budget.Reset()
				Expect(budget.Remaining()).To(Equal(1000))
			})
		})

		Context("UsedByCategory", func() {
			It("returns reserved amount for category", func() {
				budget.Reserve("system", 100)
				Expect(budget.UsedByCategory("system")).To(Equal(100))
			})

			It("returns zero for unknown category", func() {
				Expect(budget.UsedByCategory("unknown")).To(Equal(0))
			})
		})
	})
})
