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

		// Parallel dispatch: the lead must be instructed to emit all member
		// delegate calls in a single assistant message so the engine's
		// concurrent dispatch path fires. Without this instruction the model
		// defaults to sequential one-at-a-time dispatch, burning 3–5× more
		// wall-clock time and tokens on wait overhead between members.
		It("instructs the lead to dispatch all members in a single parallel message", func() {
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
})
