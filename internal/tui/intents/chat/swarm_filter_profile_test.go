package chat_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// newFilterProfileTestIntent constructs a chat intent wired for the P11
// filter-profile cycling tests. Dimensions are fixed so View() selects the
// dual-pane branch.
func newFilterProfileTestIntent(t *testing.T) *chat.Intent {
	t.Helper()
	chat.SetRunningInTestsForTest(true)
	t.Cleanup(func() { chat.SetRunningInTestsForTest(false) })

	intent := chat.NewIntent(chat.IntentConfig{
		AgentID:      "test-agent",
		SessionID:    "test-session",
		ProviderName: "openai",
		ModelName:    "gpt-4o",
		TokenBudget:  4096,
	})
	intent.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	return intent
}

// TestChatIntent_CtrlT_CyclesFilterProfile verifies that each Ctrl+T press
// advances the chat intent through the fixed three-profile cycle:
//
//	profileAll -> profileToolsOnly -> profileDelegationsOnly -> profileAll
//
// P11 design decision: the cycle never lands on an all-hidden state so the
// existing P8 recovery hint is unreachable via the keyboard.
func TestChatIntent_CtrlT_CyclesFilterProfile(t *testing.T) {
	intent := newFilterProfileTestIntent(t)

	if got, want := intent.SwarmFilterProfileForTest(), chat.SwarmFilterProfileAllForTest(); got != want {
		t.Fatalf("initial profile: got %q, want %q", got, want)
	}

	intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if got, want := intent.SwarmFilterProfileForTest(), chat.SwarmFilterProfileToolsOnlyForTest(); got != want {
		t.Fatalf("after 1x Ctrl+T: got %q, want %q", got, want)
	}

	intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if got, want := intent.SwarmFilterProfileForTest(), chat.SwarmFilterProfileDelegationsOnlyForTest(); got != want {
		t.Fatalf("after 2x Ctrl+T: got %q, want %q", got, want)
	}

	intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if got, want := intent.SwarmFilterProfileForTest(), chat.SwarmFilterProfileAllForTest(); got != want {
		t.Fatalf("after 3x Ctrl+T (wrap): got %q, want %q", got, want)
	}
}

// assertVisibleTypes fails t if the supplied visibleTypes map does not
// match want on every key in want. Used by the P11 profile assertions to
// keep each test's cognitive complexity below the revive budget.
func assertVisibleTypes(
	t *testing.T,
	label string,
	got map[streaming.SwarmEventType]bool,
	want map[streaming.SwarmEventType]bool,
) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s[%q]: got %v, want %v", label, k, got[k], v)
		}
	}
}

// TestChatIntent_FilterProfile_All asserts profileAll renders every
// SwarmEventType visible.
func TestChatIntent_FilterProfile_All(t *testing.T) {
	intent := newFilterProfileTestIntent(t)
	assertVisibleTypes(t, "profileAll", intent.SwarmVisibleTypesForTest(),
		map[streaming.SwarmEventType]bool{
			streaming.EventDelegation: true,
			streaming.EventToolCall:   true,
			streaming.EventToolResult: true,
			streaming.EventPlan:       true,
			streaming.EventReview:     true,
		})
}

// TestChatIntent_FilterProfile_ToolsOnly asserts profileToolsOnly hides
// every type except EventToolCall and EventToolResult.
func TestChatIntent_FilterProfile_ToolsOnly(t *testing.T) {
	intent := newFilterProfileTestIntent(t)
	intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT}) // -> profileToolsOnly
	assertVisibleTypes(t, "profileToolsOnly", intent.SwarmVisibleTypesForTest(),
		map[streaming.SwarmEventType]bool{
			streaming.EventDelegation: false,
			streaming.EventToolCall:   true,
			streaming.EventToolResult: true,
			streaming.EventPlan:       false,
			streaming.EventReview:     false,
		})
}

