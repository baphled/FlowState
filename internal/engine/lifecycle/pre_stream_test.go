package lifecycle_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine/lifecycle"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("PreStreamCtx", func() {
	It("carries messages, token budget, and manifest fields", func() {
		ctx := lifecycle.PreStreamCtx{
			Messages:    []provider.Message{{Role: "system"}},
			TokenBudget: 2048,
		}
		Expect(ctx.Messages).To(HaveLen(1))
		Expect(ctx.Messages[0].Role).To(Equal("system"))
		Expect(ctx.TokenBudget).To(Equal(2048))
	})

	It("defaults to a zero value with no messages", func() {
		var ctx lifecycle.PreStreamCtx
		Expect(ctx.Messages).To(BeNil())
		Expect(ctx.TokenBudget).To(BeZero())
	})
})
