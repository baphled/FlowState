package lifecycle_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine/lifecycle"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("ContextAssemblyCtx", func() {
	It("carries messages, token budget, and manifest fields", func() {
		manifest := &agent.Manifest{ID: "agent-1"}
		ctx := lifecycle.ContextAssemblyCtx{
			Messages:    []provider.Message{{Role: "user", Content: "hello"}},
			TokenBudget: 4096,
			Manifest:    manifest,
		}
		Expect(ctx.Messages).To(HaveLen(1))
		Expect(ctx.Messages[0].Content).To(Equal("hello"))
		Expect(ctx.TokenBudget).To(Equal(4096))
		Expect(ctx.Manifest).To(BeIdenticalTo(manifest))
	})

	It("defaults to a zero value with an empty message slice and nil manifest", func() {
		var ctx lifecycle.ContextAssemblyCtx
		Expect(ctx.Messages).To(BeNil())
		Expect(ctx.TokenBudget).To(BeZero())
		Expect(ctx.Manifest).To(BeNil())
	})
})
