package engine_test

// Chat-UI leak triage (May 2026, session 2d8dc0ac-8ad6-4271-a479-76c5093e1dfd).
//
// Three concrete leaks were captured in a real user session:
//
//   - Leak A: harness retry-feedback prefix `{"attempt":N,"maxRetries":M}`
//     reached the chat bubble (msg 167, 169, 183, 185). Root cause: the
//     parent-tee path at delegation.go:teeToParentStream and the delegated-
//     response collectors at delegation.go:collectDelegationResult and
//     :collectWithProgress concatenated chunk.Content into the parent
//     stream / aggregated response without checking EventType, so the
//     harness's structured retry payload landed in user-visible text.
//
//   - Leak B: the `<task_result>` LLM-context wrapper that
//     formatDelegationOutput emits leaked into the chat bubble (msg 167,
//     178, 183, 188). The wrapper is purely an LLM-visible boundary
//     marker — chat-UI rendering should never include it. The structural
//     metadata (Role=tool_result, ToolName=delegate) already conveys
//     "this is a sub-agent response" to the UI.
//
//   - Leak C: BackgroundOutputTool returned the raw provider error
//     (e.g. `provider github-copilot error [rate_limit HTTP 429]: POST
//     "https://api.githubcopilot.com/...": 429 Too Many Requests`)
//     directly inside the JSON tool-result payload (msg 231/232/243/244),
//     leaking provider identity and infrastructure URLs into the chat.
//
// These specs pin the engine-seam contracts that prevent the leaks from
// re-emerging. The persistence-side filter is the primary fix; a Vitest
// defensive filter on the Vue MessageBubble is a backstop, covered by
// web/src/components/chat/MessageBubble.spec.ts.

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("Chat-UI leak: harness EventType chunks must not contaminate persisted text (Leak A)", func() {
	// The harness emits Content like `{"attempt":1,"maxRetries":1}` under
	// EventType="harness_attempt_start". Out-of-band consumers (TUI status
	// line, SSE event channel) handle these structurally; in-band
	// concatenators (parent-tee, response collectors) must skip them.

	Describe("streaming.IsControlEvent predicate", func() {
		It("identifies every harness/typed-plan EventType the dispatcher recognises", func() {
			knownControl := []string{
				"harness_attempt_start",
				"harness_retry",
				"harness_complete",
				"harness_critic_feedback",
				"harness_wave_incomplete",
				"plan_artifact",
				"review_verdict",
				"status_transition",
			}
			for _, et := range knownControl {
				Expect(streaming.IsControlEvent(et)).To(BeTrue(),
					"%q is dispatched as an out-of-band control event; it must be in the IsControlEvent set so concatenators skip it", et)
			}
		})

		It("treats empty EventType as in-band content (the common case for assistant text)", func() {
			Expect(streaming.IsControlEvent("")).To(BeFalse())
		})

		It("does not trigger on tool_call or tool_result EventTypes (those carry structural payloads via dedicated channels)", func() {
			Expect(streaming.IsControlEvent("tool_call")).To(BeFalse())
			Expect(streaming.IsControlEvent("tool_result")).To(BeFalse())
		})
	})

	Describe("synchronous delegation collector path", func() {
		// Drive a child engine that emits a harness_attempt_start chunk
		// followed by ordinary assistant text. The harness Content must
		// not appear in the synchronous delegate-tool's Result.Output.
		var (
			harnessProvider *mockProvider
			delegation      agent.Delegation
			engines         map[string]*engine.Engine
		)

		BeforeEach(func() {
			harnessProvider = &mockProvider{
				name: "harness-provider",
				streamChunks: []provider.StreamChunk{
					// Out-of-band harness payload — Content field
					// carries the JSON metadata the leak captured.
					{EventType: "harness_attempt_start", Content: `{"attempt":1,"maxRetries":1}`},
					// Real assistant text the LLM produced.
					{Content: "All requirements clear. I have all three inputs loaded."},
					{Done: true},
				},
			}
			plannerEngine := engine.New(engine.Config{
				ChatProvider: harnessProvider,
				Manifest: agent.Manifest{
					ID:                "plan-writer",
					Name:              "Plan Writer",
					HarnessEnabled:    false, // explicit: drive the collector path, not the synthetic-event path
					Instructions:      agent.Instructions{SystemPrompt: "You write plans."},
					ContextManagement: agent.DefaultContextManagement(),
				},
			})
			engines = map[string]*engine.Engine{"plan-writer": plannerEngine}
			delegation = agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"plan-writer"}}
		})

		It("does not concatenate harness EventType Content into the delegated response wrapped as tool_result", func() {
			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

			result, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "plan-writer",
					"message":       "Write a plan",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Output).NotTo(ContainSubstring(`{"attempt":`),
				"the harness retry payload must not be concatenated into the delegated agent's persisted response — see session 2d8dc0ac msg 167")
			Expect(result.Output).NotTo(ContainSubstring(`"maxRetries":`),
				"any harness JSON shape leaking into the result wrapper is a regression of Leak A")
			Expect(result.Output).To(ContainSubstring("All requirements clear"),
				"genuine assistant content must still flow through")
		})
	})

	Describe("parent-stream tee path", func() {
		// teeToParentStream writes child-agent text into the parent's
		// stream so the lead session shows the sub-agent's voice
		// inline. It must skip out-of-band EventType chunks for the
		// same reason as the collector path.
		var (
			harnessProvider *mockProvider
			delegation      agent.Delegation
			engines         map[string]*engine.Engine
		)

		BeforeEach(func() {
			harnessProvider = &mockProvider{
				name: "harness-provider",
				streamChunks: []provider.StreamChunk{
					{EventType: "harness_attempt_start", Content: `{"attempt":1,"maxRetries":1}`},
					{Content: "Now I have all the context."},
					{Done: true},
				},
			}
			child := engine.New(engine.Config{
				ChatProvider: harnessProvider,
				Manifest: agent.Manifest{
					ID:                "plan-writer",
					Name:              "Plan Writer",
					Instructions:      agent.Instructions{SystemPrompt: "You write plans."},
					ContextManagement: agent.DefaultContextManagement(),
				},
			})
			engines = map[string]*engine.Engine{"plan-writer": child}
			delegation = agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"plan-writer"}}
		})

		It("does not write harness EventType Content into the parent stream's inline `**[agent]**` block", func() {
			parentOut := make(chan provider.StreamChunk, 64)
			ctx := engine.WithStreamOutput(context.Background(), parentOut)

			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")
			_, err := delegateTool.Execute(ctx, tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "plan-writer",
					"message":       "Plan it",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			close(parentOut)
			var teedText strings.Builder
			for chunk := range parentOut {
				if chunk.EventType == "" && chunk.Content != "" {
					teedText.WriteString(chunk.Content)
				}
			}
			Expect(teedText.String()).NotTo(ContainSubstring(`{"attempt":`),
				"teeToParentStream must filter out-of-band EventType Content — see session 2d8dc0ac msg 169/180/185/190")
			Expect(teedText.String()).To(ContainSubstring("Now I have all the context"),
				"genuine sub-agent text must still surface in the parent stream")
		})
	})
})

