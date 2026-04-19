package chat_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// TestChatIntent_ReassertsVisibleTypes_OnRender verifies that the chat intent
// holds an authoritative visibleTypes map and applies it on every render. The
// P3 A3 task requires the intent to be the source of truth so transient
// filter churn on the pane cannot silently hide non-tool_call event types.
func TestChatIntent_ReassertsVisibleTypes_OnRender(t *testing.T) {
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

	// Seed: all four non-tool-result types + a tool_call so coalesce has
	// something to pair.
	store := intent.SwarmStoreForTest()
	store.Append(streaming.SwarmEvent{ID: "d1", Type: streaming.EventDelegation, Status: "s", AgentID: "qa"})
	store.Append(streaming.SwarmEvent{ID: "t1", Type: streaming.EventToolCall, Status: "started", AgentID: "t"})
	store.Append(streaming.SwarmEvent{ID: "p1", Type: streaming.EventPlan, Status: "c", AgentID: "plan"})
	store.Append(streaming.SwarmEvent{ID: "r1", Type: streaming.EventReview, Status: "c", AgentID: "rev"})

	view := intent.View()

	// Activity rows render human labels, never the wire identifiers.
	// See swarm_activity_human_labels_test.go for the canonical contract.
	if !strings.Contains(view, "Delegation") {
		t.Errorf("delegation events must render by default (intent asserts visibility), got:\n%s", view)
	}
	if !strings.Contains(view, "Tool Call") {
		t.Errorf("tool_call events must render by default, got:\n%s", view)
	}
	if !strings.Contains(view, "Plan") {
		t.Errorf("plan events must render by default, got:\n%s", view)
	}
	if !strings.Contains(view, "Review") {
		t.Errorf("review events must render by default, got:\n%s", view)
	}
}

// TestChatIntent_VisibleTypesMapExists verifies that the intent exposes its
// visibleTypes map for future filter keybindings (Ctrl+T in P8).
func TestChatIntent_VisibleTypesMapExists(t *testing.T) {
	chat.SetRunningInTestsForTest(true)
	defer chat.SetRunningInTestsForTest(false)

	intent := chat.NewIntent(chat.IntentConfig{
		AgentID:      "test-agent",
		SessionID:    "test-session",
		ProviderName: "openai",
		ModelName:    "gpt-4o",
		TokenBudget:  4096,
	})

	types := intent.SwarmVisibleTypesForTest()
	if types == nil {
		t.Fatal("intent must expose a non-nil visibleTypes map")
	}

	for _, want := range []streaming.SwarmEventType{
		streaming.EventDelegation,
		streaming.EventToolCall,
		streaming.EventToolResult,
		streaming.EventPlan,
		streaming.EventReview,
	} {
		if !types[want] {
			t.Errorf("expected default visibleTypes[%s] = true", want)
		}
	}
}
