package lifecycle_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine/lifecycle"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("PostProcessCtx", func() {
	It("carries session id, messages, and tools-called count", func() {
		ctx := lifecycle.PostProcessCtx{
			SessionID:   "sess-post",
			Messages:    []provider.Message{{Role: "assistant"}},
			ToolsCalled: 3,
		}
		Expect(ctx.SessionID).To(Equal("sess-post"))
		Expect(ctx.Messages).To(HaveLen(1))
		Expect(ctx.ToolsCalled).To(Equal(3))
	})

	It("defaults to a zero value with no messages and no tool calls", func() {
		var ctx lifecycle.PostProcessCtx
		Expect(ctx.SessionID).To(BeEmpty())
		Expect(ctx.Messages).To(BeNil())
		Expect(ctx.ToolsCalled).To(BeZero())
	})
})
