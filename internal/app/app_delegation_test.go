package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// mockProvider is a simple mock implementation of provider.Provider for testing.
type mockProvider struct {
	name string
}

var errMockNotImplemented = errors.New("mock not implemented")

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errMockNotImplemented
}
func (m *mockProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (m *mockProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, errMockNotImplemented
}
func (m *mockProvider) Models() ([]provider.Model, error) { return nil, nil }

// mockTool is a simple mock tool for testing.
type mockTool struct {
	name string
}

func (m *mockTool) Name() string        { return m.name }
func (m *mockTool) Description() string { return "mock tool" }
func (m *mockTool) Schema() tool.Schema { return tool.Schema{} }
func (m *mockTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	return tool.Result{}, nil
}

// findDelegateTool extracts the DelegateTool from an engine for testing.
func findDelegateTool(eng *engine.Engine) *engine.DelegateTool {
	if !eng.HasTool("delegate") {
		return nil
	}
	return &engine.DelegateTool{}
}

var _ = Describe("wireDelegateToolIfEnabled", func() {
	var (
		application *App
		providerReg *provider.Registry
	)

	BeforeEach(func() {
		application = &App{
			Registry: agent.NewRegistry(),
		}
	})

	Context("when coordinator has can_delegate=true", func() {
		var (
			coordinatorManifest agent.Manifest
			coordinatorEngine   *engine.Engine
		)

		BeforeEach(func() {
			coordinatorManifest = agent.Manifest{
				ID:   "coordinator",
				Name: "Coordinator",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}

			explorerManifest := agent.Manifest{
				ID:   "explorer",
				Name: "Explorer Agent",
				Capabilities: agent.Capabilities{
					Tools: []string{"read", "bash"},
				},
			}

			analystManifest := agent.Manifest{
				ID:   "analyst",
				Name: "Analyst Agent",
				Capabilities: agent.Capabilities{
					Tools: []string{"read", "bash", "write"},
				},
			}

			application.Registry.Register(&coordinatorManifest)
			application.Registry.Register(&explorerManifest)
			application.Registry.Register(&analystManifest)

			providerReg = provider.NewRegistry()
			providerReg.Register(&mockProvider{name: "anthropic"})
			providerReg.Register(&mockProvider{name: "ollama"})
			providerReg.Register(&mockProvider{name: "openai"})
			application.providerRegistry = providerReg

			coordinatorEngine = engine.New(engine.Config{
				Manifest:      coordinatorManifest,
				AgentRegistry: application.Registry,
				Registry:      providerReg,
				Tools:         []tool.Tool{&mockTool{name: "test"}},
			})
		})

		It("creates isolated engines for each target", func() {
			application.wireDelegateToolIfEnabled(coordinatorEngine, coordinatorManifest)

			Expect(coordinatorEngine.HasTool("delegate")).To(BeTrue())

			delegateTool := findDelegateTool(coordinatorEngine)
			Expect(delegateTool).NotTo(BeNil())
		})

		It("preserves the coordinator manifest after wiring", func() {
			explorerManifest := agent.Manifest{
				ID:   "explorer-single",
				Name: "Explorer Agent Single",
				Capabilities: agent.Capabilities{
					Tools: []string{"read", "bash"},
				},
			}

			singleApp := &App{
				Registry: agent.NewRegistry(),
			}
			singleProviderReg := provider.NewRegistry()
			singleProviderReg.Register(&mockProvider{name: "ollama"})
			singleApp.providerRegistry = singleProviderReg

			singleCoordManifest := agent.Manifest{
				ID:   "coordinator",
				Name: "Coordinator",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			singleApp.Registry.Register(&singleCoordManifest)
			singleApp.Registry.Register(&explorerManifest)

			singleEngine := engine.New(engine.Config{
				Manifest:      singleCoordManifest,
				AgentRegistry: singleApp.Registry,
				Registry:      singleProviderReg,
				Tools:         []tool.Tool{&mockTool{name: "test"}},
			})

			singleApp.wireDelegateToolIfEnabled(singleEngine, singleCoordManifest)

			coordinatorManifestAfter := singleEngine.Manifest()
			Expect(coordinatorManifestAfter.ID).To(Equal("coordinator"))
			Expect(coordinatorManifestAfter.Name).To(Equal("Coordinator"))
		})

		It("preserves coordinator state after delegation setup", func() {
			explorerManifest := agent.Manifest{
				ID:   "explorer-state",
				Name: "Explorer Agent",
			}

			stateApp := &App{
				Registry: agent.NewRegistry(),
			}
			stateProviderReg := provider.NewRegistry()
			stateProviderReg.Register(&mockProvider{name: "anthropic"})
			stateProviderReg.Register(&mockProvider{name: "ollama"})
			stateApp.providerRegistry = stateProviderReg

			stateCoordManifest := agent.Manifest{
				ID:   "coordinator",
				Name: "Coordinator",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			stateApp.Registry.Register(&stateCoordManifest)
			stateApp.Registry.Register(&explorerManifest)

			stateEngine := engine.New(engine.Config{
				Manifest:      stateCoordManifest,
				AgentRegistry: stateApp.Registry,
				Registry:      stateProviderReg,
				Tools:         []tool.Tool{&mockTool{name: "test"}},
			})

			stateApp.wireDelegateToolIfEnabled(stateEngine, stateCoordManifest)

			coordinatorManifestAfter := stateEngine.Manifest()
			Expect(coordinatorManifestAfter.ID).To(Equal("coordinator"))
			Expect(coordinatorManifestAfter.Name).To(Equal("Coordinator"))
			Expect(stateEngine.HasTool("delegate")).To(BeTrue())
		})

		It("delegate engines inherit the coordinator's model preference", func() {
			explorerManifest := agent.Manifest{
				ID:                "explorer-pref",
				Name:              "Explorer Agent",
				ContextManagement: agent.DefaultContextManagement(),
			}

			prefApp := &App{
				Registry:        agent.NewRegistry(),
				defaultProvider: &mockProvider{name: "anthropic"},
			}
			prefProviderReg := provider.NewRegistry()
			prefProviderReg.Register(&mockProvider{name: "anthropic"})
			prefApp.providerRegistry = prefProviderReg

			prefCoordManifest := agent.Manifest{
				ID:   "coordinator",
				Name: "Coordinator",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			prefApp.Registry.Register(&prefCoordManifest)
			prefApp.Registry.Register(&explorerManifest)

			prefEngine := engine.New(engine.Config{
				Manifest:      prefCoordManifest,
				AgentRegistry: prefApp.Registry,
				Registry:      prefProviderReg,
				Tools:         []tool.Tool{&mockTool{name: "test"}},
			})
			prefEngine.SetModelPreference("anthropic", "claude-sonnet-4")

			prefApp.wireDelegateToolIfEnabled(prefEngine, prefCoordManifest)

			delegateTool, found := prefEngine.GetDelegateTool()
			Expect(found).To(BeTrue())

			engines := delegateTool.Engines()
			Expect(engines).To(HaveKey("explorer-pref"))

			explorerEngine := engines["explorer-pref"]
			Expect(explorerEngine.LastModel()).To(Equal("claude-sonnet-4"))
			Expect(explorerEngine.LastProvider()).To(Equal("anthropic"))
		})

		It("wires the agent registry for name-based resolution", func() {
			explorerManifest := agent.Manifest{
				ID:   "explorer-reg",
				Name: "Explorer",
				Capabilities: agent.Capabilities{
					CapabilityDescription: "explores systems",
				},
			}

			regApp := &App{
				Registry: agent.NewRegistry(),
			}
			regProviderReg := provider.NewRegistry()
			regProviderReg.Register(&mockProvider{name: "ollama"})
			regApp.providerRegistry = regProviderReg

			regCoordManifest := agent.Manifest{
				ID:   "coordinator",
				Name: "Coordinator",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			regApp.Registry.Register(&regCoordManifest)
			regApp.Registry.Register(&explorerManifest)

			regEngine := engine.New(engine.Config{
				Manifest:      regCoordManifest,
				AgentRegistry: regApp.Registry,
				Registry:      regProviderReg,
			})

			regApp.wireDelegateToolIfEnabled(regEngine, regCoordManifest)

			Expect(regEngine.HasTool("delegate")).To(BeTrue())
			delegateTool, found := regEngine.GetDelegateTool()
			Expect(found).To(BeTrue())

			_, err := delegateTool.ResolveByNameOrAlias("explorer-reg")
			Expect(err).NotTo(HaveOccurred())
		})

		It("wires embedding discovery when Ollama provider is available", func() {
			explorerManifest := agent.Manifest{
				ID:   "explorer-embed",
				Name: "Explorer",
				Capabilities: agent.Capabilities{
					CapabilityDescription: "explores and investigates systems",
				},
			}

			embedApp := &App{
				Registry: agent.NewRegistry(),
			}
			embedProviderReg := provider.NewRegistry()
			embedProviderReg.Register(&mockProvider{name: "ollama"})
			embedApp.providerRegistry = embedProviderReg
			embedApp.ollamaProvider = nil

			embedCoordManifest := agent.Manifest{
				ID:   "coordinator",
				Name: "Coordinator",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			embedApp.Registry.Register(&embedCoordManifest)
			embedApp.Registry.Register(&explorerManifest)

			embedEngine := engine.New(engine.Config{
				Manifest:      embedCoordManifest,
				AgentRegistry: embedApp.Registry,
				Registry:      embedProviderReg,
			})

			embedApp.wireDelegateToolIfEnabled(embedEngine, embedCoordManifest)

			Expect(embedEngine.HasTool("delegate")).To(BeTrue())
			delegateTool, found := embedEngine.GetDelegateTool()
			Expect(found).To(BeTrue())
			Expect(delegateTool.HasEmbeddingDiscovery()).To(BeTrue())
		})
	})

	Context("when agent has can_delegate=false", func() {
		It("does not wire the delegate tool", func() {
			noDelegationManifest := agent.Manifest{
				ID:   "standalone",
				Name: "Standalone Agent",
				Delegation: agent.Delegation{
					CanDelegate: false,
				},
			}

			application.Registry.Register(&noDelegationManifest)

			providerReg = provider.NewRegistry()
			application.providerRegistry = providerReg

			testEngine := engine.New(engine.Config{
				Manifest:      noDelegationManifest,
				AgentRegistry: application.Registry,
				Registry:      providerReg,
				Tools:         []tool.Tool{&mockTool{name: "test"}},
			})

			application.wireDelegateToolIfEnabled(testEngine, noDelegationManifest)

			Expect(testEngine.HasTool("delegate")).To(BeFalse())
		})
	})

	Describe("createDelegateEngine", func() {
		It("creates an engine with a chat provider configured", func() {
			delegateApp := &App{
				Registry:        agent.NewRegistry(),
				defaultProvider: &mockProvider{name: "anthropic"},
			}

			explorerManifest := agent.Manifest{
				ID:                "explorer-chat",
				Name:              "Explorer Agent",
				ContextManagement: agent.DefaultContextManagement(),
			}

			delegateApp.Registry.Register(&explorerManifest)

			delegateProviderReg := provider.NewRegistry()
			delegateProviderReg.Register(&mockProvider{name: "ollama"})
			delegateApp.providerRegistry = delegateProviderReg

			coordinationStore := coordination.NewMemoryStore()

			delegateEngine, _ := delegateApp.createDelegateEngine(explorerManifest, coordinationStore, nil)
			Expect(delegateEngine).NotTo(BeNil())

			_, err := delegateEngine.Stream(context.Background(), "", "hello")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("no provider available"))
		})

		It("returns an isolated engine with the target manifest", func() {
			isolatedApp := &App{
				Registry: agent.NewRegistry(),
			}

			explorerManifest := agent.Manifest{
				ID:   "explorer-isolated",
				Name: "Explorer Agent",
				Capabilities: agent.Capabilities{
					Tools: []string{"read", "bash"},
				},
			}

			isolatedApp.Registry.Register(&explorerManifest)

			isolatedProviderReg := provider.NewRegistry()
			isolatedProviderReg.Register(&mockProvider{name: "ollama"})
			isolatedApp.providerRegistry = isolatedProviderReg

			coordinationStore := coordination.NewMemoryStore()

			delegateEngine, _ := isolatedApp.createDelegateEngine(explorerManifest, coordinationStore, nil)

			Expect(delegateEngine).NotTo(BeNil())
			manifest := delegateEngine.Manifest()
			Expect(manifest.ID).To(Equal("explorer-isolated"))
			Expect(manifest.Name).To(Equal("Explorer Agent"))
		})

		It("shares the provided event bus with the delegate engine", func() {
			busApp := &App{
				Registry:        agent.NewRegistry(),
				defaultProvider: &mockProvider{name: "anthropic"},
			}
			busProviderReg := provider.NewRegistry()
			busProviderReg.Register(&mockProvider{name: "anthropic"})
			busApp.providerRegistry = busProviderReg

			explorerManifest := agent.Manifest{
				ID:                "explorer-bus",
				Name:              "Explorer Agent",
				ContextManagement: agent.DefaultContextManagement(),
			}
			busApp.Registry.Register(&explorerManifest)

			parentBus := eventbus.NewEventBus()
			coordinationStore := coordination.NewMemoryStore()

			delegateEngine, _ := busApp.createDelegateEngine(explorerManifest, coordinationStore, parentBus)
			Expect(delegateEngine.EventBus()).To(BeIdenticalTo(parentBus))
		})

		Context("when the delegate manifest declares always_active_skills", func() {
			// Pins the symmetry with root-engine creation (see
			// app.go:821 where createEngine wires Skills from
			// loadSkills). Without this, manifest-declared
			// always_active_skills silently drop on delegation —
			// session loaded_skills omits them and the
			// harness-validator reports "manifest
			// always_active_skills not loaded in session".
			// Mirrors the predecessor fix for root-engine
			// (see Obsidian: "Engine Skills Field Silent Drop").
			//
			// Pending (PIt) for the RED commit to keep make check
			// green; the GREEN commit flips to It alongside the
			// app.go createDelegateEngine Skills-wiring fix.
			PIt("loads them into the delegate engine's LoadedSkills", func() {
				// Arrange: a skill directory with a single
				// known skill that the manifest references.
				skillDir, err := os.MkdirTemp("", "delegate-skills-*")
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { os.RemoveAll(skillDir) })

				memoryKeeperDir := filepath.Join(skillDir, "memory-keeper")
				Expect(os.MkdirAll(memoryKeeperDir, 0o755)).To(Succeed())
				skillBody := "---\nname: memory-keeper\n---\nRemember things."
				Expect(os.WriteFile(filepath.Join(memoryKeeperDir, "SKILL.md"), []byte(skillBody), 0o600)).To(Succeed())

				skillsApp := &App{
					Registry:        agent.NewRegistry(),
					defaultProvider: &mockProvider{name: "anthropic"},
					Config: &config.AppConfig{
						SkillDir: skillDir,
					},
				}
				skillsProviderReg := provider.NewRegistry()
				skillsProviderReg.Register(&mockProvider{name: "anthropic"})
				skillsApp.providerRegistry = skillsProviderReg

				delegateManifest := agent.Manifest{
					ID:   "plan-writer",
					Name: "Plan Writer",
					Capabilities: agent.Capabilities{
						AlwaysActiveSkills: []string{"memory-keeper"},
					},
					ContextManagement: agent.DefaultContextManagement(),
				}
				skillsApp.Registry.Register(&delegateManifest)

				coordinationStore := coordination.NewMemoryStore()

				// Act.
				delegateEngine, _ := skillsApp.createDelegateEngine(delegateManifest, coordinationStore, nil)

				// Assert: the manifest's always_active_skills
				// must reach the delegate engine. LoadedSkills
				// is what the CLI/TUI persist into the session
				// sidecar's loaded_skills field — if this is
				// empty, the session reports the bug.
				loaded := delegateEngine.LoadedSkills()
				names := make([]string, 0, len(loaded))
				for i := range loaded {
					names = append(names, loaded[i].Name)
				}
				Expect(names).To(ContainElement("memory-keeper"))
			})
		})
	})

	Describe("delegate engine event bus sharing", func() {
		It("delivers delegate engine events to the coordinator's bus subscribers", func() {
			busApp := &App{
				Registry:        agent.NewRegistry(),
				defaultProvider: &mockProvider{name: "anthropic"},
			}
			busProviderReg := provider.NewRegistry()
			busProviderReg.Register(&mockProvider{name: "anthropic"})
			busApp.providerRegistry = busProviderReg

			explorerManifest := agent.Manifest{
				ID:   "explorer-events",
				Name: "Explorer Agent",
			}
			coordManifest := agent.Manifest{
				ID:   "coordinator-events",
				Name: "Coordinator",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			busApp.Registry.Register(&coordManifest)
			busApp.Registry.Register(&explorerManifest)

			coordinatorEngine := engine.New(engine.Config{
				Manifest:      coordManifest,
				AgentRegistry: busApp.Registry,
				Registry:      busProviderReg,
				Tools:         []tool.Tool{&mockTool{name: "test"}},
			})

			var mu sync.Mutex
			var receivedTopics []string
			coordinatorEngine.EventBus().Subscribe("session.created", func(_ any) {
				mu.Lock()
				receivedTopics = append(receivedTopics, "session.created")
				mu.Unlock()
			})

			busApp.wireDelegateToolIfEnabled(coordinatorEngine, coordManifest)

			delegateTool, found := coordinatorEngine.GetDelegateTool()
			Expect(found).To(BeTrue())

			engines := delegateTool.Engines()
			Expect(engines).To(HaveKey("explorer-events"))

			mu.Lock()
			defer mu.Unlock()
			Expect(engines["explorer-events"].EventBus()).To(BeIdenticalTo(coordinatorEngine.EventBus()))
		})
	})
})
