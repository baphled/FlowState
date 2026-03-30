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

var _ streaming.EventConsumer = (*api.WSConsumer)(nil)
var _ streaming.HarnessEventConsumer = (*api.WSConsumer)(nil)

var _ = Describe("WSConsumer EventConsumer implementation", func() {
	It("WriteEventToMsg converts a PlanArtifactEvent to WSChunkMsg with event_type and event_data", func() {
		consumer := api.NewWSConsumer(context.TODO(), nil)
		event := streaming.PlanArtifactEvent{
			Content: "## Plan\n1. Step one",
			Format:  "markdown",
			AgentID: "planner",
		}

		msg := consumer.WriteEventToMsg(event)
		Expect(msg.EventType).To(Equal("plan_artifact"))
		Expect(msg.EventData).NotTo(BeNil())
	})

	It("WriteEventToMsg converts a ReviewVerdictEvent to WSChunkMsg", func() {
		consumer := api.NewWSConsumer(context.TODO(), nil)
		event := streaming.ReviewVerdictEvent{
			Verdict:    "approved",
			Confidence: 0.95,
			Issues:     []string{"minor: naming"},
			AgentID:    "reviewer",
		}

		msg := consumer.WriteEventToMsg(event)
		Expect(msg.EventType).To(Equal("review_verdict"))
		Expect(msg.EventData).NotTo(BeNil())
	})

	It("WriteEventToMsg converts a StatusTransitionEvent to WSChunkMsg", func() {
		consumer := api.NewWSConsumer(context.TODO(), nil)
		event := streaming.StatusTransitionEvent{
			From:    "planning",
			To:      "executing",
			AgentID: "orchestrator",
		}

		msg := consumer.WriteEventToMsg(event)
		Expect(msg.EventType).To(Equal("status_transition"))
		Expect(msg.EventData).NotTo(BeNil())
	})

	It("WriteEventToMsg converts a DelegationEvent to WSChunkMsg", func() {
		consumer := api.NewWSConsumer(context.TODO(), nil)
		event := streaming.DelegationEvent{
			SourceAgent: "orchestrator",
			TargetAgent: "engineer",
			Status:      "started",
		}

		msg := consumer.WriteEventToMsg(event)
		Expect(msg.EventType).To(Equal("delegation"))
		Expect(msg.EventData).NotTo(BeNil())
	})

	It("WriteEventToMsg event_data serialises to JSON with type discriminator", func() {
		consumer := api.NewWSConsumer(context.TODO(), nil)
		event := streaming.PlanArtifactEvent{
			Content: "plan content",
			Format:  "markdown",
		}

		msg := consumer.WriteEventToMsg(event)
		data, err := json.Marshal(msg)
		Expect(err).NotTo(HaveOccurred())

		var decoded map[string]interface{}
		Expect(json.Unmarshal(data, &decoded)).To(Succeed())
		Expect(decoded).To(HaveKey("event_type"))
		Expect(decoded["event_type"]).To(Equal("plan_artifact"))
		Expect(decoded).To(HaveKey("event_data"))
	})
})

var _ = Describe("WSConsumer HarnessEventConsumer implementation", func() {
	It("WriteHarnessRetryToMsg converts content to WSChunkMsg with harness_retry event type", func() {
		consumer := api.NewWSConsumer(context.TODO(), nil)

		msg := consumer.WriteHarnessRetryToMsg("validation failed, retrying")
		Expect(msg.EventType).To(Equal("harness_retry"))
		Expect(msg.Content).To(Equal("validation failed, retrying"))
	})

	It("WriteAttemptStartToMsg converts content to WSChunkMsg with harness_attempt_start event type", func() {
		consumer := api.NewWSConsumer(context.TODO(), nil)

		msg := consumer.WriteAttemptStartToMsg("attempt 2 of 3")
		Expect(msg.EventType).To(Equal("harness_attempt_start"))
		Expect(msg.Content).To(Equal("attempt 2 of 3"))
	})

	It("WriteCompleteToMsg converts content to WSChunkMsg with harness_complete event type", func() {
		consumer := api.NewWSConsumer(context.TODO(), nil)

		msg := consumer.WriteCompleteToMsg("score: 0.95, attempts: 2")
		Expect(msg.EventType).To(Equal("harness_complete"))
		Expect(msg.Content).To(Equal("score: 0.95, attempts: 2"))
	})

	It("WriteCriticFeedbackToMsg converts content to WSChunkMsg with harness_critic_feedback event type", func() {
		consumer := api.NewWSConsumer(context.TODO(), nil)

		msg := consumer.WriteCriticFeedbackToMsg("missing error handling section")
		Expect(msg.EventType).To(Equal("harness_critic_feedback"))
		Expect(msg.Content).To(Equal("missing error handling section"))
	})

	It("serialises harness retry message to JSON correctly", func() {
		consumer := api.NewWSConsumer(context.TODO(), nil)
		msg := consumer.WriteHarnessRetryToMsg("retry reason")

		data, err := json.Marshal(msg)
		Expect(err).NotTo(HaveOccurred())

		var decoded map[string]interface{}
		Expect(json.Unmarshal(data, &decoded)).To(Succeed())
		Expect(decoded["event_type"]).To(Equal("harness_retry"))
		Expect(decoded["content"]).To(Equal("retry reason"))
	})
})

