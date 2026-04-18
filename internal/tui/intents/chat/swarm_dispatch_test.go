package chat_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// TestRecordSwarmEvent_EmitsAppendedMsg verifies that appending a swarm event
// from a stream chunk produces a tea.Cmd that, when executed, yields a
// SwarmEventAppendedMsg carrying the event's ID. The P3 B7 task requires this
// dispatch so that a background goroutine appending an event causes a
// re-render within one tick instead of waiting for a keystroke.
func TestRecordSwarmEvent_EmitsAppendedMsg(t *testing.T) {
	chat.SetRunningInTestsForTest(true)
	defer chat.SetRunningInTestsForTest(false)

	intent := chat.NewIntent(chat.IntentConfig{
		AgentID:      "test-agent",
		SessionID:    "test-session",
		ProviderName: "openai",
		ModelName:    "gpt-4o",
		TokenBudget:  4096,
	})
	intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	cmd := intent.RecordSwarmEventForTest(chat.StreamChunkMsg{
		ToolCallID:   "toolu_01DISPATCH",
		ToolCallName: "read_file",
		ToolStatus:   "started",
	})

	if cmd == nil {
		t.Fatal("recordSwarmEvent must return a non-nil tea.Cmd for chunks that map to swarm events")
	}
	msg := cmd()
	appended, ok := msg.(chat.SwarmEventAppendedMsg)
	if !ok {
		t.Fatalf("expected tea.Cmd to resolve to SwarmEventAppendedMsg, got %T (%v)", msg, msg)
	}
	if appended.ID != "toolu_01DISPATCH" {
		t.Errorf("expected SwarmEventAppendedMsg.ID to equal the event ID %q, got %q", "toolu_01DISPATCH", appended.ID)
	}
}

// TestRecordSwarmEvent_NoopReturnsNilCmd verifies that a chunk with no
// activity metadata returns a nil Cmd — no SwarmEventAppendedMsg should be
// dispatched when no event was actually appended.
func TestRecordSwarmEvent_NoopReturnsNilCmd(t *testing.T) {
	chat.SetRunningInTestsForTest(true)
	defer chat.SetRunningInTestsForTest(false)

	intent := chat.NewIntent(chat.IntentConfig{
		AgentID:      "test-agent",
		SessionID:    "test-session",
		ProviderName: "openai",
		ModelName:    "gpt-4o",
		TokenBudget:  4096,
	})

	cmd := intent.RecordSwarmEventForTest(chat.StreamChunkMsg{Content: "plain text"})

	if cmd != nil {
		t.Errorf("expected nil Cmd when no event is appended, got %T", cmd())
	}
}

// TestHandleStreamChunkMsg_BatchesAppendedMsg verifies that the full stream
// chunk handler batches the SwarmEventAppendedMsg cmd into its returned cmd
// chain so the Bubble Tea loop dispatches the re-render signal alongside the
// next chunk read.
func TestHandleStreamChunkMsg_BatchesAppendedMsg(t *testing.T) {
	chat.SetRunningInTestsForTest(true)
	defer chat.SetRunningInTestsForTest(false)

	intent := chat.NewIntent(chat.IntentConfig{
		AgentID:      "test-agent",
		SessionID:    "test-session",
		ProviderName: "openai",
		ModelName:    "gpt-4o",
		TokenBudget:  4096,
	})
	intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	// A non-terminal chunk that maps to a SwarmEvent. The returned Cmd must
	// eventually surface a SwarmEventAppendedMsg when executed (directly or
	// as one of the batched commands).
	cmd := intent.Update(chat.StreamChunkMsg{
		DelegationInfo: &provider.DelegationInfo{
			ChainID:     "chain-batch",
			TargetAgent: "qa-agent",
			Status:      "started",
		},
	})

	if cmd == nil {
		t.Fatal("handleStreamChunkMsg must return a non-nil cmd when an event is appended")
	}
	if !cmdEventuallyProducesAppendedMsg(cmd) {
		t.Errorf("expected the returned cmd to produce a SwarmEventAppendedMsg via tea.Batch")
	}
	// Sanity: the event landed.
	events := intent.SwarmStoreForTest().All()
	if len(events) != 1 {
		t.Fatalf("expected 1 event appended, got %d", len(events))
	}
	if events[0].Type != streaming.EventDelegation {
		t.Errorf("expected delegation event, got %s", events[0].Type)
	}
}

// cmdEventuallyProducesAppendedMsg executes cmd and, if the result is a
// batchMsg (tea.BatchMsg), recurses into each sub-cmd looking for a
// SwarmEventAppendedMsg. Returns true if any path produces it.
func cmdEventuallyProducesAppendedMsg(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	return msgContainsAppended(msg)
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
