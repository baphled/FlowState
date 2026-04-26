package chat_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// cmdEventuallyProducesAppendedMsg executes cmd and, if the result is a
// tea.BatchMsg, recurses into each sub-cmd looking for a
// SwarmEventAppendedMsg. Returns true if any path produces it.
func cmdEventuallyProducesAppendedMsg(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	return msgContainsAppended(cmd())
}

func msgContainsAppended(msg tea.Msg) bool {
	switch m := msg.(type) {
	case chat.SwarmEventAppendedMsg:
		return true
	case tea.BatchMsg:
		for _, sub := range m {
			if cmdEventuallyProducesAppendedMsg(sub) {
				return true
			}
		}
	}
	return false
}

// SwarmEvent dispatch tests cover the P3 B7 contract: a stream chunk that
// maps to a swarm event must produce a tea.Cmd that resolves to a
// SwarmEventAppendedMsg, so a background goroutine appending an event
// triggers a re-render within one tick (no keystroke required).
//
//   - chunks that map to an event return a non-nil Cmd whose msg is a
//     SwarmEventAppendedMsg carrying the event ID.
//   - chunks with no activity metadata return a nil Cmd (no spurious
//     dispatch).
//   - the full handleStreamChunkMsg path batches the appended-msg cmd into
//     its returned cmd chain so the Bubble Tea loop dispatches the
//     re-render alongside the next chunk read.
var _ = Describe("Chat intent SwarmEvent dispatch", func() {
	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
	})
	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	newIntent := func() *chat.Intent {
		intent := chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
		intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
		return intent
	}

	It("emits a SwarmEventAppendedMsg carrying the event ID for chunks that map to events", func() {
		intent := newIntent()

		cmd := intent.RecordSwarmEventForTest(chat.StreamChunkMsg{
			ToolCallID:   "toolu_01DISPATCH",
			ToolCallName: "read_file",
			ToolStatus:   "started",
		})

		Expect(cmd).NotTo(BeNil(),
			"recordSwarmEvent must return a non-nil tea.Cmd for chunks that map to swarm events")
		msg := cmd()
		appended, ok := msg.(chat.SwarmEventAppendedMsg)
		Expect(ok).To(BeTrue(),
			"expected tea.Cmd to resolve to SwarmEventAppendedMsg, got %T (%v)", msg, msg)
		Expect(appended.ID).To(Equal("toolu_01DISPATCH"))
	})

	It("returns a nil Cmd when the chunk has no activity metadata", func() {
		intent := chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})

		cmd := intent.RecordSwarmEventForTest(chat.StreamChunkMsg{Content: "plain text"})
		Expect(cmd).To(BeNil())
	})

	It("batches the SwarmEventAppendedMsg into the cmd chain returned by handleStreamChunkMsg", func() {
		intent := newIntent()

		cmd := intent.Update(chat.StreamChunkMsg{
			DelegationInfo: &provider.DelegationInfo{
				ChainID:     "chain-batch",
				TargetAgent: "qa-agent",
				Status:      "started",
			},
		})

		Expect(cmd).NotTo(BeNil(),
			"handleStreamChunkMsg must return a non-nil cmd when an event is appended")
		Expect(cmdEventuallyProducesAppendedMsg(cmd)).To(BeTrue(),
			"expected the returned cmd to produce a SwarmEventAppendedMsg via tea.Batch")

		events := intent.SwarmStoreForTest().All()
		Expect(events).To(HaveLen(1))
		Expect(events[0].Type).To(Equal(streaming.EventDelegation))
	})
})
