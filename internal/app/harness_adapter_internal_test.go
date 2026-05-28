package app

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

// The critic-enabler predicate decides per-agent whether the LLM critic
// fires on a given evaluation, so a single critic instance attached at
// startup can be enabled selectively (e.g. only the planner runs the
// critic by default). Manifest's per-agent override always wins; the
// global flag is the fallback when the agent expresses no opinion.
var _ = Describe("newCriticEnabler", func() {
	var registry *agent.Registry

	BeforeEach(func() {
		registry = agent.NewRegistry()
		registry.Register(&agent.Manifest{
			ID:      "planner",
			Name:    "Planner",
			Harness: &agent.HarnessConfig{Enabled: true, CriticEnabled: true},
		})
		registry.Register(&agent.Manifest{
			ID:      "explorer",
			Name:    "Explorer",
			Harness: &agent.HarnessConfig{Enabled: true, CriticEnabled: false},
		})
		registry.Register(&agent.Manifest{
			ID:   "executor",
			Name: "Executor",
			// Harness nil → predicate falls back to global default.
		})
	})

	It("returns true when the agent's manifest opts into the critic", func() {
		enabler := newCriticEnabler(registry, false)
		Expect(enabler("planner")).To(BeTrue(),
			"planner manifest sets critic_enabled: true; per-agent override wins over global")
	})

	It("returns false when the agent's manifest opts out of the critic", func() {
		enabler := newCriticEnabler(registry, true)
		Expect(enabler("explorer")).To(BeFalse(),
			"explorer manifest sets critic_enabled: false; per-agent override wins over global")
	})

	It("falls back to the global default when the manifest has no harness override", func() {
		enabler := newCriticEnabler(registry, true)
		Expect(enabler("executor")).To(BeTrue(),
			"executor manifest has no harness override; global true should win")
	})

	It("falls back to the global default when the agent is unknown to the registry", func() {
		enabler := newCriticEnabler(registry, true)
		Expect(enabler("unknown-agent")).To(BeTrue())

		enablerOff := newCriticEnabler(registry, false)
		Expect(enablerOff("unknown-agent")).To(BeFalse())
	})

	It("falls back to the global default for an empty agent ID", func() {
		enabler := newCriticEnabler(registry, true)
		Expect(enabler("")).To(BeTrue(),
			"empty agentID is the unit-test bypass path; should consult global")
	})

	It("returns the global default when the registry is nil", func() {
		enabler := newCriticEnabler(nil, true)
		Expect(enabler("planner")).To(BeTrue())

		enablerOff := newCriticEnabler(nil, false)
		Expect(enablerOff("planner")).To(BeFalse())
	})
})

// registryHasCriticEnabledAgent gates whether createHarnessStreamer wires
// a critic instance at all when the global cfg.CriticEnabled is false —
// ensuring agents with the per-manifest override still get the critic.
var _ = Describe("registryHasCriticEnabledAgent", func() {
	It("returns true when at least one registered agent has critic_enabled: true", func() {
		r := agent.NewRegistry()
		r.Register(&agent.Manifest{ID: "executor", Name: "Executor"})
		r.Register(&agent.Manifest{
			ID:      "planner",
			Name:    "Planner",
			Harness: &agent.HarnessConfig{Enabled: true, CriticEnabled: true},
		})
		Expect(registryHasCriticEnabledAgent(r)).To(BeTrue())
	})

	It("returns false when no registered agent opts into the critic", func() {
		r := agent.NewRegistry()
		r.Register(&agent.Manifest{ID: "executor", Name: "Executor"})
		r.Register(&agent.Manifest{
			ID:      "writer",
			Name:    "Writer",
			Harness: &agent.HarnessConfig{Enabled: true, CriticEnabled: false},
		})
		Expect(registryHasCriticEnabledAgent(r)).To(BeFalse())
	})

	It("returns false on a nil registry", func() {
		Expect(registryHasCriticEnabledAgent(nil)).To(BeFalse())
	})
})

// resolveHarnessRetries decides the harness retry budget. The wave
// fan-in barrier needs enough budget to walk an orchestrator through
// every stage (evidence → analysis → writing → review) when a member
// narrates-but-doesn't-write on a turn. The config defaulting
// (config.go) sets cfg.Harness.MaxRetries=1 on every load, so the
// pre-fix `if cfg.MaxRetries == 0` guard NEVER fired in production —
// the wave path silently ran with a budget of 1 and gave up on the
// first wave-incomplete check (the synthesis-hang give-up, Defect 2).
// The fix applies a FLOOR when waves are present so the wave path
// always has room to re-prompt, while still honouring a larger explicit
// operator override.
var _ = Describe("resolveHarnessRetries (wave retry-budget floor)", func() {
	It("raises the budget to the wave floor when waves are present and the configured value is below it", func() {
		// The production default that broke the wave path: cfg=1.
		Expect(resolveHarnessRetries(1, true)).To(BeNumerically(">=", waveRetryFloor),
			"a config default of 1 must be lifted to the wave floor when waves are wired")
	})

	It("leaves the configured value untouched when no waves are present", func() {
		Expect(resolveHarnessRetries(1, false)).To(Equal(1),
			"without waves the legacy budget stands — no implicit bump")
		Expect(resolveHarnessRetries(0, false)).To(Equal(0),
			"a zero with no waves means the harness uses its own NewHarness default")
	})

	It("honours an explicit operator override that exceeds the wave floor", func() {
		big := waveRetryFloor + 5
		Expect(resolveHarnessRetries(big, true)).To(Equal(big),
			"an operator who sets a larger budget keeps it — the floor never lowers a value")
	})
})

// resolveCriticModel is a tiny precedence helper; covering it directly
// here keeps the public-API surface narrow (no exported shim needed).
var _ = Describe("resolveCriticModel precedence", func() {
	It("prefers the explicit critic override when both values are present", func() {
		Expect(resolveCriticModel("opus-4-1", "sonnet-4-5")).To(Equal("opus-4-1"))
	})

	It("falls back to the default-provider model when no override is set", func() {
		Expect(resolveCriticModel("", "glm-4.7")).To(Equal("glm-4.7"))
	})

	It("returns empty when neither override nor fallback are set", func() {
		Expect(resolveCriticModel("", "")).To(BeEmpty())
	})
})
