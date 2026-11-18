package app

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BudgetPool", func() {
	It("grants the full request while budget remains", func() {
		p := NewBudgetPool(100)
		Expect(p.Reserve(40)).To(Equal(int64(40)))
	})

	It("leaves the granted budget in the remaining pool", func() {
		p := NewBudgetPool(100)
		_ = p.Reserve(40)
		Expect(p.Remaining()).To(Equal(int64(60)))
	})

	It("grants a partial amount when the pool is nearly spent", func() {
		p := NewBudgetPool(50)
		Expect(p.Reserve(80)).To(Equal(int64(50)))
	})

	It("leaves nothing remaining after an overdrawn reserve", func() {
		p := NewBudgetPool(50)
		_ = p.Reserve(80)
		Expect(p.Remaining()).To(Equal(int64(0)))
	})

	It("grants zero once the pool is exhausted", func() {
		p := NewBudgetPool(10)
		_ = p.Reserve(10)
		Expect(p.Reserve(1)).To(Equal(int64(0)))
	})

	It("grants the full request from an unlimited pool", func() {
		p := NewBudgetPool(0)
		Expect(p.Reserve(1000)).To(Equal(int64(1000)))
	})

	It("keeps granting from an unlimited pool after draws", func() {
		p := NewBudgetPool(0)
		_ = p.Reserve(1000)
		Expect(p.Reserve(1000)).To(Equal(int64(1000)))
	})

	It("returns released budget to the remaining pool", func() {
		p := NewBudgetPool(30)
		_ = p.Reserve(30)
		p.Release(12)
		Expect(p.Remaining()).To(Equal(int64(12)))
	})

	It("re-grants released budget on the next reserve", func() {
		p := NewBudgetPool(30)
		_ = p.Reserve(30)
		p.Release(12)
		Expect(p.Reserve(12)).To(Equal(int64(12)))
	})

	It("ignores non-positive releases", func() {
		p := NewBudgetPool(5)
		_ = p.Reserve(5)
		p.Release(-3)
		p.Release(0)
		Expect(p.Remaining()).To(Equal(int64(0)))
	})
})
