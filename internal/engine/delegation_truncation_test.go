package engine_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("Delegation result truncation", func() {
	var (
		delegateTool *engine.DelegateTool
	)

	BeforeEach(func() {
		delegateTool = engine.NewDelegateTool(nil, agent.Delegation{}, "source")
	})

	Describe("collectDelegationResult truncation", func() {
		It("returns truncated=true when content exceeds the byte cap", func() {
			oversized := strings.Repeat("A", engine.MaxDelegationResultBytesForTest+1)
			chunks := make(chan provider.StreamChunk, 1)
			chunks <- provider.StreamChunk{Content: oversized}
			close(chunks)

			result, err := engine.CollectDelegationResultForTest(delegateTool, chunks)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Truncated()).To(BeTrue())
			Expect(result.Response()).To(HaveSuffix("[...truncated]"))
		})

		It("returns truncated=false when content is under the byte cap", func() {
			chunks := make(chan provider.StreamChunk, 1)
			chunks <- provider.StreamChunk{Content: "small content"}
			close(chunks)

			result, err := engine.CollectDelegationResultForTest(delegateTool, chunks)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Truncated()).To(BeFalse())
			Expect(result.Response()).To(Equal("small content"))
		})

		It("drains remaining chunks after truncation without blocking", func() {
			chunkSize := engine.MaxDelegationResultBytesForTest/2 + 1
			largeContent := strings.Repeat("X", chunkSize)

			chunks := make(chan provider.StreamChunk, 4)
			chunks <- provider.StreamChunk{Content: largeContent}
			chunks <- provider.StreamChunk{Content: largeContent}
			chunks <- provider.StreamChunk{Content: "extra after cap"}
			chunks <- provider.StreamChunk{Content: "more extra"}
			close(chunks)

			result, err := engine.CollectDelegationResultForTest(delegateTool, chunks)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Truncated()).To(BeTrue())
			Expect(result.ToolCallCount()).To(Equal(4))
		})
	})
})
