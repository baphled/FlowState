package engine_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("DelegateTool.WithOwnerEngine swarm-context lookup", func() {
	var (
		leadEng     *engine.Engine
		delegateTool *engine.DelegateTool
	)

	BeforeEach(func() {
		providerReg := provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})

		leadEng = engine.New(engine.Config{
			Manifest:      agent.Manifest{ID: "Senior-Engineer", Name: "Lead", Delegation: agent.Delegation{CanDelegate: true}},
			AgentRegistry: agent.NewRegistry(),
			Registry:      providerReg,
			ChatProvider:  &mockProvider{name: "spy"},
		})

		// Targets map intentionally EXCLUDES the lead — mirrors
		// buildDelegateMaps' production behaviour.
		targetEng := engine.New(engine.Config{
			Manifest:      agent.Manifest{ID: "explorer", Name: "Explorer"},
			AgentRegistry: agent.NewRegistry(),
			Registry:      providerReg,
			ChatProvider:  &mockProvider{name: "spy"},
		})
		engines := map[string]*engine.Engine{"explorer": targetEng}

		delegateTool = engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "Senior-Engineer")
	})

	When("the lead's engine has a swarm context set but the targets map excludes the lead", func() {
		It("finds the swarm context via WithOwnerEngine, not via the targets map", func() {
			swarmCtx := &swarm.Context{
				SwarmID:     "bug-hunt",
				LeadAgent:   "Senior-Engineer",
				Members:     []string{"explorer"},
				ChainPrefix: "bug-hunt",
				Gates: []swarm.GateSpec{{
					Name:      "test-gate",
					Kind:      "builtin:result-schema",
					When:      swarm.LifecyclePostMember,
					Target:    "explorer",
					SchemaRef: "evidence-bundle-v1",
					OutputKey: "codebase-findings",
				}},
			}
			leadEng.SetSwarmContext(swarmCtx)

			// Without WithOwnerEngine: dispatch silently no-ops because
			// the targets map doesn't contain "Senior-Engineer".
			err := delegateTool.DispatchPostMemberGatesForTest(context.Background(), "explorer")
			Expect(err).NotTo(HaveOccurred(),
				"baseline: without owner engine, no gate runs, no error")

			// With WithOwnerEngine: the swarm context resolves through
			// the lead's engine, the gate fires, and the schema gate
			// finds NO payload at the expected coord-store key
			// (because the test didn't seed one) so it fails — proving
			// the gate actually ran.
			delegateTool.WithOwnerEngine(leadEng)
			err = delegateTool.DispatchPostMemberGatesForTest(context.Background(), "explorer")
			// The gate would fire, but the runner is nil in this
			// fixture so it'd no-op early. The point is that the
			// activeSwarmContext lookup now succeeds — instrument by
			// installing a fake runner.
			_ = err

			// Now install a fake runner that records calls; the gate
			// should fire and the recorder should see one invocation.
			recorder := &fakeRunner{}
			delegateTool.WithGateRunner(recorder)
			err = delegateTool.DispatchPostMemberGatesForTest(context.Background(), "explorer")
			Expect(err).NotTo(HaveOccurred(),
				"recorder reports pass; we only care that it WAS called")
			Expect(recorder.calls).To(HaveLen(1),
				"with WithOwnerEngine, the gate runner sees the dispatch — pre-fix this was zero")
			Expect(recorder.calls[0].Name).To(Equal("test-gate"))
		})
	})

	When("the lead's engine has no swarm context", func() {
		It("falls through quietly so non-swarm callers keep working", func() {
			delegateTool.WithOwnerEngine(leadEng)
			recorder := &fakeRunner{}
			delegateTool.WithGateRunner(recorder)

			err := delegateTool.DispatchPostMemberGatesForTest(context.Background(), "explorer")

			Expect(err).NotTo(HaveOccurred())
			Expect(recorder.calls).To(BeEmpty(),
				"no swarm context → no gates fire (the historical no-op behaviour for non-swarm dispatches)")
		})
	})
})

type fakeRunner struct {
	calls []swarm.GateSpec
	err   error
}

func (r *fakeRunner) Run(_ context.Context, gate swarm.GateSpec, _ swarm.GateArgs) error {
	r.calls = append(r.calls, gate)
	return r.err
}

var _ = errors.New // silence unused-import in non-builder configs