var _ = Describe("BuildWSChunkMsg EventType extraction", func() {
	It("populates EventType when chunk has EventType set", func() {
		chunk := provider.StreamChunk{
			Content:   "plan content",
			EventType: "plan_artifact",
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.EventType).To(Equal("plan_artifact"))
	})

	It("populates EventType for harness_retry event chunks", func() {
		chunk := provider.StreamChunk{
			Content:   "validation failed",
			EventType: "harness_retry",
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.EventType).To(Equal("harness_retry"))
	})

	It("populates EventType for status_transition event chunks", func() {
		chunk := provider.StreamChunk{
			EventType: "status_transition",
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.EventType).To(Equal("status_transition"))
	})

	It("populates EventType for review_verdict event chunks", func() {
		chunk := provider.StreamChunk{
			EventType: "review_verdict",
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.EventType).To(Equal("review_verdict"))
	})

	It("leaves EventType empty when chunk has no EventType", func() {
		chunk := provider.StreamChunk{
			Content: "plain text",
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.EventType).To(BeEmpty())
	})

	It("populates both EventType and Progress when both are present", func() {
		progressEvent := streaming.ProgressEvent{
			TaskID:        "task_123",
			ToolCallCount: 5,
			AgentID:       "agent_456",
		}
		chunk := provider.StreamChunk{
			Content:   "progress update",
			EventType: "progress",
			Event:     progressEvent,
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.EventType).To(Equal("progress"))
		Expect(msg.Progress).NotTo(BeNil())
		Expect(msg.Progress.TaskID).To(Equal("task_123"))
	})

	It("serialises EventType in JSON output", func() {
		chunk := provider.StreamChunk{
			Content:   "plan output",
			EventType: "plan_artifact",
		}

		msg := api.BuildWSChunkMsg(chunk)
		data, err := json.Marshal(msg)
		Expect(err).NotTo(HaveOccurred())

		var decoded map[string]interface{}
		Expect(json.Unmarshal(data, &decoded)).To(Succeed())
		Expect(decoded).To(HaveKey("event_type"))
		Expect(decoded["event_type"]).To(Equal("plan_artifact"))
	})

	It("omits event_type from JSON when empty", func() {
		chunk := provider.StreamChunk{
			Content: "plain text",
		}

		msg := api.BuildWSChunkMsg(chunk)
		data, err := json.Marshal(msg)
		Expect(err).NotTo(HaveOccurred())

		var decoded map[string]interface{}
		Expect(json.Unmarshal(data, &decoded)).To(Succeed())
		Expect(decoded).NotTo(HaveKey("event_type"))
	})

	It("populates EventData when chunk carries a typed Event", func() {
		event := streaming.PlanArtifactEvent{
			Content: "plan content",
			Format:  "markdown",
		}
		chunk := provider.StreamChunk{
			EventType: "plan_artifact",
			Event:     event,
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.EventType).To(Equal("plan_artifact"))
		Expect(msg.EventData).NotTo(BeNil())
	})

	It("leaves EventData nil when chunk has no typed Event", func() {
		chunk := provider.StreamChunk{
			EventType: "harness_retry",
			Content:   "retry content",
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.EventType).To(Equal("harness_retry"))
		Expect(msg.EventData).To(BeNil())
	})

	It("populates delegation start time fields", func() {
		now := time.Now()
		delegationInfo := &provider.DelegationInfo{
			SourceAgent: "orchestrator",
			TargetAgent: "engineer",
			Status:      "started",
			StartedAt:   &now,
		}
		chunk := provider.StreamChunk{
			DelegationInfo: delegationInfo,
		}

		msg := api.BuildWSChunkMsg(chunk)
		Expect(msg.Delegation).NotTo(BeNil())
		Expect(msg.Delegation.StartedAt).To(Equal(&now))
	})
})
