package app

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

// spyProvider captures the ChatRequest sent to the provider for assertion in tests.
type spyProvider struct {
	name            string
	capturedRequest *provider.ChatRequest
}

func (s *spyProvider) Name() string { return s.name }
func (s *spyProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	s.capturedRequest = &req
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}
func (s *spyProvider) Chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	s.capturedRequest = &req
	return provider.ChatResponse{}, nil
}
func (s *spyProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, errMockNotImplemented
}
func (s *spyProvider) Models() ([]provider.Model, error) { return nil, nil }

var _ = Describe("Tool wiring integration", func() {
	var (
		executorManifest agent.Manifest
		agentReg         *agent.Registry
		spy              *spyProvider
		providerReg      *provider.Registry
		application      *App
		eng              *engine.Engine
		ensureToolsFn    func(agent.Manifest)
	)

	buildTestEngine := func(manifest agent.Manifest) {
		twc := &toolWiringCallbacks{
			hasTool: func(name string) bool {
				if eng == nil {
					return false
				}
				return eng.HasTool(name)
			},
			ensureTools: func(m agent.Manifest) {
				if ensureToolsFn != nil {
					ensureToolsFn(m)
				}
			},
			schemaRebuilder: func() []provider.Tool {
				if eng == nil {
					return nil
				}
				return eng.ToolSchemas()
			},
		}

		hookChain := buildHookChain(hookChainConfig{
			manifestGetter: func() agent.Manifest {
				if eng != nil {
					return eng.Manifest()
				}
				return executorManifest
			},
			twc: twc,
		})

		eng = engine.New(engine.Config{
			Manifest:      manifest,
			AgentRegistry: agentReg,
			Registry:      providerReg,
			ChatProvider:  spy,
			HookChain:     hookChain,
		})

		application.wireDelegateToolIfEnabled(eng, executorManifest)

		ensureToolsFn = func(m agent.Manifest) {
			application.wireDelegateToolIfEnabled(eng, m)
		}
	}

	BeforeEach(func() {
		executorManifest = agent.Manifest{
			ID:   "executor",
			Name: "Executor",
			Delegation: agent.Delegation{
				CanDelegate: false,
			},
		}

		agentReg = agent.NewRegistry()
		agentReg.Register(&executorManifest)

		spy = &spyProvider{name: "spy"}
		providerReg = provider.NewRegistry()
		providerReg.Register(spy)

		application = &App{
			Registry:         agentReg,
			providerRegistry: providerReg,
		}
	})

	Context("when streaming as planner agent with can_delegate=true", func() {
		BeforeEach(func() {
			plannerManifest := agent.Manifest{
				ID:   "planner",
				Name: "Planner",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			agentReg.Register(&plannerManifest)
			buildTestEngine(executorManifest)
		})

		It("includes the delegate tool in the provider request", func() {
			_, err := eng.Stream(context.Background(), "planner", "hello")
			Expect(err).NotTo(HaveOccurred())

			Expect(spy.capturedRequest).NotTo(BeNil())

			toolNames := make([]string, 0, len(spy.capturedRequest.Tools))
			for _, tool := range spy.capturedRequest.Tools {
				toolNames = append(toolNames, tool.Name)
			}
			Expect(toolNames).To(ContainElement("delegate"))
		})
	})

	Context("when streaming as executor agent with can_delegate=false", func() {
		BeforeEach(func() {
			buildTestEngine(executorManifest)
		})

		It("does not include the delegate tool in the provider request", func() {
			_, err := eng.Stream(context.Background(), "executor", "hello")
			Expect(err).NotTo(HaveOccurred())

			Expect(spy.capturedRequest).NotTo(BeNil())

			toolNames := make([]string, 0, len(spy.capturedRequest.Tools))
			for _, tool := range spy.capturedRequest.Tools {
				toolNames = append(toolNames, tool.Name)
			}
			Expect(toolNames).NotTo(ContainElement("delegate"))
		})

		// P12: non-delegating agents get suggest_delegate as an escape hatch.
		It("includes the suggest_delegate tool in the provider request", func() {
			// Register a router so suggest_delegate has a valid to_agent.
			routerManifest := agent.Manifest{
				ID:   "router",
				Name: "Router",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			agentReg.Register(&routerManifest)
			application.wireSuggestDelegateToolIfDisabled(eng, executorManifest)

			_, err := eng.Stream(context.Background(), "executor", "hello")
			Expect(err).NotTo(HaveOccurred())

			Expect(spy.capturedRequest).NotTo(BeNil())

			toolNames := make([]string, 0, len(spy.capturedRequest.Tools))
			for _, tool := range spy.capturedRequest.Tools {
				toolNames = append(toolNames, tool.Name)
			}
			Expect(toolNames).To(ContainElement("suggest_delegate"))
			Expect(toolNames).NotTo(ContainElement("delegate"))
		})
	})

	// P12: when the manifest restricts capabilities.tools to a fixed list
	// (e.g. real executor.md lists [bash, file, web]), the engine's
	// buildAllowedToolSet must still surface suggest_delegate to the
	// provider — otherwise the escape hatch is invisible to the model.
	Context("when the non-delegating manifest restricts capabilities.tools", func() {
		It("still includes suggest_delegate in the provider request", func() {
			restrictedManifest := agent.Manifest{
				ID:   "restricted-executor",
				Name: "Restricted Executor",
				Capabilities: agent.Capabilities{
					Tools: []string{"bash", "file", "web"},
				},
				Delegation: agent.Delegation{
					CanDelegate: false,
				},
			}
			agentReg.Register(&restrictedManifest)
			routerManifest := agent.Manifest{
				ID:   "router",
				Name: "Router",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			agentReg.Register(&routerManifest)

			buildTestEngine(restrictedManifest)
			application.wireSuggestDelegateToolIfDisabled(eng, restrictedManifest)

			_, err := eng.Stream(context.Background(), "restricted-executor", "hello")
			Expect(err).NotTo(HaveOccurred())

			Expect(spy.capturedRequest).NotTo(BeNil())
			toolNames := make([]string, 0, len(spy.capturedRequest.Tools))
			for _, t := range spy.capturedRequest.Tools {
				toolNames = append(toolNames, t.Name)
			}
			Expect(toolNames).To(ContainElement("suggest_delegate"),
				"suggest_delegate must bypass the manifest tool filter so non-delegating agents always have the escape hatch")
		})
	})
})
