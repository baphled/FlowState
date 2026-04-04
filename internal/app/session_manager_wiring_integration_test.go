package app

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
)

type stubStreamer struct{}

func (s *stubStreamer) Stream(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

var _ streaming.Streamer = (*stubStreamer)(nil)

var _ = Describe("Session manager wiring integration", Label("integration"), func() {
	var (
		agentReg    *agent.Registry
		providerReg *provider.Registry
		application *App
	)

	BeforeEach(func() {
		agentReg = agent.NewRegistry()
		providerReg = provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})

		application = &App{
			Registry:         agentReg,
			providerRegistry: providerReg,
		}
	})

	Context("when session manager is wired to a delegating agent", func() {
		var (
			delegatingManifest agent.Manifest
			targetManifest     agent.Manifest
			eng                *engine.Engine
		)

		BeforeEach(func() {
			delegatingManifest = agent.Manifest{
				ID:   "coordinator",
				Name: "Coordinator",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}

			targetManifest = agent.Manifest{
				ID:   "worker",
				Name: "Worker",
			}

			agentReg.Register(&delegatingManifest)
			agentReg.Register(&targetManifest)

			application.sessionManager = session.NewManager(&stubStreamer{})

			eng = engine.New(engine.Config{
				Manifest:      delegatingManifest,
				AgentRegistry: agentReg,
				Registry:      providerReg,
				ChatProvider:  &mockProvider{name: "spy"},
			})

			application.wireDelegateToolIfEnabled(eng, delegatingManifest)
		})

		It("stores the background manager on the app after wiring", func() {
			Expect(application.backgroundManager).NotTo(BeNil())
		})

		It("wires the delegate tool onto the engine", func() {
			Expect(eng.HasTool("delegate")).To(BeTrue())
		})
	})

	Context("when session manager is nil", func() {
		var (
			delegatingManifest agent.Manifest
			targetManifest     agent.Manifest
			eng                *engine.Engine
		)

		BeforeEach(func() {
			delegatingManifest = agent.Manifest{
				ID:   "coordinator-nil-mgr",
				Name: "Coordinator",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}

			targetManifest = agent.Manifest{
				ID:   "worker-nil-mgr",
				Name: "Worker",
			}

			agentReg.Register(&delegatingManifest)
			agentReg.Register(&targetManifest)

			application.sessionManager = nil

			eng = engine.New(engine.Config{
				Manifest:      delegatingManifest,
				AgentRegistry: agentReg,
				Registry:      providerReg,
				ChatProvider:  &mockProvider{name: "spy"},
			})
		})

		It("does not panic when wiring a delegating agent without a session manager", func() {
			Expect(func() {
				application.wireDelegateToolIfEnabled(eng, delegatingManifest)
			}).NotTo(Panic())
		})
	})

	Context("when agent does not delegate", func() {
		var (
			nonDelegatingManifest agent.Manifest
			eng                   *engine.Engine
		)

		BeforeEach(func() {
			nonDelegatingManifest = agent.Manifest{
				ID:   "standalone",
				Name: "Standalone",
				Delegation: agent.Delegation{
					CanDelegate: false,
				},
			}

			agentReg.Register(&nonDelegatingManifest)

			application.sessionManager = session.NewManager(&stubStreamer{})

			eng = engine.New(engine.Config{
				Manifest:      nonDelegatingManifest,
				AgentRegistry: agentReg,
				Registry:      providerReg,
				ChatProvider:  &mockProvider{name: "spy"},
			})

			application.wireDelegateToolIfEnabled(eng, nonDelegatingManifest)
		})

		It("does not wire a delegate tool", func() {
			Expect(eng.HasTool("delegate")).To(BeFalse())
		})

		It("does not set the background manager", func() {
			Expect(application.backgroundManager).To(BeNil())
		})
	})
})
