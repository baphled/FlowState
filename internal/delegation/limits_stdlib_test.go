package delegation

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestSpawnLimits(t *testing.T) {
	RegisterTestingT(t)

	limits := DefaultSpawnLimits()

	Expect(limits.MaxDepth).To(Equal(5))
	Expect(limits.MaxConcurrentPerSession).To(Equal(10))
	Expect(limits.MaxTotalBudget).To(Equal(50))
	Expect(limits.StaleTimeout).To(Equal(45 * time.Minute))
	Expect(limits.ExceedsDepth(4)).To(BeFalse())
	Expect(limits.ExceedsDepth(5)).To(BeTrue())
	Expect(limits.ExceedsBudget(49)).To(BeFalse())
	Expect(limits.ExceedsBudget(50)).To(BeTrue())
}
