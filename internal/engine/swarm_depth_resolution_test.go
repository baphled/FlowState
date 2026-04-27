package engine_test

import (
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/delegation"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/swarm"
)

// handoffAtDepth builds a delegation.Handoff metadata-tagged with the
// requested depth so checkSpawnLimits sees the right value.
func handoffAtDepth(n int) *delegation.Handoff {
	return &delegation.Handoff{
		Metadata: map[string]string{"depth": strconv.Itoa(n)},
	}
}

// minimalDelegateTool returns a DelegateTool with default spawn
// limits and the given swarm registry installed.
func minimalDelegateTool(reg *swarm.Registry) *engine.DelegateTool {
	return engine.NewDelegateTool(map[string]*engine.Engine{}, agent.Delegation{CanDelegate: true}, "lead").
		WithSwarmRegistry(reg)
}

// codegenManifest returns a swarm manifest with SwarmType=codegen so
// ResolveMaxDepth picks the addendum-A4 depth-16 default.
func codegenManifest() *swarm.Manifest {
	return &swarm.Manifest{
		SchemaVersion: "1.0.0",
		ID:            "codegen-swarm",
		Lead:          "lead",
		Members:       []string{"worker"},
		SwarmType:     swarm.SwarmTypeCodegen,
	}
}

// pinnedDepthManifest returns a manifest with an explicit MaxDepth.
func pinnedDepthManifest(depth int) *swarm.Manifest {
	return &swarm.Manifest{
		SchemaVersion: "1.0.0",
		ID:            "pinned-depth-swarm",
		Lead:          "lead",
		Members:       []string{"worker"},
		MaxDepth:      depth,
	}
}

// installSwarmCtxOnLead wires a swarm.Context onto the lead engine so
// checkSpawnLimits's d.activeSwarmContext() lookup succeeds.
func installSwarmCtxOnLead(dt *engine.DelegateTool, m *swarm.Manifest) {
	lead := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "lead"},
		Manifest: agent.Manifest{
			ID:                "lead",
			Name:              "Lead",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			Delegation:        agent.Delegation{CanDelegate: true},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	swarmCtx := swarm.NewContext(m.ID, m)
	lead.SetSwarmContext(&swarmCtx)
	engines := map[string]*engine.Engine{"lead": lead}
	dt.SetEnginesForTest(engines)
}

var _ = Describe("SwarmDepthResolution", func() {
	Context("with a codegen swarm context", func() {
		It("permits a depth-10 handoff that the historical default would have rejected", func() {
			reg := swarm.NewRegistry()
			manifest := codegenManifest()
			reg.Register(manifest)
			dt := minimalDelegateTool(reg)
			installSwarmCtxOnLead(dt, manifest)

			err := dt.CheckSpawnLimitsForTest(handoffAtDepth(10))

			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("with an explicit MaxDepth pinned on the manifest", func() {
		It("rejects a handoff at the pinned ceiling", func() {
			reg := swarm.NewRegistry()
			manifest := pinnedDepthManifest(7)
			reg.Register(manifest)
			dt := minimalDelegateTool(reg)
			installSwarmCtxOnLead(dt, manifest)

			err := dt.CheckSpawnLimitsForTest(handoffAtDepth(8))

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("depth limit"))
		})
	})

	Context("with no active swarm context", func() {
		It("falls back to the historical SpawnLimits.MaxDepth=5", func() {
			dt := minimalDelegateTool(nil)

			err := dt.CheckSpawnLimitsForTest(handoffAtDepth(5))

			Expect(err).To(HaveOccurred())
		})
	})
})
