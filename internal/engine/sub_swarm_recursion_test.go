package engine_test

import (
	"context"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
)

// registerTwoLevelSwarm seeds reg with parent-swarm and child-swarm
// manifests so a member of the parent resolves to the child via
// swarm.Resolve.
func registerTwoLevelSwarm(reg *swarm.Registry) {
	reg.Register(&swarm.Manifest{
		SchemaVersion: "1.0.0",
		ID:            "parent-swarm",
		Lead:          "lead-a",
		Members:       []string{"child-swarm"},
		SwarmType:     swarm.SwarmTypeAnalysis,
	})
	reg.Register(&swarm.Manifest{
		SchemaVersion: "1.0.0",
		ID:            "child-swarm",
		Lead:          "lead-b",
		Members:       []string{"reviewer"},
		SwarmType:     swarm.SwarmTypeAnalysis,
	})
}

// trivialStreamer drains a single Done chunk, optionally counting
// its invocations on the supplied atomic.
func trivialStreamer(calls *atomic.Int32) streaming.Streamer {
	return streamerFunc(func(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		if calls != nil {
			calls.Add(1)
		}
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Content: "ok", Done: true}
		close(ch)
		return ch, nil
	})
}

// buildEnginesForRecursion constructs the lead + sub-lead + reviewer
// engine triple plus the engines map the DelegateTool needs.
func buildEnginesForRecursion() (*engine.Engine, map[string]*engine.Engine) {
	lead := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "lead"},
		Manifest: agent.Manifest{
			ID:                "lead-a",
			Name:              "Lead A",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			Delegation:        agent.Delegation{CanDelegate: true},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	subLead := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "lead-b"},
		Manifest: agent.Manifest{
			ID:                "lead-b",
			Name:              "Lead B",
			Instructions:      agent.Instructions{SystemPrompt: "lead-b"},
			Delegation:        agent.Delegation{CanDelegate: true},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	reviewer := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "reviewer"},
		Manifest: agent.Manifest{
			ID:                "reviewer",
			Name:              "Reviewer",
			Instructions:      agent.Instructions{SystemPrompt: "reviewer"},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	engines := map[string]*engine.Engine{
		"lead-a":   lead,
		"lead-b":   subLead,
		"reviewer": reviewer,
	}
	return lead, engines
}

var _ = Describe("SubSwarmRecursion", func() {
	Context("when a member of the parent resolves to a child swarm id", func() {
		It("recurses into the child swarm and dispatches the inner member", func() {
			reg := swarm.NewRegistry()
			registerTwoLevelSwarm(reg)
			lead, engines := buildEnginesForRecursion()

			parentManifest, _ := reg.Get("parent-swarm")
			parentCtx := swarm.NewContext(parentManifest.ID, parentManifest)
			lead.SetSwarmContext(&parentCtx)

			var reviewerCalls atomic.Int32
			streamers := map[string]streaming.Streamer{
				"reviewer": trivialStreamer(&reviewerCalls),
				"lead-b":   trivialStreamer(nil),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead-a").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &parentCtx, []string{"child-swarm"}, "go")

			Expect(err).NotTo(HaveOccurred())
			Expect(reviewerCalls.Load()).To(Equal(int32(1)))
		})

		It("propagates the depth through NestSubSwarm so the child carries Depth=2", func() {
			reg := swarm.NewRegistry()
			registerTwoLevelSwarm(reg)
			parentManifest, _ := reg.Get("parent-swarm")
			parentCtx := swarm.NewContext(parentManifest.ID, parentManifest)

			childCtx := parentCtx.NestSubSwarm("child-swarm")

			Expect(parentCtx.Depth).To(Equal(1))
			Expect(childCtx.Depth).To(Equal(2))
			Expect(childCtx.ChainPrefix).To(Equal("parent-swarm/child-swarm"))
		})
	})

	// Meta-Swarm Coordinator Architecture (May 2026) — Phase 3.
	//
	// When the coordinator (inside meta-swarm) calls
	// `delegate("a-team", brief)`, the delegate tool's Execute entry
	// point must route through the swarm-target dispatch branch
	// instead of the agent-engine lookup. Without this, Execute calls
	// resolveAgentID → ResolveByNameOrAlias → registry miss → error
	// because the agent registry has no `a-team` agent (and shouldn't —
	// `a-team` is a swarm).
	//
	// The swarm-target dispatch branch detects when subagent_type
	// resolves to a swarm in the swarm registry AND the active swarm
	// context lists that id in Members[], then fans out via
	// DispatchSwarmMembers with a child Context constructed from the
	// sub-swarm manifest.
	Context("when the delegate tool is invoked with a swarm-id target from inside a parent swarm", func() {
		It("dispatches the named sub-swarm's members and returns a tool.Result", func() {
			reg := swarm.NewRegistry()
			registerTwoLevelSwarm(reg)
			lead, engines := buildEnginesForRecursion()

			// Active swarm context = parent-swarm. Members lists
			// `child-swarm` as the swarm-id target.
			parentManifest, _ := reg.Get("parent-swarm")
			parentCtx := swarm.NewContext(parentManifest.ID, parentManifest)
			lead.SetSwarmContext(&parentCtx)

			var reviewerCalls atomic.Int32
			streamers := map[string]streaming.Streamer{
				"reviewer": trivialStreamer(&reviewerCalls),
				"lead-b":   trivialStreamer(nil),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead-a").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithOwnerEngine(lead)

			result, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "child-swarm",
					"message":       "fan out the work",
				},
			})

			Expect(err).NotTo(HaveOccurred(),
				"delegate('child-swarm', ...) must take the swarm-dispatch branch — "+
					"agent-registry miss should NOT short-circuit to errAgentNotInAllowlist when the id is a swarm in Members[]")
			Expect(reviewerCalls.Load()).To(Equal(int32(1)),
				"the child swarm's reviewer member must have been dispatched once")
			Expect(result.Output).NotTo(BeEmpty(),
				"swarm-dispatch must synthesise a tool.Result.Output so the caller's transcript shows the delegation happened")
		})

		It("preserves the agent-target path when subagent_type is an agent in the active swarm's members", func() {
			// Regression pin: from inside a swarm whose Members[] lists
			// an AGENT (not a sub-swarm), delegate to that agent must
			// still take the agent-engine path. The swarm-dispatch
			// branch must not steal agent-target traffic.
			reg := swarm.NewRegistry()
			reg.Register(&swarm.Manifest{
				SchemaVersion: "1.0.0",
				ID:            "review-swarm",
				Lead:          "lead-a",
				Members:       []string{"reviewer"},
				SwarmType:     swarm.SwarmTypeAnalysis,
			})
			lead, engines := buildEnginesForRecursion()

			ctx := swarm.NewContext("review-swarm", &swarm.Manifest{
				ID:      "review-swarm",
				Lead:    "lead-a",
				Members: []string{"reviewer"},
			})
			lead.SetSwarmContext(&ctx)

			var reviewerCalls atomic.Int32
			streamers := map[string]streaming.Streamer{
				"reviewer": trivialStreamer(&reviewerCalls),
			}

			agentReg := agent.NewRegistry()
			agentReg.Register(&agent.Manifest{
				ID:   "reviewer",
				Name: "Reviewer",
			})

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead-a").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithRegistry(agentReg).
				WithOwnerEngine(lead)

			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "reviewer",
					"message":       "please review",
				},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(reviewerCalls.Load()).To(Equal(int32(1)),
				"the agent-target path must still fire — swarm-dispatch must not capture agent-id targets")
		})
	})
})
