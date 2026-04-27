package engine_test

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/swarm"
)

// extGateSwarmContext builds a swarm.Context whose Gates slice
// includes a single ext: post-member gate targeting the named member.
func extGateSwarmContext(swarmID, gateKind, target string) swarm.Context {
	return swarm.Context{
		SwarmID:   swarmID,
		LeadAgent: "lead",
		Members:   []string{target},
		Gates: []swarm.GateSpec{{
			Name:   "g1",
			Kind:   gateKind,
			When:   swarm.LifecyclePostMember,
			Target: target,
		}},
		ChainPrefix: swarmID,
	}
}

var _ = Describe("SwarmExtGateRouting", func() {
	BeforeEach(func() {
		swarm.ResetExtGateRegistryForTest()
	})

	Context("when the manifest names an ext gate registered via RegisterExtGateFunc", func() {
		It("dispatches through the public RunGate path and observes a pass", func() {
			var calls atomic.Int32
			err := swarm.RegisterExtGateFunc("echo-pass", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
				calls.Add(1)
				return swarm.ExtGateResponse{Pass: true}, nil
			})
			Expect(err).NotTo(HaveOccurred())

			runner := swarm.NewMultiRunner()
			gateErr := runner.Run(context.Background(), swarm.GateSpec{
				Name: "g1",
				Kind: "ext:echo-pass",
				When: swarm.LifecyclePostMember,
			}, swarm.GateArgs{SwarmID: "s", MemberID: "m"})

			Expect(gateErr).NotTo(HaveOccurred())
			Expect(calls.Load()).To(Equal(int32(1)))
		})

		It("surfaces *swarm.GateError when the registered ext gate returns Pass:false", func() {
			err := swarm.RegisterExtGateFunc("echo-fail", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
				return swarm.ExtGateResponse{Pass: false, Reason: "no go"}, nil
			})
			Expect(err).NotTo(HaveOccurred())

			runner := swarm.NewMultiRunner()
			gateErr := runner.Run(context.Background(), swarm.GateSpec{
				Name: "g2",
				Kind: "ext:echo-fail",
				When: swarm.LifecyclePostMember,
			}, swarm.GateArgs{SwarmID: "s", MemberID: "m"})

			Expect(gateErr).To(HaveOccurred())
			var ge *swarm.GateError
			Expect(errors.As(gateErr, &ge)).To(BeTrue())
			Expect(ge.GateKind).To(Equal("ext:echo-fail"))
		})

		It("times out the gate when Timeout is set and the func sleeps past it", func() {
			err := swarm.RegisterExtGateFunc("slow-gate", func(ctx context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
				select {
				case <-time.After(500 * time.Millisecond):
					return swarm.ExtGateResponse{Pass: true}, nil
				case <-ctx.Done():
					return swarm.ExtGateResponse{}, ctx.Err()
				}
			})
			Expect(err).NotTo(HaveOccurred())

			report := swarm.Dispatch(context.Background(), swarm.NewMultiRunner(), []swarm.GateSpec{{
				Name:    "g3",
				Kind:    "ext:slow-gate",
				When:    swarm.LifecyclePostMember,
				Timeout: 50 * time.Millisecond,
			}}, swarm.GateArgs{SwarmID: "s", MemberID: "m"})

			Expect(report.Halted).To(BeTrue())
			var ge *swarm.GateError
			Expect(errors.As(report.Err, &ge)).To(BeTrue())
			Expect(errors.Is(report.Err, context.DeadlineExceeded)).To(BeTrue())
		})
	})

	Context("when an ext gate fires through the engine's post-member dispatcher", func() {
		It("routes via runner.Run for the matching member and the func runs once", func() {
			var calls atomic.Int32
			err := swarm.RegisterExtGateFunc("engine-pass", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
				calls.Add(1)
				return swarm.ExtGateResponse{Pass: true}, nil
			})
			Expect(err).NotTo(HaveOccurred())

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
			engines := map[string]*engine.Engine{"lead": lead}
			swarmCtx := extGateSwarmContext("ext-route-swarm", "ext:engine-pass", "qa-agent")
			lead.SetSwarmContext(&swarmCtx)

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithGateRunner(swarm.NewMultiRunner())

			gateErr := delegateTool.DispatchPostMemberGatesForTest(context.Background(), "qa-agent")

			Expect(gateErr).NotTo(HaveOccurred())
			Expect(calls.Load()).To(Equal(int32(1)))
		})
	})
})
