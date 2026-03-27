package provider

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ToolResultInfo", func() {
	It("stores content and error state", func() {
		info := &ToolResultInfo{
			Content: "output",
			IsError: false,
		}

		Expect(info.Content).To(Equal("output"))
		Expect(info.IsError).To(BeFalse())
	})
})

var _ = Describe("StreamChunk with tool result", func() {
	It("stores the tool result details", func() {
		chunk := StreamChunk{
			Content:   "test",
			Done:      false,
			EventType: "tool_result",
			ToolResult: &ToolResultInfo{
				Content: "tool output",
				IsError: false,
			},
		}

		Expect(chunk.ToolResult).NotTo(BeNil())
		Expect(chunk.ToolResult.Content).To(Equal("tool output"))
	})
})

var _ = Describe("DelegationInfo", func() {
	It("stores delegation metadata", func() {
		startedAt := time.Now().UTC().Truncate(time.Second)
		completedAt := startedAt.Add(2 * time.Second)

		info := &DelegationInfo{
			SourceAgent:  "planner",
			TargetAgent:  "explorer",
			ChainID:      "chain-1",
			ToolCalls:    3,
			LastTool:     "delegate",
			StartedAt:    &startedAt,
			CompletedAt:  &completedAt,
			Status:       "started",
			ModelName:    "claude-sonnet-4-6",
			ProviderName: "anthropic",
			Description:  "Exploring codebase for requirements",
		}

		Expect(info.SourceAgent).To(Equal("planner"))
		Expect(info.TargetAgent).To(Equal("explorer"))
		Expect(info.ChainID).To(Equal("chain-1"))
		Expect(info.ToolCalls).To(Equal(3))
		Expect(info.LastTool).To(Equal("delegate"))
		Expect(info.StartedAt).To(BeComparableTo(&startedAt))
		Expect(info.CompletedAt).To(BeComparableTo(&completedAt))
		Expect(info.Status).To(Equal("started"))
		Expect(info.ModelName).To(Equal("claude-sonnet-4-6"))
		Expect(info.ProviderName).To(Equal("anthropic"))
		Expect(info.Description).To(Equal("Exploring codebase for requirements"))
	})
})

var _ = Describe("StreamChunk with delegation info", func() {
	It("stores the delegation details", func() {
		chunk := StreamChunk{
			Content:   "",
			Done:      false,
			EventType: "delegation",
			DelegationInfo: &DelegationInfo{
				SourceAgent:  "planner",
				TargetAgent:  "plan-writer",
				ChainID:      "chain-2",
				ToolCalls:    1,
				LastTool:     "delegate",
				Status:       "started",
				ModelName:    "claude-sonnet-4-6",
				ProviderName: "anthropic",
				Description:  "Writing plan",
			},
		}

		Expect(chunk.DelegationInfo).NotTo(BeNil())
		Expect(chunk.DelegationInfo.SourceAgent).To(Equal("planner"))
		Expect(chunk.DelegationInfo.TargetAgent).To(Equal("plan-writer"))
		Expect(chunk.DelegationInfo.ChainID).To(Equal("chain-2"))
		Expect(chunk.DelegationInfo.ToolCalls).To(Equal(1))
		Expect(chunk.DelegationInfo.LastTool).To(Equal("delegate"))
		Expect(chunk.DelegationInfo.Status).To(Equal("started"))
	})
})

var _ = Describe("StreamChunk delegation info", func() {
	It("is nil by default", func() {
		chunk := StreamChunk{
			Content: "hello",
			Done:    false,
		}

		Expect(chunk.DelegationInfo).To(BeNil())
	})
})
