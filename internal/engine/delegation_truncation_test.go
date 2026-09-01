package engine_test

import (
	"context"
	"strings"
	"time"

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

var _ = Describe("Tool error capture in delegation results", func() {
	var (
		delegateTool *engine.DelegateTool
	)

	BeforeEach(func() {
		delegateTool = engine.NewDelegateTool(nil, agent.Delegation{}, "source")
	})

	Describe("collectDelegationResult", func() {
		It("captures tool error content when IsError is true", func() {
			errMsg := "permission denied: tool bash is not in the allowlist"
			chunks := make(chan provider.StreamChunk, 1)
			chunks <- provider.StreamChunk{
				ToolResult: &provider.ToolResultInfo{
					Content: errMsg,
					IsError: true,
				},
			}
			close(chunks)

			result, err := engine.CollectDelegationResultForTest(delegateTool, chunks)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Response()).To(ContainSubstring(errMsg))
		})

		It("does not capture non-error tool result content", func() {
			chunks := make(chan provider.StreamChunk, 2)
			chunks <- provider.StreamChunk{
				Content: "assistant text about the result",
			}
			chunks <- provider.StreamChunk{
				ToolResult: &provider.ToolResultInfo{
					Content: "some large tool output",
					IsError: false,
				},
			}
			close(chunks)

			result, err := engine.CollectDelegationResultForTest(delegateTool, chunks)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Response()).To(Equal("assistant text about the result"),
				"non-error tool result content should not appear in the aggregated response — the sub-assistant's own text already summarises it")
		})

		It("drops thinking chunks from the delegation response", func() {
			chunks := make(chan provider.StreamChunk, 3)
			chunks <- provider.StreamChunk{Thinking: "Thinking about cool thing."}
			chunks <- provider.StreamChunk{Content: "I thought about cool thing, and it is this."}
			chunks <- provider.StreamChunk{Done: true}
			close(chunks)

			result, err := engine.CollectDelegationResultForTest(delegateTool, chunks)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Response()).To(Equal("I thought about cool thing, and it is this."),
				"child-agent thinking must not be glued onto the delegation tool_result — it is persisted separately as role='thinking' rows in the child session")
		})

		It("interleaves assistant text and tool errors correctly", func() {
			chunks := make(chan provider.StreamChunk, 3)
			chunks <- provider.StreamChunk{Content: "I'll use bash to write the file."}
			chunks <- provider.StreamChunk{
				ToolResult: &provider.ToolResultInfo{
					Content: "permission denied: bash",
					IsError: true,
				},
			}
			chunks <- provider.StreamChunk{Content: "The file has been written."}
			close(chunks)

			result, err := engine.CollectDelegationResultForTest(delegateTool, chunks)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Response()).To(ContainSubstring("I'll use bash"))
			Expect(result.Response()).To(ContainSubstring("permission denied: bash"))
			Expect(result.Response()).To(ContainSubstring("The file has been written"),
				"assistant text after the tool error should still be captured")
		})
	})

	Describe("collectWithProgress", func() {
		It("captures tool error content when IsError is true", func() {
			ctx := context.Background()
			chunks := make(chan provider.StreamChunk, 1)
			chunks <- provider.StreamChunk{
				ToolResult: &provider.ToolResultInfo{
					Content: "tool not available",
					IsError: true,
				},
			}
			close(chunks)

			result, err := engine.CollectWithProgressForTest(ctx, delegateTool, chunks, time.Now())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Response()).To(ContainSubstring("tool not available"))
		})

		It("does not capture non-error tool result content", func() {
			ctx := context.Background()
			chunks := make(chan provider.StreamChunk, 2)
			chunks <- provider.StreamChunk{Content: "assistant summary"}
			chunks <- provider.StreamChunk{
				ToolResult: &provider.ToolResultInfo{
					Content: "large output",
					IsError: false,
				},
			}
			close(chunks)

			result, err := engine.CollectWithProgressForTest(ctx, delegateTool, chunks, time.Now())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Response()).To(Equal("assistant summary"))
		})
		It("drops thinking chunks from the delegation response", func() {
			ctx := context.Background()
			chunks := make(chan provider.StreamChunk, 2)
			chunks <- provider.StreamChunk{Thinking: "reasoning about the task"}
			chunks <- provider.StreamChunk{Content: "final answer"}
			close(chunks)

			result, err := engine.CollectWithProgressForTest(ctx, delegateTool, chunks, time.Now())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Response()).To(Equal("final answer"))
		})
	})
})
