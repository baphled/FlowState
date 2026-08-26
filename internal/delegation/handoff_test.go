package delegation_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/delegation"
)

var _ = Describe("Handoff", func() {
	It("creates a valid handoff", func() {
		handoff := delegation.NewHandoff(delegation.Handoff{
			SourceAgent: "source-agent",
			TargetAgent: "target-agent",
			TaskType:    "task-type",
			ChainID:     "chain-123",
			Message:     "message",
			Feedback:    "feedback",
			Metadata:    map[string]string{"key": "value"},
		})

		Expect(handoff.SourceAgent).To(Equal("source-agent"))
		Expect(handoff.TargetAgent).To(Equal("target-agent"))
		Expect(handoff.TaskType).To(Equal("task-type"))
		Expect(handoff.ChainID).To(Equal("chain-123"))
		Expect(handoff.Message).To(Equal("message"))
		Expect(handoff.Feedback).To(Equal("feedback"))
		Expect(handoff.Metadata).To(HaveKeyWithValue("key", "value"))
		Expect(handoff.Validate()).NotTo(HaveOccurred())
	})

	It("returns an error when the source agent is empty", func() {
		handoff := delegation.NewHandoff(delegation.Handoff{TargetAgent: "target-agent", TaskType: "task-type", ChainID: "chain-123", Message: "message", Feedback: "feedback"})

		Expect(handoff.Validate()).To(MatchError(ContainSubstring("source agent")))
	})

	It("returns an error when the target agent is empty", func() {
		handoff := delegation.NewHandoff(delegation.Handoff{SourceAgent: "source-agent", TaskType: "task-type", ChainID: "chain-123", Message: "message", Feedback: "feedback"})

		Expect(handoff.Validate()).To(MatchError(ContainSubstring("target agent")))
	})

	It("returns an error when the chain ID is empty", func() {
		handoff := delegation.NewHandoff(delegation.Handoff{SourceAgent: "source-agent", TargetAgent: "target-agent", TaskType: "task-type", Message: "message", Feedback: "feedback"})

		Expect(handoff.Validate()).To(MatchError(ContainSubstring("chain id")))
	})

	It("round-trips through JSON without losing fields", func() {
		original := delegation.NewHandoff(delegation.Handoff{SourceAgent: "source-agent", TargetAgent: "target-agent", TaskType: "task-type", ChainID: "chain-123", Message: "message", Feedback: "feedback", Metadata: map[string]string{"alpha": "beta"}})

		payload, err := json.Marshal(original)
		Expect(err).NotTo(HaveOccurred())

		var decoded delegation.Handoff
		Expect(json.Unmarshal(payload, &decoded)).To(Succeed())
		Expect(decoded).To(Equal(*original))
	})

	It("preserves arbitrary metadata key-value pairs", func() {
		handoff := delegation.NewHandoff(delegation.Handoff{SourceAgent: "source-agent", TargetAgent: "target-agent", TaskType: "task-type", ChainID: "chain-123", Message: "message", Feedback: "feedback", Metadata: map[string]string{"one": "1", "two": "2"}})

		Expect(handoff.Metadata).To(HaveLen(2))
		Expect(handoff.Metadata).To(HaveKeyWithValue("one", "1"))
		Expect(handoff.Metadata).To(HaveKeyWithValue("two", "2"))
	})
})
