package context_test

import (
	"github.com/baphled/flowstate/internal/context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mockResolver provides test model context limits.
type mockResolver struct {
	limits map[string]int
}

func (m *mockResolver) ResolveContextLength(provider, model string) int {
	key := provider + "/" + model
	if limit, ok := m.limits[key]; ok {
		return limit
	}
	return 0
}

var _ = Describe("TokenBudget", func() {
	Describe("DefaultModelContextFallback", func() {
		It("is 16384 — the post-bug-fix default that fits the 11-skill always-active bundle plus delegation tables", func() {
			Expect(context.DefaultModelContextFallback).To(Equal(16384))
		})
	})

	Describe("ApproximateCounter", func() {
		Context("Count", func() {
			It("returns positive count for non-empty text", func() {
				counter := context.NewApproximateCounter()
				Expect(counter.Count("hello world")).To(BeNumerically(">", 0))
			})

			It("returns zero for empty text", func() {
				counter := context.NewApproximateCounter()
				Expect(counter.Count("")).To(Equal(0))
			})
		})

		Context("ModelLimit with resolver", func() {
			It("returns resolver value for known models", func() {
				resolver := &mockResolver{limits: map[string]int{
					"anthropic/claude-sonnet-4-20250514": 200000,
					"openai/gpt-4o":                      128000,
				}}
				counter := context.NewApproximateCounterWithResolver(resolver, "anthropic")
				Expect(counter.ModelLimit("claude-sonnet-4-20250514")).To(Equal(200000))
			})

			It("returns the 16K default fallback when resolver returns 0", func() {
				resolver := &mockResolver{limits: map[string]int{}}
				counter := context.NewApproximateCounterWithResolver(resolver, "anthropic")
				Expect(counter.ModelLimit("unknown-model")).To(Equal(context.DefaultModelContextFallback))
			})

			It("returns the 16K default fallback when no resolver configured", func() {
				counter := context.NewApproximateCounter()
				Expect(counter.ModelLimit("any-model")).To(Equal(context.DefaultModelContextFallback))
			})

			It("honours an operator-supplied fallback override", func() {
				counter := context.NewApproximateCounter()
				counter.SetFallback(32768)
				Expect(counter.ModelLimit("any-model")).To(Equal(32768))
			})

			It("ignores a non-positive fallback override", func() {
				counter := context.NewApproximateCounter()
				counter.SetFallback(0)
				counter.SetFallback(-1)
				Expect(counter.ModelLimit("any-model")).To(Equal(context.DefaultModelContextFallback))
			})
		})
	})

	Describe("TiktokenCounter", func() {
		Context("Count", func() {
			It("returns positive count for non-empty text", func() {
				counter := context.NewTiktokenCounter()
				Expect(counter.Count("hello world")).To(BeNumerically(">", 0))
			})
		})

		Context("ModelLimit with resolver", func() {
			It("returns resolver value for known models", func() {
				resolver := &mockResolver{limits: map[string]int{
					"anthropic/claude-sonnet-4-20250514": 200000,
					"openai/gpt-4o":                      128000,
				}}
				counter := context.NewTiktokenCounterWithResolver(resolver, "anthropic")
				Expect(counter.ModelLimit("claude-sonnet-4-20250514")).To(Equal(200000))
			})

			It("returns the 16K default fallback when resolver returns 0", func() {
				resolver := &mockResolver{limits: map[string]int{}}
				counter := context.NewTiktokenCounterWithResolver(resolver, "anthropic")
				Expect(counter.ModelLimit("unknown-model")).To(Equal(context.DefaultModelContextFallback))
			})

			It("returns the 16K default fallback when no resolver configured", func() {
				counter := context.NewTiktokenCounter()
				Expect(counter.ModelLimit("any-model")).To(Equal(context.DefaultModelContextFallback))
			})

			It("honours an operator-supplied fallback override", func() {
				counter := context.NewTiktokenCounter()
				counter.SetFallback(32768)
				Expect(counter.ModelLimit("any-model")).To(Equal(32768))
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
