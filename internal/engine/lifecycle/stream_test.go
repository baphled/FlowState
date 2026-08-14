package lifecycle_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine/lifecycle"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("StreamCtx", func() {
	It("carries provider, agent id, messages, tool schemas, and result channel", func() {
		ch := make(chan<- provider.StreamChunk)
		ctx := lifecycle.StreamCtx{
			AgentID:     "agent-7",
			Messages:    []provider.Message{{Role: "user"}},
			ToolSchemas: []provider.Tool{{Name: "bash"}},
			ResultCh:    ch,
		}
		Expect(ctx.AgentID).To(Equal("agent-7"))
		Expect(ctx.Messages).To(HaveLen(1))
		Expect(ctx.ToolSchemas).To(HaveLen(1))
		Expect(ctx.ResultCh).To(BeIdenticalTo(ch))
	})

	It("defaults to a zero value with a nil channel", func() {
		var ctx lifecycle.StreamCtx
		Expect(ctx.AgentID).To(BeEmpty())
		Expect(ctx.Messages).To(BeNil())
		Expect(ctx.ResultCh).To(BeNil())
	})
})
