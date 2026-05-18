package engine_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/swarm"
)

func newSwarmLeadEngine(leadID string, registry *agent.Registry) *engine.Engine {
	return engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "swarm-lead-test"},
		Manifest: agent.Manifest{
			ID:   leadID,
			Name: "Senior Engineer",
			Instructions: agent.Instructions{
				SystemPrompt: "You are the senior engineer.",
			},
		},
		AgentRegistry: registry,
	})
}

// newCoordinatorEngineWithSwarmRegistry builds the engine used by the
// meta-swarm specs. The coordinator manifest carries `id: coordinator`
// so the swarm-lead block fires when swarmCtx.LeadAgent == "coordinator",
// and a populated swarm registry lets the lead-block renderer fall
// through to swarm-id lookup for members that aren't agents.
func newCoordinatorEngineWithSwarmRegistry(swarmReg *swarm.Registry) *engine.Engine {
	return engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "meta-swarm-lead-test"},
		Manifest: agent.Manifest{
			ID:   "coordinator",
			Name: "Coordinator",
			Instructions: agent.Instructions{
				SystemPrompt: "You are the coordinator.",
			},
		},
		AgentRegistry: agent.NewRegistry(), // empty — sub-swarm ids must NOT resolve as agents
		SwarmRegistry: swarmReg,
	})
}

func newSwarmTestRegistry() *agent.Registry {
	registry := agent.NewRegistry()
	registry.Register(&agent.Manifest{
		ID:       "senior-engineer",
		Name:     "Senior Engineer",
		Metadata: agent.Metadata{Role: "Lead Engineer"},
	})
	registry.Register(&agent.Manifest{
		ID:       "explorer",
		Name:     "Explorer",
		Metadata: agent.Metadata{Role: "Codebase Explorer"},
	})
	registry.Register(&agent.Manifest{
		ID:       "Code-Reviewer",
		Name:     "Code Reviewer",
		Metadata: agent.Metadata{Role: "Quality Gate"},
	})
	return registry
}

func newBugHuntContext() swarm.Context {
	return swarm.Context{
		SwarmID:     "bug-hunt",
		LeadAgent:   "senior-engineer",
		Members:     []string{"explorer", "Code-Reviewer"},
		ChainPrefix: "bug-hunt",
		Depth:       1,
	}
}

