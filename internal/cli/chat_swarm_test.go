package cli_test

import (
	"bytes"
	"errors"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/swarm"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("chat command post-swarm gate dispatch (T-swarm-3)", func() {
	var out *bytes.Buffer
	chatCmd := func(testApp *app.App, args ...string) error {
		root := cli.NewRootCmd(testApp)
		root.SetOut(out)
		root.SetErr(out)
		root.SetArgs(args)
		return root.Execute()
	}
	BeforeEach(func() {
		out = new(bytes.Buffer)
	})

	It("flushes swarm-level post gates after the chat message stream completes", func() {
		testApp := createRunTestApp([]provider.StreamChunk{{Content: "ok", Done: true}}, nil)
		runner := &recordingGateRunner{}
		wireChatSwarmDelegateTool(testApp, runner, []swarm.GateSpec{postSwarmGate()})

		err := chatCmd(testApp, "chat", "--message", "hello", "--agent", "test-swarm")

		Expect(err).NotTo(HaveOccurred())
		Expect(runner.calls).To(HaveLen(1),
			"post-swarm gate must fire when the chat message stream completes")
		Expect(runner.calls[0].When).To(Equal(swarm.LifecyclePostSwarm))
	})

	It("returns the gate failure when the post-swarm runner reports an error", func() {
		testApp := createRunTestApp([]provider.StreamChunk{{Content: "ok", Done: true}}, nil)
		runner := &failingGateRunner{
			err: &swarm.GateError{
				GateName: "post-swarm-aggregate",
				GateKind: "builtin:result-schema",
				When:     swarm.LifecyclePostSwarm,
				Reason:   "aggregate validation failed",
			},
		}
		wireChatSwarmDelegateTool(testApp, runner, []swarm.GateSpec{postSwarmGate()})

		err := chatCmd(testApp, "chat", "--message", "hello", "--agent", "test-swarm")

		Expect(err).To(HaveOccurred())
		var gateErr *swarm.GateError
		Expect(errors.As(err, &gateErr)).To(BeTrue())
		Expect(gateErr.Reason).To(ContainSubstring("aggregate validation failed"))
	})
})

func wireChatSwarmDelegateTool(testApp *app.App, runner swarm.GateRunner, gates []swarm.GateSpec) {
	testApp.Registry = agent.NewRegistry()
	testApp.Registry.Register(&agent.Manifest{
		ID:           "worker",
		Name:         "Worker",
		Instructions: agent.Instructions{SystemPrompt: "work"},
	})
	swarmReg := swarm.NewRegistry()
	swarmReg.Register(&swarm.Manifest{
		ID:      "test-swarm",
		Lead:    "worker",
		Members: []string{"worker"},
		Harness: swarm.HarnessConfig{Gates: gates},
		Context: swarm.ContextConfig{ChainPrefix: "test"},
	})
	testApp.SwarmRegistry = swarmReg
	dt := engine.NewDelegateToolWithBackground(
		map[string]*engine.Engine{"worker": testApp.Engine},
		agent.Delegation{CanDelegate: true},
		"worker",
		nil,
		coordination.NewMemoryStore(),
	).WithGateRunner(runner)
	testApp.Engine.AddTool(dt)
}
