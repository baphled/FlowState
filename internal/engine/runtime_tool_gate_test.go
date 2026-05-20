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

// PR7 / Coordinator Over-Execution (May 2026) — runtime tool gate.
//
// The Bug Fixes/Coordinator Over-Execution (May 2026) investigation
// note (Layer 1) demonstrates that executeToolCall's pre-PR7 loop
// would run any tool whose name matched a registered tool in
// e.tools regardless of whether the agent's manifest declared it.
// buildAllowedToolSetFor only filters the schemas advertised to the
// LLM — but permissive providers (glm-4.x at zai/openzen) emit
// out-of-schema tool calls anyway. Session
// fbdea3e6-6e00-4c96-89ec-0ab953799cee shows the coordinator
// (manifest tools = [coordination_store, skill_load, delegate,
// todowrite]) executing 21 bash + 5 read calls direct.
//
// PR7 closes the gap at the dispatch boundary: an executeToolCall
// for a tool name that is registered on the engine but NOT in the
// active manifest's effective toolset returns a structured
// rejection result (IsError=true, Error wraps
// tool.ErrToolNotAllowed) before t.Execute is invoked. The
// behaviour matches the structured-rejection shape PR5/PR6
// established for todo_strict_mode and skill-name redirect, so
// providers' tool-result chunk handling continues to work
// unchanged.
//
// Ordering contract:
//
//   - new gate fires BEFORE t.Execute when the tool name IS
//     registered in e.tools — keeps the dispatch from running an
//     out-of-manifest call even when the model emitted one.
//   - new gate does NOT fire when the tool name is NOT registered
//     in e.tools — that case keeps falling through to PR2's
//     skill-name redirect (then to the generic tool-not-found
//     path). The redirect deliberately fires for skill names
//     called as tools regardless of the agent's effective toolset
//     because the redirect body is informational, never invoked
//     by the engine.
//   - the collision case (skill name and registered tool name
//     coincide AND the tool IS in effective set): tool wins —
//     normal execution. If tool is NOT in effective set, new gate
//     fires with the "X is not in this agent's allowed toolset"
//     body, not the skill redirect.
var _ = Describe("Engine.executeToolCall runtime tool gate (PR7)", func() {
	makeEngine := func(manifest agent.Manifest, knownSkills []string) *engine.Engine {
		providerReg := provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})
		cfg := engine.Config{
			Manifest:      manifest,
			AgentRegistry: agent.NewRegistry(),
			Registry:      providerReg,
			ChatProvider:  &mockProvider{name: "spy"},
		}
		if knownSkills != nil {
			cfg.KnownSkillsFunc = func() []string { return knownSkills }
		}
		return engine.New(cfg)
	}

	runTool := func(eng *engine.Engine, name string) (tool.Result, error) {
		return eng.ExecuteToolCallForTest(context.Background(), "sess-runtime-gate", &provider.ToolCall{
			ID:        "call-" + name,
			Name:      name,
			Arguments: map[string]any{},
		})
	}

	When("the tool name IS in the agent's effective toolset (A.1.1)", func() {
		It("executes normally — regression pin for the allowed path", func() {
			// Manifest declares `bash`; EffectiveTools() returns
			// union(declared, base) → [bash, todowrite, todo_update,
			// skill_load].
			manifest := agent.Manifest{
				ID:   "lead",
				Name: "Lead",
				Capabilities: agent.Capabilities{
					Tools: []string{"bash"},
				},
			}
			eng := makeEngine(manifest, nil)
			eng.AddTool(&gateHaltFakeTool{name: "bash", err: nil})

			result, err := runTool(eng, "bash")

			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsError).To(BeFalse(),
				"a tool the manifest declared must execute normally — the gate fires only on out-of-manifest calls")
			Expect(result.Output).To(Equal("fake output"),
				"the gate must not interpose on the allowed path; the fake tool's output reaches the caller untouched")
			Expect(result.Error).NotTo(HaveOccurred())
		})
	})

	When("the tool name is REGISTERED but NOT in the agent's effective toolset (A.1.2 — the load-bearing spec)", func() {
		It("returns a structured tool_result rejection without invoking Execute", func() {
			// Manifest declares NOTHING — under D1, EffectiveTools()
			// returns the default base set (todowrite, todo_update,
			// skill_load). `bash` is NOT in that set, even though
			// `bash` is registered on the engine. This is the
			// Coordinator Over-Execution (May 2026) Layer 1 bug:
			// pre-PR7 dispatch fell through to t.Execute regardless.
			manifest := agent.Manifest{
				ID:   "coordinator",
				Name: "Coordinator",
				// Capabilities.Tools is empty on purpose — the
				// inheritance floor (DefaultBaseTools) is what
				// EffectiveTools returns and the spec asserts the
				// runtime now respects that.
			}
			// executableMockTool (not gateHaltFakeTool) gives us
			// the execCalled flag we need for the "Execute never
			// ran" assertion.
			fakeBash := &executableMockTool{
				name:        "bash",
				description: "fake bash",
				execResult:  tool.Result{Output: "should never run"},
			}
			eng := makeEngine(manifest, nil)
			eng.AddTool(fakeBash)

			result, err := runTool(eng, "bash")

			Expect(err).NotTo(HaveOccurred(),
				"the gate emits an IsError tool_result, not a Go error — the agent's tool loop sees the rejection and is expected to pivot (delegate, todowrite breakdown, or stop) on the next turn")
			Expect(result.IsError).To(BeTrue(),
				"the call did not run; the rejection must be tagged IsError so the chunk path stamps role=tool_error and provider serialisers route through the failure shape")
			Expect(result.Output).To(ContainSubstring("'bash' is not in this agent's allowed toolset"),
				"the body must name the rejected tool verbatim so the model can reason about which call it issued out-of-scope")
			Expect(result.Output).To(ContainSubstring("Available tools:"),
				"the rejection must enumerate the agent's effective tools so the model has a list of legitimate alternatives — without it the model is left guessing why bash was refused")
			Expect(result.Output).To(ContainSubstring("Delegate to a specialist"),
				"the rejection body's call-to-action is delegation — the investigation note's Layer 3 finding (glm-4.x rationalises 'I'll just do it myself' when delegation seems unavailable) requires the engine to point the model at the alternative path")
			Expect(errors.Is(result.Error, tool.ErrToolNotAllowed)).To(BeTrue(),
				"the wrapped sentinel error lets callers distinguish 'tool not permitted by manifest' from 'tool not found' / generic execution failure — sibling of ErrToolNotFound")
			Expect(fakeBash.execCalled).To(BeFalse(),
				"the load-bearing assertion: the gate fires BEFORE Execute — the side-effecting tool body must never run for an out-of-manifest call")
		})
	})

	When("the manifest declares `delegate` (A.1.3 — bundle expansion)", func() {
		It("permits delegate, background_output, and background_cancel via the bundle alias", func() {
			// buildAllowedToolSetFor expands `delegate` into the
			// three lifecycle tools per commit 3 (May 2026, Gap B).
			// The runtime gate must respect that expansion — the
			// manifest entry `delegate` permits all three at
			// dispatch, not just the literal name.
			manifest := agent.Manifest{
				ID:   "coordinator",
				Name: "Coordinator",
				Capabilities: agent.Capabilities{
					Tools: []string{"delegate"},
				},
			}
			eng := makeEngine(manifest, nil)
			// All three lifecycle tools registered as fakes; each
			// must dispatch cleanly under the bundle alias.
			fakeDelegate := &gateHaltFakeTool{name: "delegate", err: nil}
			fakeBgOutput := &gateHaltFakeTool{name: "background_output", err: nil}
			fakeBgCancel := &gateHaltFakeTool{name: "background_cancel", err: nil}
			eng.AddTool(fakeDelegate)
			eng.AddTool(fakeBgOutput)
			eng.AddTool(fakeBgCancel)

			for _, name := range []string{"delegate", "background_output", "background_cancel"} {
				result, err := runTool(eng, name)
				Expect(err).NotTo(HaveOccurred(),
					"the bundle alias `delegate` expands to the lifecycle trio — none of the three should be rejected")
				Expect(result.IsError).To(BeFalse(),
					"bundle-expanded names must pass the gate; rejecting them would re-introduce the pre-D1 fail-closed semantics the inheritance floor was added to remove")
			}
		})
	})

	When("the unknown tool name matches a known skill (A.1.4 — skill redirect ordering)", func() {
		It("falls through to PR2's skill-name redirect even when the skill name is NOT in the effective toolset", func() {
			// Coordinator manifest with the minimal coordinator
			// surface. `task-tracker` is a skill name, not a
			// registered tool — so the executeToolCall loop never
			// matches `task-tracker` against e.tools. The new gate
			// runs ONLY for matched tools, so it does not interpose
			// on this path. The skill redirect (PR2 / Item 3 Agent
			// Runtime Quality, May 2026) is the right surface here:
			// the body points the model at skill_load(name="X")
			// regardless of whether the skill name is in the
			// agent's effective toolset.
			manifest := agent.Manifest{
				ID:   "coordinator",
				Name: "Coordinator",
				Capabilities: agent.Capabilities{
					// Deliberately omits skill_load and any tool
					// whose name collides with the skill catalogue.
					// `task-tracker` will not appear in the
					// effective set regardless.
					Tools: []string{"delegate"},
				},
			}
			eng := makeEngine(manifest, []string{"task-tracker", "memory-keeper"})
			// Register `bash` so the engine has SOMETHING in
			// e.tools — the not-found fallback would otherwise be
			// suspiciously empty.
			eng.AddTool(&gateHaltFakeTool{name: "bash", err: nil})

			result, err := runTool(eng, "task-tracker")

			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsError).To(BeTrue())
			Expect(result.Output).To(ContainSubstring(`'task-tracker' is a skill, not a tool`),
				"the skill redirect must still fire for unknown tool names that match known skills — the gate sits in the matched-tool branch, not in the fallthrough path")
			Expect(result.Output).To(ContainSubstring(`skill_load(name="task-tracker")`),
				"the redirect body remains the canonical recovery action regardless of the agent's effective toolset — the engine never auto-invokes skill_load, so there is no recursion risk")
			Expect(result.Output).NotTo(ContainSubstring("is not in this agent's allowed toolset"),
				"the new gate must NOT pre-empt the skill redirect path for unmatched tool names; the two surfaces serve different cases")
			Expect(errors.Is(result.Error, tool.ErrToolNotFound)).To(BeTrue(),
				"the skill redirect preserves the ErrToolNotFound sentinel for backward compatibility with callers that distinguish on it")
		})

		It("fires the NEW gate (not the skill redirect) when the skill name COLLIDES with a registered tool that is NOT in the effective set", func() {
			// Collision edge case from the brief: if `task-tracker`
			// is BOTH a skill name AND a registered tool name AND
			// the tool is NOT in the effective set, the new gate
			// wins. Rationale: matched-tool branch reaches the gate
			// before any unmatched-tool fallthrough; the rejection
			// body cites "not in allowed toolset", not "is a skill".
			manifest := agent.Manifest{
				ID:   "coordinator",
				Name: "Coordinator",
				Capabilities: agent.Capabilities{
					Tools: []string{"delegate"},
				},
			}
			eng := makeEngine(manifest, []string{"task-tracker"})
			// Register `task-tracker` as a tool too — the
			// collision case. The skill catalogue still lists it.
			eng.AddTool(&gateHaltFakeTool{name: "task-tracker", err: nil})

			result, err := runTool(eng, "task-tracker")

			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsError).To(BeTrue())
			Expect(result.Output).To(ContainSubstring("'task-tracker' is not in this agent's allowed toolset"),
				"the matched-tool branch reaches the new gate first; the rejection body must cite the toolset, not the skill catalogue")
			Expect(result.Output).NotTo(ContainSubstring("is a skill, not a tool"),
				"the skill redirect lives in the unmatched-tool fallthrough; the matched-tool branch must not redirect through it under collision")
			Expect(errors.Is(result.Error, tool.ErrToolNotAllowed)).To(BeTrue(),
				"matched-tool rejection wraps ErrToolNotAllowed, not ErrToolNotFound — the two sentinels distinguish manifest gating from registry absence")
		})
	})
})
