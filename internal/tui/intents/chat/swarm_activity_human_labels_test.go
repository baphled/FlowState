package chat_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// RED specs confirming the "human label" contract from the ADR
// "Swarm Activity Event Model" and T5/T21 of the Multi-Agent Chat UX Plan.
//
// Rendered rows MUST display human-readable labels:
//
//	delegation   -> "Delegation"
//	tool_call    -> "Tool Call"
//	plan         -> "Plan"
//	review       -> "Review"
//
// And the wire identifiers "tool_call" and "tool_result" MUST NOT leak into
// the rendered output. These specs exercise the Intent.View seam directly —
// the same seam the activity pane is rendered through at runtime — rather
// than the private formatter.
var _ = Describe("Intent.View swarm activity row labels", func() {
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
		// Width 100 guarantees dual-pane is active and the secondary
		// pane (Activity Timeline) is rendered.
		intent.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	// pushEvent dispatches a single StreamChunkMsg that will cause the
	// chat intent to append a SwarmEvent of the requested type via the
	// same path used by the streaming pipeline in production. The
	// neighbouring `swarm_activity_wiring_test.go` uses the same seam.
	pushEvent := func(t streaming.SwarmEventType) {
		switch t {
		case streaming.EventDelegation:
			intent.Update(chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID:     "chain-lbl",
					TargetAgent: "qa-agent",
					Status:      "started",
				},
			})
		case streaming.EventToolCall:
			intent.Update(chat.StreamChunkMsg{
				ToolCallID:   "toolu_lbl",
				ToolCallName: "bash",
				ToolStatus:   "started",
			})
		case streaming.EventPlan:
			intent.Update(chat.StreamChunkMsg{
				EventType: streaming.EventTypePlanArtifact,
				Content:   "plan body",
			})
		case streaming.EventReview:
			intent.Update(chat.StreamChunkMsg{
				EventType: streaming.EventTypeReviewVerdict,
				Content:   "PASS",
			})
		default:
			Fail("pushEvent: unsupported event type " + string(t))
		}
	}

	// The activity pane renders one row per event as
	//   ▸ {Label} · {AgentID} · {Status}
	// The leading "▸ " glyph is a stable anchor that only appears on
	// activity-pane rows — not notifications, delegation picker, or any
	// other widget that might also use the plain word "Delegation".
	// Anchoring on "▸ <Label>" prevents false positives where the bare
	// word is matched elsewhere in the composed view.
	const rowPrefix = "▸ "

	Describe("renders human labels (Delegation, Tool Call, Plan, Review)", func() {
		It("renders 'Delegation' on the activity row for a delegation event", func() {
			pushEvent(streaming.EventDelegation)
			Expect(intent.View()).To(ContainSubstring(rowPrefix+"Delegation"),
				"delegation rows must display the human label 'Delegation', not the wire identifier 'delegation'")
		})

		It("renders 'Tool Call' on the activity row for a tool_call event", func() {
			pushEvent(streaming.EventToolCall)
			Expect(intent.View()).To(ContainSubstring(rowPrefix+"Tool Call"),
				"tool_call rows must display the human label 'Tool Call', not the wire identifier 'tool_call'")
		})

		It("renders 'Plan' on the activity row for a plan event", func() {
			pushEvent(streaming.EventPlan)
			Expect(intent.View()).To(ContainSubstring(rowPrefix+"Plan"),
				"plan rows must display the human label 'Plan', not the wire identifier 'plan'")
		})

		It("renders 'Review' on the activity row for a review event", func() {
			pushEvent(streaming.EventReview)
			Expect(intent.View()).To(ContainSubstring(rowPrefix+"Review"),
				"review rows must display the human label 'Review', not the wire identifier 'review'")
		})
	})

	Describe("never leaks the wire identifier tool_call or tool_result", func() {
		It("does not render 'tool_call' for a tool_call event", func() {
			pushEvent(streaming.EventToolCall)
			Expect(intent.View()).NotTo(ContainSubstring("tool_call"),
				"rendered output must not leak the wire identifier 'tool_call'")
		})

		It("does not render 'tool_result' for a tool_call event", func() {
			pushEvent(streaming.EventToolCall)
			Expect(intent.View()).NotTo(ContainSubstring("tool_result"),
				"rendered output must not leak the wire identifier 'tool_result'")
		})

		It("does not render 'tool_call' for a delegation event", func() {
			pushEvent(streaming.EventDelegation)
			Expect(intent.View()).NotTo(ContainSubstring("tool_call"),
				"rendered output must not leak the wire identifier 'tool_call'")
		})

		It("does not render 'tool_result' for a delegation event", func() {
			pushEvent(streaming.EventDelegation)
			Expect(intent.View()).NotTo(ContainSubstring("tool_result"),
				"rendered output must not leak the wire identifier 'tool_result'")
		})
	})
})