var _ = Describe("Chat-UI leak: <task_result> wrapper must not appear in the SSE-emitted tool_result chunk (Leak B)", func() {
	// engine.go's tool-loop emits a synthetic StreamChunk with
	// EventType="tool_result" carrying ToolResult.Content. That chunk
	// feeds the SSE bridge AND the session accumulator, so it is the
	// load-bearing site for chat-bubble cleanliness. The wrapper is
	// preserved on tool.Result.Output (used by appendToolResultsBatchToMessages
	// for the next-turn LLM prompt) but stripped on the chunk that
	// reaches the UI / persistence layer.

	Describe("engine.UnwrapTaskResult contract", func() {
		It("is invertible against engine.FormatDelegationOutput on its full output", func() {
			roundtrip := engine.UnwrapTaskResult(engine.FormatDelegationOutput("inner"))
			Expect(roundtrip).To(Equal("inner"))
		})

		It("does NOT strip nested or inline `<task_result>` substrings (only the canonical outer wrapper)", func() {
			// Defensive: if a sub-agent happens to emit `<task_result>`
			// as part of its prose (e.g. discussing this very leak),
			// don't corrupt their text.
			s := "the model said: <task_result> is a marker"
			Expect(engine.UnwrapTaskResult(s)).To(Equal(s))
		})
	})
})
