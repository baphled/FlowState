package app

import (
	"context"
	"io/fs"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/plan/harness"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/swarm"
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

// coordWaveValidator.MissingForChain must resolve expected keys against
// the SWARM's engine-assigned chainID (swarmCtx.ChainPrefix, stamped by
// AssignRunChainID at swarm start — #28) when running inside a swarm
// turn, NOT the per-delegate session.IDKey. The engine rebinds
// session.IDKey to the delegate session id on every delegation
// (delegation.go:2541), so a validator that reads session.IDKey inside a
// plan-writer delegate looks under "delegate-plan-writer-<ts>/..." while
// explorer/librarian wrote evidence under the run's swarm chainID. That
// namespace mismatch produced `harness exhausted retries with wave still
// incomplete maxRetries=8` and skipped the plan-reviewer stage entirely
// (live run 2026-05-28T20:05). The validator must mirror the same source
// gates + publish use (swarm.ScopeFromContext → ChainPrefix).
var _ = Describe("coordWaveValidator chainID resolution", func() {
	var (
		store *coordination.MemoryStore
		v     *coordWaveValidator
		wave  harness.WaveStage
	)

	BeforeEach(func() {
		store = coordination.NewMemoryStore()
		v = &coordWaveValidator{store: store}
		wave = harness.WaveStage{
			Name: "evidence",
			ExpectedKeys: []string{
				"{chainID}/codebase-findings",
				"{chainID}/external-refs",
			},
		}
	})

	Context("inside a delegate sub-session of a swarm run", func() {
		It("resolves the swarm engine-assigned chainID, not the rebound delegate session id", func() {
			const swarmChain = "planning-loop-abc123"
			// Evidence written by explorer/librarian under the run's
			// engine-assigned swarm chainID.
			Expect(store.Set(swarmChain+"/codebase-findings", []byte("{}"))).To(Succeed())
			Expect(store.Set(swarmChain+"/external-refs", []byte("{}"))).To(Succeed())

			// The engine has rebound session.IDKey to the delegate
			// session id (delegation.go:2541). The dispatcher attached
			// the swarm scope (swarm.WithScope) with the engine-assigned
			// chainID on ChainPrefix.
			ctx := context.WithValue(context.Background(), session.IDKey{}, "delegate-plan-writer-1748462700")
			ctx = swarm.WithScope(ctx, &swarm.Context{
				SwarmID:         "planning-loop",
				ChainPrefix:     swarmChain,
				ChainIDAssigned: true,
			})

			missing, err := v.MissingForChain(ctx, "plan-writer", wave)
			Expect(err).NotTo(HaveOccurred())
			Expect(missing).To(BeEmpty(),
				"validator must resolve the swarm chainID (ChainPrefix), not the per-delegate session.IDKey")
		})

		It("reports the swarm-namespaced key as missing when the evidence is genuinely absent", func() {
			const swarmChain = "planning-loop-abc123"
			ctx := context.WithValue(context.Background(), session.IDKey{}, "delegate-plan-writer-1748462700")
			ctx = swarm.WithScope(ctx, &swarm.Context{
				SwarmID:         "planning-loop",
				ChainPrefix:     swarmChain,
				ChainIDAssigned: true,
			})

			missing, err := v.MissingForChain(ctx, "plan-writer", wave)
			Expect(err).NotTo(HaveOccurred())
			Expect(missing).To(ConsistOf(
				swarmChain+"/codebase-findings",
				swarmChain+"/external-refs",
			), "missing keys must be reported under the swarm chainID namespace")
		})
	})

	Context("non-swarm / standalone harness use (backwards compat)", func() {
		It("falls back to session.IDKey when no swarm scope is attached", func() {
			const sessionChain = "standalone-session-42"
			Expect(store.Set(sessionChain+"/codebase-findings", []byte("{}"))).To(Succeed())
			Expect(store.Set(sessionChain+"/external-refs", []byte("{}"))).To(Succeed())

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionChain)

			missing, err := v.MissingForChain(ctx, "planner", wave)
			Expect(err).NotTo(HaveOccurred())
			Expect(missing).To(BeEmpty(),
				"standalone harness use must keep resolving session.IDKey")
		})

		It("honours an explicit standalone scope marker (nil swarm context) by falling back to session.IDKey", func() {
			const sessionChain = "standalone-session-99"
			Expect(store.Set(sessionChain+"/codebase-findings", []byte("{}"))).To(Succeed())
			Expect(store.Set(sessionChain+"/external-refs", []byte("{}"))).To(Succeed())

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionChain)
			// Dispatcher marked this turn explicitly standalone.
			ctx = swarm.WithScope(ctx, nil)

			missing, err := v.MissingForChain(ctx, "planner", wave)
			Expect(err).NotTo(HaveOccurred())
			Expect(missing).To(BeEmpty(),
				"a nil swarm scope means standalone — resolve session.IDKey, not a swarm chain")
		})
	})

	Context("chainID-identity unification (engine-owned, slugified)", func() {
		It("resolves the SLUGIFIED engine-assigned chain so it agrees with the member-write preamble and the publisher", func() {
			// The member-write preamble, the publisher and the gate all route
			// the chainID through swarm.SlugifyChainID. The validator must
			// read the SAME normalised value or it looks under a different
			// namespace than the members wrote (the core divergence). The
			// "{swarmID}-{hash}" engine form is already safe, so slugify is a
			// no-op here — but reading via slugify keeps every site in lockstep.
			const swarmChain = "planning-loop-abc123def456"
			Expect(store.Set(swarmChain+"/codebase-findings", []byte("{}"))).To(Succeed())
			Expect(store.Set(swarmChain+"/external-refs", []byte("{}"))).To(Succeed())

			ctx := swarm.WithScope(context.Background(), &swarm.Context{
				SwarmID:         "planning-loop",
				ChainPrefix:     swarmChain,
				ChainIDAssigned: true,
			})

			missing, err := v.MissingForChain(ctx, "plan-writer", wave)
			Expect(err).NotTo(HaveOccurred())
			Expect(missing).To(BeEmpty(),
				"the validator resolves the slugified engine-assigned chain, matching where members wrote")
		})

		It("slugifies a session.IDKey containing a slash so a free-form id can never fracture key parsing here either", func() {
			// Standalone/legacy path: a caller threading a free-form session id
			// with a slash must NOT reach key construction verbatim. The
			// validator slugifies it, matching where a slugifying writer landed.
			// Asserted via the MISSING path with NO keys present, so the
			// suffix-scan backstop cannot mask the slash: a non-slugifying
			// resolve would report "planner/sme-sectional-plans/..." (the slash
			// form) while the fix reports the slugged "planner-sme-..." form.
			const slugged = "planner-sme-sectional-plans"

			ctx := context.WithValue(context.Background(), session.IDKey{}, "planner/sme-sectional-plans")

			missing, err := v.MissingForChain(ctx, "planner", wave)
			Expect(err).NotTo(HaveOccurred())
			Expect(missing).To(ConsistOf(
				slugged+"/codebase-findings",
				slugged+"/external-refs",
			), "missing keys must be reported under the SLUGGED namespace, not the slash-bearing session id")
			for _, m := range missing {
				Expect(m).NotTo(HavePrefix("planner/"),
					"a slash-bearing chainID must never reach key construction verbatim")
			}
		})
	})

	Context("suffix-scan backstop", func() {
		It("fires when the resolved swarm chainID has no exact key but evidence exists under another chain", func() {
			// Evidence landed under a near-miss chain (e.g. a stale or
			// lead-renamed namespace). The exact swarm chainID has no
			// key, so a hard-fail would churn the 8-retry loop. The
			// suffix-scan backstop must rescue the near-miss.
			Expect(store.Set("some-other-chain/codebase-findings", []byte("{}"))).To(Succeed())
			Expect(store.Set("some-other-chain/external-refs", []byte("{}"))).To(Succeed())

			ctx := swarm.WithScope(context.Background(), &swarm.Context{
				SwarmID:         "planning-loop",
				ChainPrefix:     "planning-loop-noexactkey",
				ChainIDAssigned: true,
			})

			missing, err := v.MissingForChain(ctx, "plan-writer", wave)
			Expect(err).NotTo(HaveOccurred())
			Expect(missing).To(BeEmpty(),
				"suffix-scan backstop must apply when the resolved chainID yields no exact key, preventing 8-retry hard-fail churn")
		})

		It("still reports missing when neither the exact key nor any suffix match exists", func() {
			ctx := swarm.WithScope(context.Background(), &swarm.Context{
				SwarmID:         "planning-loop",
				ChainPrefix:     "planning-loop-empty",
				ChainIDAssigned: true,
			})

			missing, err := v.MissingForChain(ctx, "plan-writer", wave)
			Expect(err).NotTo(HaveOccurred())
			Expect(missing).NotTo(BeEmpty(),
				"genuinely-absent evidence must still report missing after the suffix-scan backstop")
		})
	})
})

