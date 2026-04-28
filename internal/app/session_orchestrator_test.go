package app_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/swarm"
)

// fakeOrchestratorStreamer captures the agent id + message threaded to
// streaming.Run by the orchestrator. The streamed channel emits a
// single Done chunk so DispatchSwarm wraps up promptly.
type fakeOrchestratorStreamer struct {
	capturedAgentID string
	capturedMessage string
	chunks          []provider.StreamChunk
	err             error
}

func (f *fakeOrchestratorStreamer) Stream(_ context.Context, agentID string, message string) (<-chan provider.StreamChunk, error) {
	f.capturedAgentID = agentID
	f.capturedMessage = message
	if f.err != nil {
		return nil, f.err
	}
	out := make(chan provider.StreamChunk, len(f.chunks)+1)
	for _, c := range f.chunks {
		out <- c
	}
	out <- provider.StreamChunk{Done: true}
	close(out)
	return out, nil
}

// fakeOrchestratorEngine satisfies swarm.DispatchEngine and records
// whether the orchestrator's swarm dispatch lifecycle ran.
type fakeOrchestratorEngine struct {
	contexts      []*swarm.Context
	flushCalls    int
	snapshotCalls int
	restoreCalls  int
}

func (f *fakeOrchestratorEngine) SetSwarmContext(ctx *swarm.Context) {
	f.contexts = append(f.contexts, ctx)
}

func (f *fakeOrchestratorEngine) FlushSwarmLifecycle(_ context.Context) error {
	f.flushCalls++
	return nil
}

func (f *fakeOrchestratorEngine) ManifestSnapshot() any {
	f.snapshotCalls++
	return "pre-dispatch-token"
}

func (f *fakeOrchestratorEngine) RestoreManifest(_ any) {
	f.restoreCalls++
}

// fakeStreamConsumer implements streaming.StreamConsumer minimally.
type fakeStreamConsumer struct {
	chunks []string
	err    error
	done   bool
}

func (f *fakeStreamConsumer) WriteChunk(content string) error {
	f.chunks = append(f.chunks, content)
	return nil
}
func (f *fakeStreamConsumer) WriteError(err error) { f.err = err }
func (f *fakeStreamConsumer) Done()                { f.done = true }

