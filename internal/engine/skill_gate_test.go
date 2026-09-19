package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tracer"
)

// P1 (July 2026) — deterministic skills-first gate. The guard
// auto-injects always-active skills instead of rejecting, and trips a
// circuit breaker after three consecutive rejections so a model that
// never issues skill_load cannot wedge its session.
var _ = Describe("Engine skills-first deterministic gate", func() {
	makeEngine := func(knownSkills []string) *engine.Engine {
		providerReg := provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})
		manifest := agent.Manifest{
			ID:   "tester",
			Name: "Tester",
			Capabilities: agent.Capabilities{
				Tools: []string{"bash"},
			},
		}
		cfg := engine.Config{
			Manifest:      manifest,
			AgentRegistry: agent.NewRegistry(),
			Registry:      providerReg,
			ChatProvider:  &mockProvider{name: "spy"},
		}
		if knownSkills != nil {
			cfg.KnownSkillsFunc = func() []string { return knownSkills }
		}
		eng := engine.New(cfg)
		eng.AddTool(&gateHaltFakeTool{name: "bash", err: nil})
		return eng
	}

	seedSkills := func(eng *engine.Engine, names ...string) {
		skills := make([]skill.Skill, 0, len(names))
		for _, n := range names {
			skills = append(skills, skill.Skill{Name: n, Content: "content of " + n})
		}
		engine.SetSkillsForTest(eng, skills)
	}

	runTool := func(eng *engine.Engine, name string) (tool.Result, error) {
		return eng.ExecuteToolCallForTest(context.Background(), "sess-skill-gate", &provider.ToolCall{
			ID:        "call-" + name,
			Name:      name,
			Arguments: map[string]any{},
		})
	}

	When("always-active skills are not yet loaded", func() {
		It("auto-injects the skills and proceeds with the original call instead of rejecting", func() {
			eng := makeEngine([]string{"pre-action"})
			seedSkills(eng, "pre-action")

			result, err := runTool(eng, "bash")

			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsError).To(BeFalse(),
				"the happy path is deterministic injection, never rejection — the guard is a no-op once skills are baked")
			Expect(result.Output).To(Equal("fake output"))
			Expect(engine.SkillLoadCompletedForTest(eng, "sess-skill-gate")).To(BeTrue(),
				"after deterministic injection the gate must be satisfied so subsequent calls proceed")
		})
	})

	When("no skill content is resolvable", func() {
		It("still rejects on the first calls and trips the circuit breaker after three consecutive rejections", func() {
			eng := makeEngine([]string{"pre-action"})

			for i := 0; i < 3; i++ {
				result, err := runTool(eng, "bash")
				Expect(err).NotTo(HaveOccurred())
				Expect(result.IsError).To(BeTrue(),
					"without injectable content the guard keeps rejecting until the threshold is reached (call %d)", i+1)
			}

			result, err := runTool(eng, "bash")

			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsError).To(BeFalse(),
				"after three consecutive rejections the circuit breaker auto-satisfies the gate and lets the fourth call through")
			Expect(result.Output).To(Equal("fake output"))
		})
	})

	When("a successful skill_load breaks the rejection streak", func() {
		It("resets the consecutive rejection counter", func() {
			eng := makeEngine([]string{"pre-action"})

			for i := 0; i < 2; i++ {
				_, _ = runTool(eng, "bash")
			}
			engine.MarkSkillLoadCalledForTest(eng, "sess-skill-gate")

			Expect(engine.SkillGuardRejectionCountForTest(eng, "sess-skill-gate")).To(Equal(0),
				"a compliant skill_load resets the streak so a later non-compliance burst starts from zero")
		})
	})
})

// countingSkillGuardRecorder captures the skill-guard telemetry calls
// so specs can assert the gate paths move the recorder counters.
type countingSkillGuardRecorder struct {
	tracer.NoopRecorder
	autoInjections int
	rejections     int
	breakerTrips   int
}

// RecordSkillGuardAutoInjection counts one auto-injection.
func (r *countingSkillGuardRecorder) RecordSkillGuardAutoInjection() { r.autoInjections++ }

// RecordSkillGuardRejection counts one rejection.
func (r *countingSkillGuardRecorder) RecordSkillGuardRejection() { r.rejections++ }

// RecordSkillGuardCircuitBreakerTrip counts one circuit-breaker trip.
func (r *countingSkillGuardRecorder) RecordSkillGuardCircuitBreakerTrip() { r.breakerTrips++ }

var _ = Describe("Engine skill guard telemetry", func() {
	makeRecordedEngine := func(knownSkills []string, rec *countingSkillGuardRecorder) *engine.Engine {
		providerReg := provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})
		manifest := agent.Manifest{
			ID:   "telemetry-tester",
			Name: "Telemetry Tester",
			Capabilities: agent.Capabilities{
				Tools: []string{"bash"},
			},
		}
		cfg := engine.Config{
			Manifest:      manifest,
			AgentRegistry: agent.NewRegistry(),
			Registry:      providerReg,
			ChatProvider:  &mockProvider{name: "spy"},
			Recorder:      rec,
			KnownSkillsFunc: func() []string { return knownSkills },
		}
		eng := engine.New(cfg)
		eng.AddTool(&gateHaltFakeTool{name: "bash", err: nil})
		return eng
	}

	runGateTool := func(eng *engine.Engine) (tool.Result, error) {
		return eng.ExecuteToolCallForTest(context.Background(), "sess-skill-gate-telemetry", &provider.ToolCall{
			ID:        "call-bash",
			Name:      "bash",
			Arguments: map[string]any{},
		})
	}

	It("counts a deterministic auto-injection through the inject path", func() {
		rec := &countingSkillGuardRecorder{}
		eng := makeRecordedEngine([]string{"pre-action"}, rec)
		engine.SetSkillsForTest(eng, []skill.Skill{{Name: "pre-action", Content: "content of pre-action"}})

		result, err := runGateTool(eng)

		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeFalse())
		Expect(rec.autoInjections).To(Equal(1),
			"the auto-inject path must move flowstate_skill_guard_auto_injections_total")
		Expect(rec.rejections).To(Equal(0))
		Expect(rec.breakerTrips).To(Equal(0))
	})

	It("counts rejections and the circuit-breaker trip through the reject path", func() {
		rec := &countingSkillGuardRecorder{}
		eng := makeRecordedEngine([]string{"pre-action"}, rec)

		for i := 0; i < 3; i++ {
			result, err := runGateTool(eng)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsError).To(BeTrue(), "call %d rejects while content is unresolvable", i+1)
		}
		Expect(rec.rejections).To(Equal(3),
			"each rejection must move flowstate_skill_guard_rejections_total")
		Expect(rec.breakerTrips).To(Equal(0))

		result, err := runGateTool(eng)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsError).To(BeFalse(),
			"the fourth call auto-satisfies the gate through the circuit breaker")
		Expect(rec.breakerTrips).To(Equal(1),
			"the circuit-breaker path must move flowstate_skill_guard_circuit_breaker_trips_total")
		Expect(rec.autoInjections).To(Equal(0),
			"no content was injectable, so the auto-injection counter stays flat")
	})
})