// Retiring the wave-fan-in harness from the planning loop: the planner
// manifest must no longer declare `harness.waves`. The wave-fan-in
// validator (coordWaveValidator) ran inside a delegate sub-session whose
// context did not carry the swarm scope, so it fell back to the
// per-delegate session UUID and never found the evidence members actually
// wrote — emitting `harness exhausted retries with wave still incomplete`
// and skipping the plan-reviewer stage (live runs 2026-05-28). The swarm
// post-member gates in internal/app/swarms/planning-loop.yml resolve the
// chainID correctly (swarm.ScopeFromContext → ChainPrefix) and cover the
// SAME stages plus the publication honesty gate, so the wave harness is a
// redundant, broken parallel mechanism in front of a working gate system.
//
// Dropping the `waves:` block (while keeping harness_enabled +
// critic_enabled) makes collectAgentWaves(registry) return empty for the
// planning roster, so createHarnessStreamer never wires the validator and
// the planning dispatch path can never emit the wave-incomplete re-prompt.
// The validator code and its unit specs above are retained (dormant) so a
// future non-planning orchestrator can opt back in by re-declaring waves.
var _ = Describe("planning-loop wave-fan-in harness retirement", func() {
	It("the embedded planner manifest declares no harness.waves", func() {
		data, err := fs.ReadFile(agentsFS, "agents/planner.md")
		Expect(err).NotTo(HaveOccurred(), "read embedded planner manifest")

		content := string(data)
		Expect(content).To(HavePrefix("---"), "planner manifest must have YAML frontmatter")
		fm := strings.SplitN(content[3:], "---", 2)
		Expect(fm).To(HaveLen(2), "planner frontmatter must be closed with ---")

		var probe struct {
			ID      string `yaml:"id"`
			Harness *struct {
				Enabled       bool `yaml:"enabled"`
				CriticEnabled bool `yaml:"critic_enabled"`
				Waves         []struct {
					Name string `yaml:"name"`
				} `yaml:"waves"`
			} `yaml:"harness"`
		}
		Expect(yaml.Unmarshal([]byte(strings.TrimSpace(fm[0])), &probe)).To(Succeed(),
			"parse planner frontmatter")

		Expect(probe.Harness).NotTo(BeNil(),
			"planner must keep its harness block (critic stays wired)")
		Expect(probe.Harness.Waves).To(BeEmpty(),
			"planner manifest must NOT declare harness.waves — the wave-fan-in barrier is retired in favour of the swarm post-member gates")
		Expect(probe.Harness.CriticEnabled).To(BeTrue(),
			"retiring waves must not disable the LLM critic — only the wave barrier is removed")
	})

	It("collectAgentWaves returns no stages for the planning-loop roster, so the wave validator is never wired", func() {
		// Mirror the planning-loop roster from
		// internal/app/swarms/planning-loop.yml. With the planner's waves
		// retired, no member declares waves, so collectAgentWaves must be
		// empty — createHarnessStreamer then skips coordWaveValidator
		// wiring entirely (the planning dispatch path cannot re-prompt on
		// "wave still incomplete").
		registry := agent.NewRegistry()
		registry.Register(&agent.Manifest{
			ID:      "planner",
			Name:    "Planner",
			Harness: &agent.HarnessConfig{Enabled: true, CriticEnabled: true},
		})
		for _, id := range []string{"explorer", "librarian", "analyst", "plan-writer", "plan-reviewer"} {
			registry.Register(&agent.Manifest{
				ID:      id,
				Name:    id,
				Harness: &agent.HarnessConfig{Enabled: true},
			})
		}

		Expect(collectAgentWaves(registry)).To(BeEmpty(),
			"no planning-loop member declares waves once the harness is retired")
	})
})
