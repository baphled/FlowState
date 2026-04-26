package chat_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// anyTypeVisible reports whether at least one key in types is mapped to
// true. Used as a cheap all-hidden detector in the NeverAllHidden spec.
func anyTypeVisible(types map[streaming.SwarmEventType]bool) bool {
	for _, v := range types {
		if v {
			return true
		}
	}
	return false
}

// Filter-profile tests cover the P11 Ctrl+T cycling contract:
//
//   - the cycle is exactly profileAll -> profileToolsOnly ->
//     profileDelegationsOnly -> profileAll, never landing on an
//     all-hidden state.
//   - each profile asserts a specific visibleTypes shape.
//   - the active profile name surfaces in the activity-pane rendered
//     output for non-default profiles, and is omitted for profileAll.
//   - Ctrl+T is state-mutation-only: it returns a nil tea.Cmd.
var _ = Describe("Chat intent SwarmFilterProfile", func() {
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
		intent.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	})
	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	expectVisibleTypes := func(label string, want map[streaming.SwarmEventType]bool) {
		got := intent.SwarmVisibleTypesForTest()
		for k, v := range want {
			Expect(got[k]).To(Equal(v), "%s[%q]: got %v, want %v", label, k, got[k], v)
		}
	}

	It("cycles through profileAll -> ToolsOnly -> DelegationsOnly -> wrap on Ctrl+T", func() {
		Expect(intent.SwarmFilterProfileForTest()).To(Equal(chat.SwarmFilterProfileAllForTest()))

		intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
		Expect(intent.SwarmFilterProfileForTest()).To(Equal(chat.SwarmFilterProfileToolsOnlyForTest()))

		intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
		Expect(intent.SwarmFilterProfileForTest()).To(Equal(chat.SwarmFilterProfileDelegationsOnlyForTest()))

		intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
		Expect(intent.SwarmFilterProfileForTest()).To(Equal(chat.SwarmFilterProfileAllForTest()),
			"3rd Ctrl+T must wrap back to profileAll")
	})

	It("profileAll sets every type visible", func() {
		expectVisibleTypes("profileAll", map[streaming.SwarmEventType]bool{
			streaming.EventDelegation: true,
			streaming.EventToolCall:   true,
			streaming.EventToolResult: true,
			streaming.EventPlan:       true,
			streaming.EventReview:     true,
		})
	})

	It("profileToolsOnly hides all but EventToolCall and EventToolResult", func() {
		intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT}) // -> profileToolsOnly
		expectVisibleTypes("profileToolsOnly", map[streaming.SwarmEventType]bool{
			streaming.EventDelegation: false,
			streaming.EventToolCall:   true,
			streaming.EventToolResult: true,
			streaming.EventPlan:       false,
			streaming.EventReview:     false,
		})
	})

	It("profileDelegationsOnly hides EventToolCall and EventToolResult, keeps the rest", func() {
		intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT}) // -> profileToolsOnly
		intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT}) // -> profileDelegationsOnly
		expectVisibleTypes("profileDelegationsOnly", map[streaming.SwarmEventType]bool{
			streaming.EventDelegation: true,
			streaming.EventToolCall:   false,
			streaming.EventToolResult: false,
			streaming.EventPlan:       true,
			streaming.EventReview:     true,
		})
	})

	It("never lands on an all-hidden state through the full cycle", func() {
		for press := range 3 {
			Expect(anyTypeVisible(intent.SwarmVisibleTypesForTest())).To(BeTrue(),
				"press %d: all types hidden — cycle must never land on all-hidden", press)
			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
		}
	})

	Describe("filter profile name in rendered footer", func() {
		It("omits the profile name for profileAll (default)", func() {
			intent.SwarmStoreForTest().Append(streaming.SwarmEvent{
				ID: "d1", Type: streaming.EventDelegation, Status: "started", AgentID: "qa",
			})
			view := intent.View()
			Expect(view).NotTo(ContainSubstring("Tool calls only"))
			Expect(view).NotTo(ContainSubstring("Delegations + plan + review"))
		})

		It("renders 'Tool calls only' for profileToolsOnly", func() {
			intent.SwarmStoreForTest().Append(streaming.SwarmEvent{
				ID: "t1", Type: streaming.EventToolCall, Status: "started", AgentID: "tool",
			})
			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			Expect(intent.View()).To(ContainSubstring("Tool calls only"))
		})

		It("renders 'Delegations + plan + review' for profileDelegationsOnly", func() {
			intent.SwarmStoreForTest().Append(streaming.SwarmEvent{
				ID: "d1", Type: streaming.EventDelegation, Status: "started", AgentID: "qa",
			})
			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT}) // -> profileToolsOnly
			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT}) // -> profileDelegationsOnly
			Expect(intent.View()).To(ContainSubstring("Delegations + plan + review"))
		})
	})

	It("treats Ctrl+T as state-mutation-only and returns no tea.Cmd", func() {
		Expect(intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})).To(BeNil())
	})
})
