package chat_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// Visible-types tests cover the P3 A3 contract: the chat intent owns an
// authoritative visibleTypes map and reasserts it on every render so
// transient filter churn on the activity pane cannot silently hide
// non-tool_call event types.
var _ = Describe("Chat intent visibleTypes", func() {
	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
	})
	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	It("renders every default-visible event type on View()", func() {
		intent := chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
		intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

		// Seed: all four non-tool-result types + a tool_call so coalesce
		// has something to pair.
		store := intent.SwarmStoreForTest()
		store.Append(streaming.SwarmEvent{ID: "d1", Type: streaming.EventDelegation, Status: "s", AgentID: "qa"})
		store.Append(streaming.SwarmEvent{ID: "t1", Type: streaming.EventToolCall, Status: "started", AgentID: "t"})
		store.Append(streaming.SwarmEvent{ID: "p1", Type: streaming.EventPlan, Status: "c", AgentID: "plan"})
		store.Append(streaming.SwarmEvent{ID: "r1", Type: streaming.EventReview, Status: "c", AgentID: "rev"})

		view := intent.View()

		// Activity rows render human labels, never the wire identifiers.
		// See swarm_activity_human_labels_test.go for the canonical contract.
		Expect(view).To(ContainSubstring("Delegation"))
		Expect(view).To(ContainSubstring("Tool Call"))
		Expect(view).To(ContainSubstring("Plan"))
		Expect(view).To(ContainSubstring("Review"))
	})

	It("exposes a non-nil visibleTypes map with every default type set to true", func() {
		intent := chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})

		types := intent.SwarmVisibleTypesForTest()
		Expect(types).NotTo(BeNil())

		for _, want := range []streaming.SwarmEventType{
			streaming.EventDelegation,
			streaming.EventToolCall,
			streaming.EventToolResult,
			streaming.EventPlan,
			streaming.EventReview,
		} {
			Expect(types[want]).To(BeTrue(),
				"expected default visibleTypes[%s] = true", want)
		}
	})
})
