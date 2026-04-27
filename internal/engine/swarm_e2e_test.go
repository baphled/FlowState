package engine_test

import (
	"context"
	"errors"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
)

// e2eStreamer drains a single Done chunk and counts invocations.
func e2eStreamer(calls *atomic.Int32) streaming.Streamer {
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

// buildSmokeWorkerEngine constructs a single worker-engine the smoke
// test fans out to.
func buildSmokeWorkerEngine() *engine.Engine {
	return engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "worker"},
		Manifest: agent.Manifest{
			ID:                "worker",
			Name:              "Worker",
			Instructions:      agent.Instructions{SystemPrompt: "worker"},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
}

// buildSmokeLeadEngine constructs the lead engine the DelegateTool
// uses as its source-agent.
func buildSmokeLeadEngine() *engine.Engine {
	return engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "lead"},
		Manifest: agent.Manifest{
			ID:                "lead",
			Name:              "Lead",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			Delegation:        agent.Delegation{CanDelegate: true},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
}

// smokeManifest builds a swarm manifest the e2e test drives end-to-
// end via NewManifestBuilder. Pinned at fast-no-jitter retry so the
// test does not pay wall-clock backoff on the panic / retryable
// edges in adjacent suites.
func smokeManifest(name, gateKind string) *swarm.Manifest {
	m := swarm.NewManifestBuilder("smoke-swarm").
		WithLead("lead").
		WithMember("worker").
		WithGate(name, gateKind, swarm.LifecyclePostMember, "worker").
		Build()
	return &m
}

var _ = Describe("SwarmEndToEndSmoke", func() {
	BeforeEach(func() {
		swarm.ResetExtGateRegistryForTest()
	})

	Context("with an ext:always-pass gate", func() {
		It("dispatches the worker once and the gate runs once on a passing run", func() {
			var gateCalls atomic.Int32
			err := swarm.RegisterExtGateFunc("always-pass", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
				gateCalls.Add(1)
				return swarm.ExtGateResponse{Pass: true}, nil
			})
			Expect(err).NotTo(HaveOccurred())

			manifest := smokeManifest("g1", "ext:always-pass")
			reg := swarm.NewRegistry()
			reg.Register(manifest)

			lead := buildSmokeLeadEngine()
			worker := buildSmokeWorkerEngine()
			engines := map[string]*engine.Engine{"lead": lead, "worker": worker}
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			var workerCalls atomic.Int32
			streamers := map[string]streaming.Streamer{
				"worker": e2eStreamer(&workerCalls),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithGateRunner(swarm.NewMultiRunner())

			err = delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, []string{"worker"}, "go")

			Expect(err).NotTo(HaveOccurred())
			Expect(workerCalls.Load()).To(Equal(int32(1)))
			Expect(gateCalls.Load()).To(Equal(int32(1)))
		})
	})

	Context("with an ext:always-fail gate", func() {
		It("surfaces *swarm.GateError carrying GateKind ext:always-fail", func() {
			err := swarm.RegisterExtGateFunc("always-fail", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
				return swarm.ExtGateResponse{Pass: false, Reason: "no go"}, nil
			})
			Expect(err).NotTo(HaveOccurred())

			manifest := smokeManifest("g1", "ext:always-fail")
			reg := swarm.NewRegistry()
			reg.Register(manifest)

			lead := buildSmokeLeadEngine()
			worker := buildSmokeWorkerEngine()
			engines := map[string]*engine.Engine{"lead": lead, "worker": worker}
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			streamers := map[string]streaming.Streamer{"worker": e2eStreamer(nil)}
			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithGateRunner(swarm.NewMultiRunner())

			dispatchErr := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, []string{"worker"}, "go")

			Expect(dispatchErr).To(HaveOccurred())
			var ge *swarm.GateError
			Expect(errors.As(dispatchErr, &ge)).To(BeTrue())
			Expect(ge.GateKind).To(Equal("ext:always-fail"))
		})
	})
})