// TestChatIntent_FilterProfile_DelegationsOnly asserts profileDelegationsOnly
// hides EventToolCall and EventToolResult but leaves delegation, plan, and
// review visible.
func TestChatIntent_FilterProfile_DelegationsOnly(t *testing.T) {
	intent := newFilterProfileTestIntent(t)
	intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT}) // -> profileToolsOnly
	intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT}) // -> profileDelegationsOnly
	assertVisibleTypes(t, "profileDelegationsOnly", intent.SwarmVisibleTypesForTest(),
		map[streaming.SwarmEventType]bool{
			streaming.EventDelegation: true,
			streaming.EventToolCall:   false,
			streaming.EventToolResult: false,
			streaming.EventPlan:       true,
			streaming.EventReview:     true,
		})
}

// TestChatIntent_FilterProfile_NeverAllHidden asserts the invariant that no
// profile in the P11 cycle leaves every type hidden. Walk the full cycle
// and fail as soon as we observe an all-hidden state.
func TestChatIntent_FilterProfile_NeverAllHidden(t *testing.T) {
	intent := newFilterProfileTestIntent(t)
	for press := range 3 {
		if !anyTypeVisible(intent.SwarmVisibleTypesForTest()) {
			t.Fatalf("press %d: all types hidden — cycle must never land on all-hidden", press)
		}
		intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	}
}

// anyTypeVisible reports whether at least one key in types is mapped to
// true. Used as a cheap all-hidden detector in the NeverAllHidden test.
func anyTypeVisible(types map[streaming.SwarmEventType]bool) bool {
	for _, v := range types {
		if v {
			return true
		}
	}
	return false
}

// TestChatIntent_FilterProfileName_RendersInFooter verifies that the chat
// intent surfaces the active profile name in the activity pane's rendered
// output when the profile is non-default, and omits it when profile is
// profileAll (the default state — no need to distract the user).
func TestChatIntent_FilterProfileName_RendersInFooter(t *testing.T) {
	t.Run("profileAll omits profile name", func(t *testing.T) {
		intent := newFilterProfileTestIntent(t)
		// Seed at least one event so the pane renders content rather than
		// its placeholder strings.
		intent.SwarmStoreForTest().Append(streaming.SwarmEvent{
			ID: "d1", Type: streaming.EventDelegation, Status: "started", AgentID: "qa",
		})
		view := intent.View()
		if strings.Contains(view, "Tool calls only") ||
			strings.Contains(view, "Delegations + plan + review") {
			t.Fatalf("profileAll should not render a profile name tag, view:\n%s", view)
		}
	})

	t.Run("profileToolsOnly renders 'Tool calls only'", func(t *testing.T) {
		intent := newFilterProfileTestIntent(t)
		intent.SwarmStoreForTest().Append(streaming.SwarmEvent{
			ID: "t1", Type: streaming.EventToolCall, Status: "started", AgentID: "tool",
		})
		intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
		view := intent.View()
		if !strings.Contains(view, "Tool calls only") {
			t.Fatalf("expected profile name 'Tool calls only' in view, got:\n%s", view)
		}
	})

	t.Run("profileDelegationsOnly renders 'Delegations + plan + review'", func(t *testing.T) {
		intent := newFilterProfileTestIntent(t)
		intent.SwarmStoreForTest().Append(streaming.SwarmEvent{
			ID: "d1", Type: streaming.EventDelegation, Status: "started", AgentID: "qa",
		})
		intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT}) // -> profileToolsOnly
		intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT}) // -> profileDelegationsOnly
		view := intent.View()
		if !strings.Contains(view, "Delegations + plan + review") {
			t.Fatalf("expected profile name 'Delegations + plan + review' in view, got:\n%s", view)
		}
	})
}

// TestChatIntent_CtrlT_ReturnsNoCommand verifies handleFilterToggle is a
// state-mutation-only handler with no tea.Cmd side effects.
func TestChatIntent_CtrlT_ReturnsNoCommand(t *testing.T) {
	intent := newFilterProfileTestIntent(t)
	cmd := intent.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if cmd != nil {
		t.Errorf("Ctrl+T must be state-mutation-only, got non-nil cmd: %v", cmd)
	}
}
