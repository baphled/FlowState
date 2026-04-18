package app

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// Phase 12 — the suggest_delegate tool is the inverse of delegate: it is
// wired into engines for agents whose manifests set can_delegate:false, so
// the model has a legitimate escape hatch when asked to reach @<agent>.
// An agent has delegate OR suggest_delegate, never both.
var _ = Describe("wireSuggestDelegateToolIfDisabled (P12)", func() {
	var (
		application *App
		providerReg *provider.Registry
	)

	BeforeEach(func() {
		application = &App{
			Registry: agent.NewRegistry(),
		}
		providerReg = provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "anthropic"})
		application.providerRegistry = providerReg
	})

	Context("when the agent has can_delegate=false", func() {
		var (
			executorManifest agent.Manifest
			executorEngine   *engine.Engine
		)

		BeforeEach(func() {
			executorManifest = agent.Manifest{
				ID:   "executor",
				Name: "Executor",
				Delegation: agent.Delegation{
					CanDelegate: false,
				},
			}
			routerManifest := agent.Manifest{
				ID:   "router",
				Name: "Router",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			application.Registry.Register(&executorManifest)
			application.Registry.Register(&routerManifest)

			executorEngine = engine.New(engine.Config{
				Manifest:      executorManifest,
				AgentRegistry: application.Registry,
				Registry:      providerReg,
				Tools:         []tool.Tool{&mockTool{name: "bash"}},
			})
		})

		It("registers the suggest_delegate tool", func() {
			application.wireSuggestDelegateToolIfDisabled(executorEngine, executorManifest)
			Expect(executorEngine.HasTool("suggest_delegate")).To(BeTrue())
		})

		It("does not register the delegate tool", func() {
			application.wireSuggestDelegateToolIfDisabled(executorEngine, executorManifest)
			Expect(executorEngine.HasTool("delegate")).To(BeFalse())
		})
	})

	Context("when the agent has can_delegate=true", func() {
		It("does not register the suggest_delegate tool", func() {
			coordinatorManifest := agent.Manifest{
				ID:   "coordinator",
				Name: "Coordinator",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			application.Registry.Register(&coordinatorManifest)

			coordinatorEngine := engine.New(engine.Config{
				Manifest:      coordinatorManifest,
				AgentRegistry: application.Registry,
				Registry:      providerReg,
				Tools:         []tool.Tool{&mockTool{name: "test"}},
			})

			application.wireSuggestDelegateToolIfDisabled(coordinatorEngine, coordinatorManifest)
			Expect(coordinatorEngine.HasTool("suggest_delegate")).To(BeFalse())
		})
	})
})
