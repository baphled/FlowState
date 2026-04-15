package chat_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("swarm activity pane wiring", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	Describe("View renders secondary pane when terminal is wide enough", func() {
		It("includes the Activity Timeline header at 100x24", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			view := intent.View()
			Expect(view).To(ContainSubstring("Activity Timeline"))
		})
	})

	Describe("View falls back to single-pane below dual-pane threshold", func() {
		It("omits the Activity Timeline header at 79x24", func() {
			intent.Update(tea.WindowSizeMsg{Width: 79, Height: 24})

			view := intent.View()
			Expect(view).NotTo(ContainSubstring("Activity Timeline"))
		})
	})

	Describe("Intent constructor seeds the swarm event store", func() {
		It("initialises an empty MemorySwarmStore with capacity 15", func() {
			store := intent.SwarmStoreForTest()
			Expect(store).NotTo(BeNil())
			Expect(store.All()).To(BeEmpty())
		})
	})

	Describe("StreamChunk events feed the swarm activity store", func() {
		It("appends a delegation event when DelegationInfo is present", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.Update(chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID:     "chain-99",
					TargetAgent: "qa-agent",
					Status:      "started",
				},
			})

			events := intent.SwarmStoreForTest().All()
			Expect(events).To(HaveLen(1))
			Expect(events[0].Type).To(Equal(streaming.EventDelegation))
			Expect(events[0].AgentID).To(Equal("qa-agent"))
			Expect(events[0].Status).To(Equal("started"))

			view := intent.View()
			Expect(view).To(ContainSubstring("delegation"))
			Expect(view).To(ContainSubstring("qa-agent"))
		})

		It("appends a tool_call event when ToolCallName is present", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.Update(chat.StreamChunkMsg{
				ToolCallName: "ReadFile",
				ToolStatus:   "started",
			})

			events := intent.SwarmStoreForTest().All()
			Expect(events).To(HaveLen(1))
			Expect(events[0].Type).To(Equal(streaming.EventToolCall))
			Expect(events[0].Status).To(Equal("started"))

			view := intent.View()
			Expect(view).To(ContainSubstring("tool_call"))
		})

		It("appends a plan event when EventType is plan_artifact", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.Update(chat.StreamChunkMsg{
				EventType: streaming.EventTypePlanArtifact,
				Content:   "plan content",
			})

			events := intent.SwarmStoreForTest().All()
			Expect(events).To(HaveLen(1))
			Expect(events[0].Type).To(Equal(streaming.EventPlan))
		})

		It("appends a review event when EventType is review_verdict", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.Update(chat.StreamChunkMsg{
				EventType: streaming.EventTypeReviewVerdict,
				Content:   "PASS",
			})

			events := intent.SwarmStoreForTest().All()
			Expect(events).To(HaveLen(1))
			Expect(events[0].Type).To(Equal(streaming.EventReview))
		})

		It("ignores chunks that carry no activity metadata", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.Update(chat.StreamChunkMsg{Content: "hello"})

			Expect(intent.SwarmStoreForTest().All()).To(BeEmpty())
		})
	})

	Describe("recordSwarmEvent with nil store", func() {
		It("is a no-op when the swarm store has been cleared to nil", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
			intent.SetSwarmStoreForTest(nil)

			// Must not panic on a delegation chunk.
			intent.Update(chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID:     "chain-nil",
					TargetAgent: "x",
					Status:      "started",
				},
			})

			// View must still render without panic; the pane gets nil events.
			Expect(func() { _ = intent.View() }).NotTo(Panic())
		})
	})

	Describe("SwarmEventAppendedMsg handling", func() {
		It("returns nil from Update and does not mutate the store", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			cmd := intent.Update(chat.SwarmEventAppendedMsg{})

			Expect(cmd).To(BeNil())
			Expect(intent.SwarmStoreForTest().All()).To(BeEmpty())
		})
	})

	Describe("swarmEventFromChunk converter", func() {
		It("returns false for a chunk with no activity metadata", func() {
			_, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{Content: "x"}, "agent-1")
			Expect(ok).To(BeFalse())
		})

		It("defaults tool-call status to started when ToolStatus is empty", func() {
			ev, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				ToolCallName: "WriteFile",
			}, "agent-1")
			Expect(ok).To(BeTrue())
			Expect(ev.Status).To(Equal("started"))
			Expect(ev.AgentID).To(Equal("agent-1"))
		})

		It("falls back to the chat agent ID when DelegationInfo target is empty", func() {
			ev, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID: "chain-x",
					Status:  "started",
				},
			}, "agent-1")
			Expect(ok).To(BeTrue())
			Expect(ev.AgentID).To(Equal("agent-1"))
		})
	})
})
