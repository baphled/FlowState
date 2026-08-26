package delegation

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SpawnLimits", func() {
	It("returns the default spawn limits", func() {
		limits := DefaultSpawnLimits()

		Expect(limits.MaxDepth).To(Equal(5))
		Expect(limits.MaxConcurrentPerSession).To(Equal(10))
		Expect(limits.MaxTotalBudget).To(Equal(50))
		Expect(limits.StaleTimeout).To(Equal(45 * time.Minute))
	})

	It("checks depth and budget thresholds inclusively", func() {
		limits := SpawnLimits{MaxDepth: 5, MaxTotalBudget: 50}

		Expect(limits.ExceedsDepth(4)).To(BeFalse())
		Expect(limits.ExceedsDepth(5)).To(BeTrue())
		Expect(limits.ExceedsBudget(49)).To(BeFalse())
		Expect(limits.ExceedsBudget(50)).To(BeTrue())
	})
})
