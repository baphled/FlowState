package chat_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// Covers P7/C2 — when an agent whose manifest sets can_delegate:false emits
// a bare tool_use as the first chunk of its reply (no preceding text), and
// the user's prompt referenced another agent via an @<name> mention, the
// chat intent must surface a visible warning so the user understands why the
// reply appears to go off-topic. The detection is conservative: it fires at
// most once per turn and only when all three signals align.
var _ = Describe("premature delegation misfire warning (P7/C2)", func() {
	var (
		intent *chat.Intent
		reg    *agent.Registry
	)

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "executor",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
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

	It("warns when a non-delegating agent emits a bare tool_use as the first chunk after an @-mention", func() {
		chat.SetTurnUserMessageForTest(intent, "delegate to @team-lead and run the plan")

		cmd := intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{
			ToolCallName: "query_vault",
			ToolStatus:   "started",
		})
		// The returned cmd may be nil; what matters is the notification.
		_ = cmd

		active := intent.NotificationManagerForTest().Active()
		Expect(active).NotTo(BeEmpty(),
			"a premature delegation misfire must surface a notification")

		var joined strings.Builder
		for _, n := range active {
			joined.WriteString(strings.ToLower(n.Title))
			joined.WriteString(" | ")
			joined.WriteString(strings.ToLower(n.Message))
			joined.WriteString(" || ")
		}
		combined := joined.String()
		Expect(combined).To(SatisfyAny(
			ContainSubstring("delegate"),
			ContainSubstring("cannot"),
			ContainSubstring("off-target"),
			ContainSubstring("off target"),
		), "warning wording must reference delegation/off-target, got: %q", combined)
	})

	It("does not warn when the current agent can delegate", func() {
		// Team-lead can delegate: even if the first chunk is a tool_use,
		// this is not the misfire pattern.
		intent.SetAgentIDForTest("team-lead")
		chat.SetTurnUserMessageForTest(intent, "delegate to @team-lead and run it")

		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{
			ToolCallName: "query_vault",
			ToolStatus:   "started",
		})

		Expect(intent.NotificationManagerForTest().Active()).To(BeEmpty(),
			"a delegating agent starting a tool_use is normal; no warning expected")
	})

	It("does not warn when the first chunk carries text content", func() {
		chat.SetTurnUserMessageForTest(intent, "delegate to @team-lead please")

		// Text first — this is not the bare-tool_use ordering bug.
		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{
			Content: "Sure, I will do that.",
		})
		// Now a tool_use arrives, but turnHasText should already be true.
		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{
			ToolCallName: "query_vault",
			ToolStatus:   "started",
		})

		Expect(intent.NotificationManagerForTest().Active()).To(BeEmpty(),
			"a tool_use preceded by text is not the misfire pattern")
	})

	It("does not warn when the user message contains no @-mention", func() {
		chat.SetTurnUserMessageForTest(intent, "please run the tests and summarise")

		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{
			ToolCallName: "query_vault",
			ToolStatus:   "started",
		})

		Expect(intent.NotificationManagerForTest().Active()).To(BeEmpty(),
			"without an @-mention there is no evidence the user wanted delegation")
	})

	It("does not warn when the @-mention references an unknown agent", func() {
		// @bogus is not registered — we cannot honestly claim the user
		// asked for delegation.
		chat.SetTurnUserMessageForTest(intent, "delegate to @bogus please")

		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{
			ToolCallName: "query_vault",
			ToolStatus:   "started",
		})

		Expect(intent.NotificationManagerForTest().Active()).To(BeEmpty(),
			"an @-mention that does not match any known agent is not actionable")
	})

	It("fires the warning at most once per user turn", func() {
		chat.SetTurnUserMessageForTest(intent, "delegate to @team-lead now")

		// Two back-to-back tool_use chunks from the same turn must not
		// produce two notifications.
		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{
			ToolCallName: "query_vault",
			ToolStatus:   "started",
		})
		intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{
			ToolCallName: "query_vault",
			ToolStatus:   "started",
		})

		active := intent.NotificationManagerForTest().Active()
		Expect(active).To(HaveLen(1),
			"duplicate warnings on a single turn spam the user")
	})
})
