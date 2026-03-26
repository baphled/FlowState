package streaming_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
)

var _ = Describe("Event", func() {
	Describe("EventType", func() {
		Context("when called on TextChunkEvent", func() {
			It("returns text_chunk", func() {
				event := &streaming.TextChunkEvent{Content: "Hello"}
				Expect(event.EventType()).To(Equal("text_chunk"))
			})
		})

		Context("when called on ToolCallEvent", func() {
			It("returns tool_call", func() {
				event := &streaming.ToolCallEvent{
					Name:     "test_tool",
					Args:     map[string]any{"key": "value"},
					Result:   "success",
					Duration: 100 * time.Millisecond,
				}
				Expect(event.EventType()).To(Equal("tool_call"))
			})
		})

		Context("when called on DelegationEvent", func() {
			It("returns delegation", func() {
				event := &streaming.DelegationEvent{
					Source:  "planner",
					Target:  "executor",
					ChainID: "chain-123",
					Status:  "started",
				}
				Expect(event.EventType()).To(Equal("delegation"))
			})
		})

		Context("when called on CoordinationStoreEvent", func() {
			It("returns coordination_store", func() {
				event := &streaming.CoordinationStoreEvent{
					Operation: "get",
					Key:       "state:agent-1",
					ChainID:   "chain-123",
				}
				Expect(event.EventType()).To(Equal("coordination_store"))
			})
		})

		Context("when called on StatusTransitionEvent", func() {
			It("returns status_transition", func() {
				event := &streaming.StatusTransitionEvent{
					From: "planning",
					To:   "executing",
				}
				Expect(event.EventType()).To(Equal("status_transition"))
			})
		})

		Context("when called on PlanArtifactEvent", func() {
			It("returns plan_artifact", func() {
				event := &streaming.PlanArtifactEvent{
					Content: "plan content",
					Format:  "json",
				}
				Expect(event.EventType()).To(Equal("plan_artifact"))
			})
		})

		Context("when called on ReviewVerdictEvent", func() {
			It("returns review_verdict", func() {
				event := &streaming.ReviewVerdictEvent{
					Verdict:    "approve",
					Confidence: 0.95,
					Issues:     []string{},
				}
				Expect(event.EventType()).To(Equal("review_verdict"))
			})
		})
	})
})

var _ = Describe("Event JSON Serialisation", func() {
	Context("when serialising a TextChunkEvent", func() {
		It("includes the type discriminator", func() {
			event := &streaming.TextChunkEvent{Content: "Hello world"}
			data, err := event.MarshalJSON()
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring(`"type":"text_chunk"`))
			Expect(string(data)).To(ContainSubstring(`"content":"Hello world"`))
		})
	})

	Context("when serialising a ToolCallEvent", func() {
		It("includes all fields with type discriminator", func() {
			event := &streaming.ToolCallEvent{
				Name:     "search",
				Args:     map[string]any{"query": "test"},
				Result:   "found 5 results",
				Duration: 50 * time.Millisecond,
			}
			data, err := event.MarshalJSON()
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring(`"type":"tool_call"`))
			Expect(string(data)).To(ContainSubstring(`"name":"search"`))
			Expect(string(data)).To(ContainSubstring(`"duration":50`))
		})
	})

	Context("when serialising a ReviewVerdictEvent", func() {
		It("includes all fields with type discriminator", func() {
			event := &streaming.ReviewVerdictEvent{
				Verdict:    "reject",
				Confidence: 0.75,
				Issues:     []string{"missing tests", "lint errors"},
			}
			data, err := event.MarshalJSON()
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring(`"type":"review_verdict"`))
			Expect(string(data)).To(ContainSubstring(`"verdict":"reject"`))
			Expect(string(data)).To(ContainSubstring(`"confidence":0.75`))
			Expect(string(data)).To(ContainSubstring(`"issues"`))
		})
	})
})
