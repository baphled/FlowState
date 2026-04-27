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
})