var _ = Describe("Engine swarm-lead system prompt", func() {
	Describe("when no swarm context is attached", func() {
		It("does not include swarm-lead text in the prompt", func() {
			eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())

			prompt := eng.BuildSystemPrompt()

			Expect(strings.ToLower(prompt)).NotTo(ContainSubstring("leading swarm"))
			Expect(prompt).NotTo(ContainSubstring("Swarm Leadership"))
		})
	})

	Describe("when a swarm context is attached to the lead", func() {
		It("emits swarm id, member ids, and delegation guidance", func() {
			eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())
			ctx := newBugHuntContext()

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring("bug-hunt"))
			Expect(prompt).To(ContainSubstring("explorer"))
			Expect(prompt).To(ContainSubstring("Code-Reviewer"))
			Expect(strings.ToLower(prompt)).To(ContainSubstring("delegate"))
			Expect(prompt).To(ContainSubstring("bug-hunt/senior-engineer"))
		})

		// Parallel dispatch: the lead must be instructed to emit independent
		// member delegate calls in a single assistant message so the engine's
		// concurrent dispatch path fires. Without this instruction the model
		// defaults to sequential one-at-a-time dispatch, burning 3–5× more
		// wall-clock time and tokens on wait overhead between members.
		// Wave-dependent members (e.g. reviewers that read explorer output)
		// must still be dispatched after their upstream members complete.
		It("instructs the lead to dispatch independent members in a single parallel message", func() {
			eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())
			ctx := newBugHuntContext()

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			lowerPrompt := strings.ToLower(prompt)
			Expect(lowerPrompt).To(
				SatisfyAny(
					ContainSubstring("single message"),
					ContainSubstring("simultaneously"),
					ContainSubstring("parallel"),
					ContainSubstring("at once"),
				),
				"swarm lead prompt must instruct the model to dispatch all members "+
					"in one message with multiple tool calls; without this the model "+
					"dispatches sequentially and blocks on each result before starting the next",
			)
		})

		It("resolves member names and roles from the agent registry", func() {
			eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())
			ctx := newBugHuntContext()

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring("Explorer"))
			Expect(prompt).To(ContainSubstring("Codebase Explorer"))
			Expect(prompt).To(ContainSubstring("Quality Gate"))
		})
	})

	Describe("cache invalidation", func() {
		It("removes swarm-lead text after SetSwarmContext(nil)", func() {
			eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())
			ctx := newBugHuntContext()

			eng.SetSwarmContext(&ctx)
			withSwarm := eng.BuildSystemPrompt()
			Expect(strings.ToLower(withSwarm)).To(ContainSubstring("leading swarm"))

			eng.SetSwarmContext(nil)
			withoutSwarm := eng.BuildSystemPrompt()

			Expect(strings.ToLower(withoutSwarm)).NotTo(ContainSubstring("leading swarm"))
			Expect(withoutSwarm).NotTo(ContainSubstring("Swarm Leadership"))
		})

		It("returns identical prompts on repeated calls with the same context", func() {
			eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())
			ctx := newBugHuntContext()
			eng.SetSwarmContext(&ctx)

			first := eng.BuildSystemPrompt()
			second := eng.BuildSystemPrompt()

			Expect(first).To(Equal(second))
		})
	})

	Describe("non-leading agent", func() {
		It("does not emit the leadership section even when swarm context is attached", func() {
			eng := newSwarmLeadEngine("explorer", newSwarmTestRegistry())
			ctx := newBugHuntContext()

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			Expect(strings.ToLower(prompt)).NotTo(ContainSubstring("leading swarm"))
			Expect(prompt).NotTo(ContainSubstring("Swarm Leadership"))
		})
	})

	// Meta-Swarm Coordinator Architecture (May 2026) — Phase 2.
	//
	// When the coordinator leads meta-swarm whose members ARE OTHER
	// SWARMS, the lead-block renderer (engine.go::appendSwarmLeadSectionFor)
	// must resolve each swarm-id member through the swarm registry and
	// render its description with a `(swarm)` suffix so the model can
	// tell at a glance that delegating to one of these members
	// dispatches a whole sub-swarm rather than a single agent.
	//
	// Without this, the renderer falls back to the bare id (since the
	// agent registry has no `a-team` / `dev-swarm` / etc. agents), the
	// model sees no description text, and the routing decision becomes
	// guesswork. The regression-pin tests below assert: each swarm-id
	// member appears in the prompt, each renders with its swarm
	// description, and the `(swarm)` marker is present so the kind is
	// disambiguated for the model.
	Describe("when the lead's swarm members are themselves swarms (meta-swarm)", func() {
		newMetaSwarmRegistry := func() *swarm.Registry {
			reg := swarm.NewRegistry()
			reg.Register(&swarm.Manifest{
				ID:          "meta-swarm",
				Lead:        "coordinator",
				Members:     []string{"a-team", "dev-swarm", "planning-loop", "board-room"},
				Description: "Top-level orchestrator. Coordinator picks the right sub-swarm.",
			})
			reg.Register(&swarm.Manifest{
				ID:          "a-team",
				Lead:        "coordinator",
				Description: "A-Team Swarm (research → strategy → critique → writing → execution)",
			})
			reg.Register(&swarm.Manifest{
				ID:          "dev-swarm",
				Lead:        "Team-Lead",
				Description: "Dev Swarm (full-lifecycle implementation through Team-Lead)",
			})
			reg.Register(&swarm.Manifest{
				ID:          "planning-loop",
				Lead:        "planner",
				Description: "Planning Loop (requirements → research → plan)",
			})
			reg.Register(&swarm.Manifest{
				ID:          "board-room",
				Lead:        "chair",
				Description: "Board Room (financial/strategic analysis)",
			})
			return reg
		}

		newMetaSwarmContext := func() swarm.Context {
			return swarm.Context{
				SwarmID:     "meta-swarm",
				LeadAgent:   "coordinator",
				Members:     []string{"a-team", "dev-swarm", "planning-loop", "board-room"},
				ChainPrefix: "meta",
				Depth:       0,
			}
		}

		It("renders every sub-swarm member with its description and a (swarm) kind marker", func() {
			eng := newCoordinatorEngineWithSwarmRegistry(newMetaSwarmRegistry())
			ctx := newMetaSwarmContext()

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			// All four sub-swarms appear as members in the lead block.
			Expect(prompt).To(ContainSubstring("a-team"))
			Expect(prompt).To(ContainSubstring("dev-swarm"))
			Expect(prompt).To(ContainSubstring("planning-loop"))
			Expect(prompt).To(ContainSubstring("board-room"))

			// Each sub-swarm's description is rendered so the model can
			// match user intent against a meaningful blurb.
			Expect(prompt).To(ContainSubstring("A-Team Swarm"))
			Expect(prompt).To(ContainSubstring("Dev Swarm"))
			Expect(prompt).To(ContainSubstring("Planning Loop"))
			Expect(prompt).To(ContainSubstring("Board Room"))

			// `(swarm)` marker disambiguates kind. Without this, the
			// model can't tell whether `a-team` is an agent or a
			// nested swarm and may invoke the wrong tool semantics.
			Expect(strings.Count(prompt, "(swarm)")).To(BeNumerically(">=", 4),
				"each sub-swarm member must render with `(swarm)` so the model "+
					"knows delegate('a-team', ...) dispatches a whole swarm, not an agent")
		})

		It("renders the coordination namespace under the meta-swarm chain prefix", func() {
			eng := newCoordinatorEngineWithSwarmRegistry(newMetaSwarmRegistry())
			ctx := newMetaSwarmContext()

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring("meta/coordinator"),
				"meta-swarm uses chain_prefix `meta`; the coord-store namespace "+
					"must reflect that so sub-swarm runs don't collide with a-team / planning-loop / board-room / dev-swarm namespaces")
		})
	})
})
