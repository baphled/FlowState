package api_test

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
)

var _ = Describe("WebSocket delegation event forwarding", func() {

	It("populates Delegation field when StreamChunk has DelegationInfo", func() {
		delegationInfo := &provider.DelegationInfo{
			SourceAgent: "orchestrator",
			TargetAgent: "senior-engineer",
			Status:      "in_progress",
			Description: "Implementing feature X",
		}
		chunk := provider.StreamChunk{
			Content:        "Processing...",
			DelegationInfo: delegationInfo,
			Done:           false,
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.Delegation).NotTo(BeNil())
		Expect(msg.Delegation.SourceAgent).To(Equal("orchestrator"))
		Expect(msg.Delegation.TargetAgent).To(Equal("senior-engineer"))
		Expect(msg.Delegation.Status).To(Equal("in_progress"))
		Expect(msg.Delegation.Description).To(Equal("Implementing feature X"))
	})

	It("leaves Delegation nil when StreamChunk has no DelegationInfo", func() {
		chunk := provider.StreamChunk{
			Content: "Response",
			Done:    false,
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.Delegation).To(BeNil())
	})

	It("populates ToolCall field when StreamChunk has ToolCall", func() {
		toolCall := &provider.ToolCall{
			Name: "bash",
			ID:   "call_123",
		}
		chunk := provider.StreamChunk{
			Content:  "Calling tool",
			ToolCall: toolCall,
			Done:     false,
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.ToolCall).NotTo(BeNil())
		Expect(msg.ToolCall.Name).To(Equal("bash"))
		Expect(msg.ToolCall.ID).To(Equal("call_123"))
	})

	It("leaves ToolCall nil when StreamChunk has no ToolCall", func() {
		chunk := provider.StreamChunk{
			Content: "Response",
			Done:    false,
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.ToolCall).To(BeNil())
	})

	It("populates Progress field when StreamChunk Event is ProgressEvent", func() {
		progressEvent := streaming.ProgressEvent{
			TaskID:            "task_123",
			ToolCallCount:     5,
			LastTool:          "bash",
			ActiveDelegations: 2,
			ElapsedTime:       30 * time.Second,
			AgentID:           "agent_456",
		}
		chunk := provider.StreamChunk{
			Content: "Progress update",
			Event:   progressEvent,
			Done:    false,
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.Progress).NotTo(BeNil())
		Expect(msg.Progress.TaskID).To(Equal("task_123"))
		Expect(msg.Progress.ToolCallCount).To(Equal(5))
		Expect(msg.Progress.LastTool).To(Equal("bash"))
		Expect(msg.Progress.ActiveDelegations).To(Equal(2))
		Expect(msg.Progress.ElapsedTime).To(Equal(30 * time.Second))
		Expect(msg.Progress.AgentID).To(Equal("agent_456"))
	})

	It("leaves Progress nil when StreamChunk Event is not ProgressEvent", func() {
		chunk := provider.StreamChunk{
			Content: "Response",
			Done:    false,
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.Progress).To(BeNil())
	})

	It("preserves Content, Done, and Error fields when adding delegation fields", func() {
		chunk := provider.StreamChunk{
			Content: "Original content",
			Done:    false,
			Error:   nil,
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.Content).To(Equal("Original content"))
		Expect(msg.Done).To(BeFalse())
		Expect(msg.Error).To(Equal(""))
	})

	It("populates Error field correctly", func() {
		chunk := provider.StreamChunk{
			Content: "",
			Done:    false,
			Error:   nil,
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.Error).To(Equal(""))
	})

	It("serialises all fields to JSON correctly", func() {
		delegationInfo := &provider.DelegationInfo{
			SourceAgent: "orchestrator",
			TargetAgent: "engineer",
			Status:      "active",
		}
		progressEvent := streaming.ProgressEvent{
			ToolCallCount: 3,
		}
		chunk := provider.StreamChunk{
			Content:        "test",
			Done:           false,
			DelegationInfo: delegationInfo,
			Event:          progressEvent,
		}

		msg := api.BuildWSChunkMsg(chunk)
		data, err := json.Marshal(msg)
		Expect(err).NotTo(HaveOccurred())

		var decoded map[string]interface{}
		Expect(json.Unmarshal(data, &decoded)).To(Succeed())
		Expect(decoded).To(HaveKey("content"))
		Expect(decoded).To(HaveKey("delegation"))
		Expect(decoded).To(HaveKey("progress"))
	})
})

var _ = Describe("WSConsumer delegation interface implementation", func() {
	It("WriteDelegationToMsg converts DelegationEvent to WSChunkMsg", func() {
		consumer := api.NewWSConsumer(context.TODO(), nil)
		delegationEvent := streaming.DelegationEvent{
			SourceAgent:  "orchestrator",
			TargetAgent:  "senior-engineer",
			ChainID:      "chain_123",
			Status:       "in_progress",
			ModelName:    "claude-opus",
			ProviderName: "anthropic",
			Description:  "Implementing feature",
			ToolCalls:    5,
			LastTool:     "bash",
		}

		msg := consumer.WriteDelegationToMsg(delegationEvent)
		Expect(msg.Delegation).NotTo(BeNil())
		Expect(msg.Delegation.SourceAgent).To(Equal("orchestrator"))
		Expect(msg.Delegation.TargetAgent).To(Equal("senior-engineer"))
		Expect(msg.Delegation.Status).To(Equal("in_progress"))
	})

	It("WriteProgressToMsg converts ProgressEvent to WSChunkMsg", func() {
		consumer := api.NewWSConsumer(context.TODO(), nil)
		progressEvent := streaming.ProgressEvent{
			TaskID:            "task_123",
			ToolCallCount:     3,
			LastTool:          "bash",
			ActiveDelegations: 1,
			ElapsedTime:       15 * time.Second,
			AgentID:           "agent_456",
		}

		msg := consumer.WriteProgressToMsg(progressEvent)
		Expect(msg.Progress).NotTo(BeNil())
		Expect(msg.Progress.ToolCallCount).To(Equal(3))
		Expect(msg.Progress.AgentID).To(Equal("agent_456"))
	})
})
