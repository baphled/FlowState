package adapters_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/adapters"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("EventAdapter", func() {
	var (
		now time.Time
	)

	BeforeEach(func() {
		now = time.Now()
	})

	Describe("EventToMsg", func() {
		Context("when receiving a DelegationEvent", func() {
			It("should convert to a StreamChunkMsg with DelegationInfo", func() {
				event := streaming.DelegationEvent{
					SourceAgent:  "planner",
					TargetAgent:  "researcher",
					ChainID:      "chain-123",
					Status:       "started",
					ModelName:    "gpt-4",
					ProviderName: "openai",
					Description:  "Investigating user query",
					ToolCalls:    5,
					LastTool:     "search_web",
					StartedAt:    &now,
				}

				msg := adapters.EventToMsg(event)
				Expect(msg).To(BeAssignableToTypeOf(chat.StreamChunkMsg{}))

				chunk := msg.(chat.StreamChunkMsg)
				Expect(chunk.DelegationInfo).NotTo(BeNil())
				Expect(chunk.DelegationInfo.SourceAgent).To(Equal("planner"))
				Expect(chunk.DelegationInfo.TargetAgent).To(Equal("researcher"))
				Expect(chunk.DelegationInfo.ChainID).To(Equal("chain-123"))
				Expect(chunk.DelegationInfo.Status).To(Equal("started"))
				Expect(chunk.DelegationInfo.ModelName).To(Equal("gpt-4"))
				Expect(chunk.DelegationInfo.ProviderName).To(Equal("openai"))
				Expect(chunk.DelegationInfo.Description).To(Equal("Investigating user query"))
				Expect(chunk.DelegationInfo.ToolCalls).To(Equal(5))
				Expect(chunk.DelegationInfo.LastTool).To(Equal("search_web"))
				Expect(chunk.DelegationInfo.StartedAt).To(Equal(&now))
			})
		})

		Context("when receiving a StatusTransitionEvent", func() {
			It("should convert to a StreamChunkMsg with DelegationInfo", func() {
				event := streaming.StatusTransitionEvent{
					From:    "idle",
					To:      "researching",
					AgentID: "researcher",
				}

				msg := adapters.EventToMsg(event)
				Expect(msg).To(BeAssignableToTypeOf(chat.StreamChunkMsg{}))

				chunk := msg.(chat.StreamChunkMsg)
				Expect(chunk.DelegationInfo).NotTo(BeNil())
				Expect(chunk.DelegationInfo.TargetAgent).To(Equal("researcher"))
				Expect(chunk.DelegationInfo.Status).To(Equal("researching"))
				Expect(chunk.DelegationInfo.Description).To(ContainSubstring("Transitioning from idle to researching"))
			})
		})

		Context("when receiving a ReviewVerdictEvent", func() {
			It("should convert to a StreamChunkMsg with DelegationInfo", func() {
				event := streaming.ReviewVerdictEvent{
					Verdict:    "reject",
					Confidence: 0.8,
					Issues:     []string{"issue1", "issue2"},
					AgentID:    "reviewer",
				}

				msg := adapters.EventToMsg(event)
				Expect(msg).To(BeAssignableToTypeOf(chat.StreamChunkMsg{}))

				chunk := msg.(chat.StreamChunkMsg)
				Expect(chunk.DelegationInfo).NotTo(BeNil())
				Expect(chunk.DelegationInfo.TargetAgent).To(Equal("reviewer"))
				Expect(chunk.DelegationInfo.Status).To(Equal("reviewing"))
				Expect(chunk.DelegationInfo.Description).To(ContainSubstring("Verdict: reject"))
				Expect(chunk.DelegationInfo.Description).To(ContainSubstring("Confidence: 0.80"))
				Expect(chunk.DelegationInfo.Description).To(ContainSubstring("issue1, issue2"))
			})
		})

		Context("when receiving an unknown event", func() {
			It("should return nil", func() {
				event := streaming.TextChunkEvent{
					Content: "hello",
					AgentID: "bot",
				}

				msg := adapters.EventToMsg(event)
				Expect(msg).To(BeNil())
			})
		})
	})
})
