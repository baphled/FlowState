package engine_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// Item 3 of the Agent Runtime Quality plan (May 2026): when an agent
// hallucinates a tool call whose name is actually a SKILL (e.g.
// `task-tracker`), the engine's tool-not-found path returns a
// structured recovery hint instead of the generic
// "tool not found. Available tools: [...]" message. The recovery body
// points at the canonical invocation — skill_load(name="X") — so the
// model has an explicit next-call to make.
//
// Order matters per D4: this redirect must NOT replace the existing
// fuzzy "Did you mean" path for unknown tool names that do not match
// any known skill. R4 mitigation pins the exact-match contract — the
// redirect only fires when toolCall.Name is in the autoloader's
// catalogue verbatim.
//
// Safety net: the redirect is informational, not an instruction the
// engine acts on. Even when `skill_load` itself is somehow unavailable
// in the agent's effective toolset (degraded manifest, mid-rollout
// state), the redirect text is still emitted — the engine never
// auto-invokes anything from the recovery body, so there is no
// infinite-loop risk.
var _ = Describe("Engine.executeToolCall skill-name redirect", func() {
	makeEngine := func(knownSkills []string) *engine.Engine {
		providerReg := provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})
		cfg := engine.Config{
			Manifest:      agent.Manifest{ID: "lead", Name: "Lead"},
			AgentRegistry: agent.NewRegistry(),
			Registry:      providerReg,
			ChatProvider:  &mockProvider{name: "spy"},
		}
		if knownSkills != nil {
			cfg.KnownSkillsFunc = func() []string { return knownSkills }
		}
		eng := engine.New(cfg)
		// Register a real tool so the engine has something to fall through
		// past in the "no match" cases. The redirect path is exercised by
		// asking for a name that is NOT registered as a tool.
		eng.AddTool(&gateHaltFakeTool{name: "bash", err: nil})
		return eng
	}

	runTool := func(eng *engine.Engine, name string) (tool.Result, error) {
		return eng.ExecuteToolCallForTest(context.Background(), "sess-skill-redirect", &provider.ToolCall{
			ID:        "call-" + name,
			Name:      name,
			Arguments: map[string]any{},
		})
	}

	When("the unknown tool name exact-matches a known skill name", func() {
		It("returns a structured tool_result pointing the model at skill_load(name=\"X\")", func() {
			eng := makeEngine([]string{"task-tracker", "memory-keeper", "pre-action"})

			result, err := runTool(eng, "task-tracker")

			// The redirect is an IsError tool_result, not a Go error —
			// the agent's tool loop sees it and the model is expected to
			// emit a skill_load call next.
			Expect(err).NotTo(HaveOccurred(),
				"redirect surfaces as an IsError result, never an outer error — outer errors abort the stream")
			Expect(result.IsError).To(BeTrue(),
				"the call did fail; we provide recovery guidance, not a pretence of success")
			Expect(result.Output).To(Equal(
				`'task-tracker' is a skill, not a tool. Invoke it with skill_load(name="task-tracker").`,
			),
				"the body is the verbatim plan-specified redirect message — Item 3 of Agent Runtime Quality (May 2026)")
			Expect(result.Error).To(MatchError(tool.ErrToolNotFound),
				"the wrapped sentinel error stays in place so errors.Is(tool.ErrToolNotFound) callers continue to recognise the failure shape")
		})

		It("does not include the generic 'Available tools:' inventory in the redirect body", func() {
			// R4 mitigation: when the redirect fires we suppress the
			// generic catalogue listing. The model already knows the
			// skill exists (Item 2's <available_skills> block listed it);
			// duplicating the tool inventory just dilutes the recovery
			// signal.
			eng := makeEngine([]string{"task-tracker"})

			result, _ := runTool(eng, "task-tracker")

			Expect(result.Output).NotTo(ContainSubstring("Available tools:"),
				"redirect body is the canonical recovery action, not a verbose tool inventory")
			Expect(result.Output).NotTo(ContainSubstring("Did you mean"),
				"the fuzzy-suggest path is the fallback for non-skill typos, not the redirect")
		})
	})

	When("the unknown tool name does not match any known skill", func() {
		It("falls through to the existing 'tool not found. Available tools: [...]' message", func() {
			// R4 negative case: a real typo (`bashh` for `bash`) must
			// stay on the existing fuzzy-suggest path. Over-redirecting
			// would produce confusing messages like
			// "'bashh' is a skill" for names that are neither tools nor
			// skills.
			eng := makeEngine([]string{"task-tracker", "memory-keeper"})

			result, err := runTool(eng, "bashh")

			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsError).To(BeTrue())
			Expect(result.Output).To(ContainSubstring("tool 'bashh' not found"),
				"unknown-non-skill names keep the historical error shape")
			Expect(result.Output).To(ContainSubstring("Available tools:"),
				"the tool inventory remains in the fallback path so the model can correct typos against the real catalogue")
			Expect(result.Output).To(ContainSubstring("Did you mean 'bash'?"),
				"the Levenshtein suggestion stays attached on the fallback path — Item 3 augments the tool-not-found path, it does not replace the fuzzy match")
			Expect(result.Output).NotTo(ContainSubstring("is a skill, not a tool"),
				"the redirect must not over-fire on names that are not actually known skills")
			Expect(errors.Is(result.Error, tool.ErrToolNotFound)).To(BeTrue())
		})
	})

	When("no KnownSkillsFunc is configured on the engine", func() {
		It("preserves the pre-Item-3 fallback path with no behaviour change", func() {
			// The redirect is opt-in by Config. An engine constructed
			// without the func continues to emit the historical message
			// — keeps the wider matrix (tests, legacy callers) safe
			// under the rollout.
			eng := makeEngine(nil)

			result, err := runTool(eng, "task-tracker")

			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsError).To(BeTrue())
			Expect(result.Output).To(ContainSubstring("tool 'task-tracker' not found"))
			Expect(result.Output).NotTo(ContainSubstring("is a skill, not a tool"))
		})
	})

	When("the agent's effective toolset omits skill_load entirely", func() {
		It("still emits the redirect — the body is informational, never auto-invoked by the engine", func() {
			// Safety-net spec from the brief: the redirect body
			// references skill_load(name="X") but the engine does NOT
			// call skill_load itself. The text is consumed by the model
			// on the next turn; if skill_load is unavailable the model
			// will see the redirect, attempt the skill_load call, and
			// hit the standard permission/availability path. There is
			// no executor-side recursion that could infinite-loop on a
			// degraded agent.
			eng := makeEngine([]string{"task-tracker"})
			// Note: this engine has only `bash` registered as a tool
			// (see makeEngine). skill_load is intentionally absent —
			// the redirect must still fire because the catalogue is the
			// authority for "is X a skill", not the tool registry.

			result, err := runTool(eng, "task-tracker")

			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsError).To(BeTrue())
			Expect(result.Output).To(ContainSubstring(`skill_load(name="task-tracker")`),
				"the recovery hint is emitted regardless of skill_load availability — the engine never auto-routes, so there is no loop risk")
		})
	})
})