var _ = Describe("SessionOrchestrator", func() {
	var (
		registry *agent.Registry
		swarmReg *swarm.Registry
		streamer *fakeOrchestratorStreamer
		eng      *fakeOrchestratorEngine
		consumer *fakeStreamConsumer
	)

	BeforeEach(func() {
		registry = agent.NewRegistry()
		registry.Register(&agent.Manifest{ID: "executor", Name: "Executor"})
		registry.Register(&agent.Manifest{ID: "Senior-Engineer", Name: "Senior Engineer"})
		registry.Register(&agent.Manifest{ID: "explorer", Name: "Explorer"})

		swarmReg = swarm.NewRegistry()
		swarmReg.Register(&swarm.Manifest{
			SchemaVersion: "1.0.0",
			ID:            "bug-hunt",
			Lead:          "Senior-Engineer",
			Members:       []string{"explorer"},
		})

		streamer = &fakeOrchestratorStreamer{}
		eng = &fakeOrchestratorEngine{}
		consumer = &fakeStreamConsumer{}
	})

	Describe("ProcessUserInput", func() {
		Context("when DefaultAgent resolves to an agent and ScanMentions is false", func() {
			It("streams from that agent without installing a swarm context", func() {
				orch := app.NewSessionOrchestrator(eng, registry, swarmReg, streamer)

				err := orch.ProcessUserInput(context.Background(), app.UserInput{
					Message:      "hello",
					DefaultAgent: "executor",
				}, consumer)

				Expect(err).NotTo(HaveOccurred())
				Expect(streamer.capturedAgentID).To(Equal("executor"))
				Expect(streamer.capturedMessage).To(Equal("hello"))
				// SetSwarmContext is called once — with nil — to keep
				// the engine in single-agent shape.
				Expect(eng.contexts).To(HaveLen(1))
				Expect(eng.contexts[0]).To(BeNil())
				// Symmetric snapshot/restore around the stream.
				Expect(eng.snapshotCalls).To(Equal(1))
				Expect(eng.restoreCalls).To(Equal(1))
				Expect(eng.flushCalls).To(Equal(1))
			})
		})

		Context("when DefaultAgent resolves to a swarm and ScanMentions is false", func() {
			It("streams from the swarm's lead and installs the swarm context", func() {
				orch := app.NewSessionOrchestrator(eng, registry, swarmReg, streamer)

				err := orch.ProcessUserInput(context.Background(), app.UserInput{
					Message:      "trace the auth path",
					DefaultAgent: "bug-hunt",
				}, consumer)

				Expect(err).NotTo(HaveOccurred())
				Expect(streamer.capturedAgentID).To(Equal("Senior-Engineer"))
				Expect(eng.contexts).To(HaveLen(1))
				Expect(eng.contexts[0]).NotTo(BeNil())
				Expect(eng.contexts[0].SwarmID).To(Equal("bug-hunt"))
				Expect(eng.contexts[0].LeadAgent).To(Equal("Senior-Engineer"))
			})
		})

		Context("when ScanMentions is true and the message contains @<swarm-id>", func() {
			It("the @-mention overrides DefaultAgent", func() {
				orch := app.NewSessionOrchestrator(eng, registry, swarmReg, streamer)

				err := orch.ProcessUserInput(context.Background(), app.UserInput{
					Message:      "@bug-hunt please look at the auth module",
					DefaultAgent: "executor",
					ScanMentions: true,
				}, consumer)

				Expect(err).NotTo(HaveOccurred())
				Expect(streamer.capturedAgentID).To(Equal("Senior-Engineer"),
					"swarm @-mention must override DefaultAgent")
				Expect(eng.contexts[0]).NotTo(BeNil())
				Expect(eng.contexts[0].SwarmID).To(Equal("bug-hunt"))
			})
		})

		Context("when ScanMentions is true but only agent @-mentions appear", func() {
			It("falls through to DefaultAgent (agent mentions don't redirect)", func() {
				orch := app.NewSessionOrchestrator(eng, registry, swarmReg, streamer)

				err := orch.ProcessUserInput(context.Background(), app.UserInput{
					Message:      "ask @explorer to look at this",
					DefaultAgent: "executor",
					ScanMentions: true,
				}, consumer)

				Expect(err).NotTo(HaveOccurred())
				Expect(streamer.capturedAgentID).To(Equal("executor"))
				Expect(eng.contexts[0]).To(BeNil())
			})
		})

		Context("when ScanMentions is true and an unknown @-mention appears", func() {
			It("skips it and falls through to DefaultAgent", func() {
				orch := app.NewSessionOrchestrator(eng, registry, swarmReg, streamer)

				err := orch.ProcessUserInput(context.Background(), app.UserInput{
					Message:      "ping @ghost-thing about it",
					DefaultAgent: "executor",
					ScanMentions: true,
				}, consumer)

				Expect(err).NotTo(HaveOccurred())
				Expect(streamer.capturedAgentID).To(Equal("executor"))
			})
		})

		Context("when both DefaultAgent is empty and ScanMentions matches no swarm", func() {
			It("returns the no-target error", func() {
				orch := app.NewSessionOrchestrator(eng, registry, swarmReg, streamer)

				err := orch.ProcessUserInput(context.Background(), app.UserInput{
					Message:      "",
					DefaultAgent: "",
				}, consumer)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no agent or swarm target resolved"))
				// Streamer should NOT have been driven.
				Expect(streamer.capturedAgentID).To(Equal(""))
				Expect(eng.snapshotCalls).To(Equal(0))
			})
		})

		Context("when DefaultAgent is unknown", func() {
			It("returns swarm.NotFoundError without driving the streamer", func() {
				orch := app.NewSessionOrchestrator(eng, registry, swarmReg, streamer)

				err := orch.ProcessUserInput(context.Background(), app.UserInput{
					Message:      "hi",
					DefaultAgent: "ghost",
				}, consumer)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ghost"))
				Expect(streamer.capturedAgentID).To(Equal(""))
			})
		})
	})
})
