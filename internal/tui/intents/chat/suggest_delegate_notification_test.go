package chat_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// Phase 12 — when the engine executes the suggest_delegate tool on behalf of a
// non-delegating agent, the resulting tool_result chunk carries a structured
// JSON payload describing a recommended agent switch. The chat intent must
// recognise this payload and surface a one-line actionable notification so the
// user can act on it (the UI layer will later wire this into an agent-picker
// prompt). This gives the model a legitimate escape hatch and pairs with the
// P7 warning path for models that ignore the tool.
var _ = Describe("suggest_delegate tool_result surfaces a switch-agent notification (P12)", func() {
	var (
		intent *chat.Intent
		reg    *agent.Registry
	)

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "executor",
			SessionID:    "test-session-p12",
			ProviderName: "anthropic",
			ModelName:    "claude-opus-4-7",
			TokenBudget:  4096,
		})

		reg = agent.NewRegistry()
		reg.Register(&agent.Manifest{
			ID:   "executor",
			Name: "Executor",
			Delegation: agent.Delegation{
				CanDelegate: false,
			},
		})
		reg.Register(&agent.Manifest{
			ID:   "router",
			Name: "Router",
			Delegation: agent.Delegation{
				CanDelegate: true,
			},
		})
		reg.Register(&agent.Manifest{
			ID:   "team-lead",
			Name: "Team Lead",
			Delegation: agent.Delegation{
				CanDelegate: true,
			},
		})
		intent.SetAgentRegistryForTest(reg)
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	It("renders a switch-agent notification when suggest_delegate returns a structured payload", func() {
		payload := `{"suggestion":"switch_agent","from_agent":"executor","to_agent":"router","target_agent":"team-lead","reason":"needs planning","user_prompt":"Switch to router to delegate to @team-lead?"}`

		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{
			ToolCallName: "suggest_delegate",
			ToolStatus:   "completed",
			ToolCallID:   "tc-p12-1",
			ToolResult:   payload,
		})

		active := intent.NotificationManagerForTest().Active()
		Expect(active).NotTo(BeEmpty(),
			"suggest_delegate tool_result must surface a notification")

		var joined strings.Builder
		for _, n := range active {
			joined.WriteString(strings.ToLower(n.Title))
			joined.WriteString(" | ")
			joined.WriteString(strings.ToLower(n.Message))
			joined.WriteString(" || ")
		}
		combined := joined.String()
		Expect(combined).To(SatisfyAll(
			ContainSubstring("switch"),
			ContainSubstring("router"),
			ContainSubstring("team-lead"),
		), "notification must guide the user to switch agents; got: %q", combined)
	})

	It("does not render a notification when a different tool's tool_result arrives", func() {
		// Sanity: unrelated tool_results must not trigger the suggestion UI.
		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{
			ToolCallName: "query_vault",
			ToolStatus:   "completed",
			ToolCallID:   "tc-other-1",
			ToolResult:   `{"foo":"bar"}`,
		})
		Expect(intent.NotificationManagerForTest().Active()).To(BeEmpty())
	})

	It("ignores malformed suggest_delegate payloads without crashing", func() {
		// Malformed JSON must not surface the actionable notification — the
		// chat intent should fail open silently rather than spam the user.
		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{
			ToolCallName: "suggest_delegate",
			ToolStatus:   "completed",
			ToolCallID:   "tc-p12-bad",
			ToolResult:   `not-json at all`,
		})
		Expect(intent.NotificationManagerForTest().Active()).To(BeEmpty())
	})

	It("ignores suggest_delegate payloads missing the switch_agent suggestion marker", func() {
		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{
			ToolCallName: "suggest_delegate",
			ToolStatus:   "completed",
			ToolCallID:   "tc-p12-no-marker",
			ToolResult:   `{"other":"data"}`,
		})
		Expect(intent.NotificationManagerForTest().Active()).To(BeEmpty())
	})
})
