package app

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/tool"
	skilltool "github.com/baphled/flowstate/internal/tool/skill"
	todotool "github.com/baphled/flowstate/internal/tool/todo"
)

// emptySkillLoader satisfies skilltool.Loader with a zero-skill set so the
// skill_load tool can register at construction without touching disk.
type emptySkillLoader struct{}

func (emptySkillLoader) LoadAll() ([]skill.Skill, error) {
	return nil, nil
}

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
				Capabilities: agent.Capabilities{
					Tools: []string{"delegate"},
				},
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

	// Diagnostic for session-1776611908809856897 (planner session, 8 messages,
	// ToolCalls=None on every assistant turn, model emitted tool-call-shaped
	// JSON as plain content). The canonical planner manifest declares
	// capabilities.tools = [delegate, coordination_store, skill_load, todowrite].
	// If any of those four names fails to reach req.Tools, the model cannot
	// legitimately call the tool and falls back to hallucinating JSON. The
	// existing "includes the delegate tool" test above only probes delegate;
	// this context asserts the whole planner profile so a regression that
	// silently drops one of the other three shows up immediately.
	Context("when streaming as planner with the canonical tool profile", func() {
		It("exposes delegate, coordination_store, skill_load and todowrite", func() {
			plannerManifest := agent.Manifest{
				ID:   "planner",
				Name: "Planner",
				Capabilities: agent.Capabilities{
					Tools: []string{
						"delegate",
						"coordination_store",
						"skill_load",
						"todowrite",
					},
				},
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			agentReg.Register(&plannerManifest)

			// Build the engine already seeded with the two static tools
			// (skill_load, todowrite) that the real buildTools() registers
			// at startup. wireDelegateToolIfEnabled supplies the remaining
			// delegate, background_output, background_cancel and
			// coordination_store when the manifest opts in.
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
					return plannerManifest
				},
				twc: twc,
			})

			staticTools := []tool.Tool{
				skilltool.New(emptySkillLoader{}),
				todotool.New(todotool.NewMemoryStore()),
			}

			eng = engine.New(engine.Config{
				Manifest:      plannerManifest,
				AgentRegistry: agentReg,
				Registry:      providerReg,
				ChatProvider:  spy,
				HookChain:     hookChain,
				Tools:         staticTools,
			})

			application.wireDelegateToolIfEnabled(eng, plannerManifest)
			ensureToolsFn = func(m agent.Manifest) {
				application.wireDelegateToolIfEnabled(eng, m)
			}

			_, err := eng.Stream(context.Background(), "planner", "list the plans")
			Expect(err).NotTo(HaveOccurred())

			Expect(spy.capturedRequest).NotTo(BeNil())
			names := make([]string, 0, len(spy.capturedRequest.Tools))
			for _, t := range spy.capturedRequest.Tools {
				names = append(names, t.Name)
			}

			Expect(names).To(ContainElements(
				"delegate",
				"coordination_store",
				"skill_load",
				"todowrite",
			), "every tool declared in planner.md capabilities.tools must reach the provider request; "+
				"any missing entry forces the model to hallucinate tool-call-shaped JSON as content")
		})
	})

	// Idempotency: if wireDelegateToolIfEnabled runs twice for the same
	// manifest — which happens when ensureTools fires on a manifest switch
	// back to a delegating agent after a non-delegating detour, or when the
	// setup path and the App-build path both wire the default manifest at
	// startup — the engine must not end up with duplicate delegate,
	// background_output, background_cancel or coordination_store entries in
	// its tool registry. Duplicate entries corrupt the provider tool schema
	// (the OpenAI SDK forbids duplicate function names) and confuse the
	// allowed-set filter.
	Context("when wireDelegateToolIfEnabled is invoked twice for the same manifest", func() {
		It("does not duplicate delegation tools in the engine", func() {
			plannerManifest := agent.Manifest{
				ID:   "planner",
				Name: "Planner",
				Capabilities: agent.Capabilities{
					Tools: []string{"delegate", "coordination_store"},
				},
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			agentReg.Register(&plannerManifest)
			buildTestEngine(executorManifest)

			application.wireDelegateToolIfEnabled(eng, plannerManifest)
			application.wireDelegateToolIfEnabled(eng, plannerManifest)

			_, err := eng.Stream(context.Background(), "planner", "hello")
			Expect(err).NotTo(HaveOccurred())

			Expect(spy.capturedRequest).NotTo(BeNil())
			counts := map[string]int{}
			for _, t := range spy.capturedRequest.Tools {
				counts[t.Name]++
			}

			Expect(counts["delegate"]).To(Equal(1),
				"delegate must appear exactly once even after wireDelegateToolIfEnabled is called twice")
			Expect(counts["background_output"]).To(Equal(1),
				"background_output must appear exactly once")
			Expect(counts["background_cancel"]).To(Equal(1),
				"background_cancel must appear exactly once")
			Expect(counts["coordination_store"]).To(Equal(1),
				"coordination_store must appear exactly once")
		})
	})

	// Delegation tool accumulation on manifest swap. Scenario:
	// the app boots with default_agent=executor (can_delegate=false), which
	// adds suggest_delegate. The CLI then selects --agent planner
	// (can_delegate=true), triggering SetManifest and the ensureTools
	// callback. wireDelegateToolIfEnabled adds delegate, but
	// wireSuggestDelegateToolIfDisabled early-returns without ever removing
	// the stale suggest_delegate. The engine now advertises BOTH tools to
	// the provider; Anthropic rejects the request with:
	//   400 Bad Request: tools: Tool names must be unique
	// (two tools sharing the overlapping "delegate"-prefixed schema
	// identifiers), which in turn triggers unintended failover away from
	// the caller-pinned provider.
	//
	// Contract: a given engine advertises either delegate (can_delegate=true)
	// or suggest_delegate (can_delegate=false), never both — across the
	// entire lifetime of the engine, including after arbitrarily many
	// SetManifest swaps in either direction. Background delegation tools
	// (background_output, background_cancel, coordination_store) share the
	// delegate fate: they are only meaningful alongside delegate, so they
	// must be removed when swapping to a non-delegating manifest.
	Context("when the manifest swaps between delegating and non-delegating", func() {
		var plannerManifest agent.Manifest

		BeforeEach(func() {
			plannerManifest = agent.Manifest{
				ID:   "planner",
				Name: "Planner",
				Capabilities: agent.Capabilities{
					Tools: []string{"delegate", "coordination_store"},
				},
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			agentReg.Register(&plannerManifest)
		})

		It("removes suggest_delegate when swapping executor -> planner", func() {
			// Boot path: default manifest is executor. Both wiring
			// functions run at startup against executor.
			buildTestEngine(executorManifest)
			application.wireSuggestDelegateToolIfDisabled(eng, executorManifest)
			Expect(eng.HasTool("suggest_delegate")).To(BeTrue(),
				"precondition: boot-time wiring leaves suggest_delegate on the executor engine")

			// CLI then selects --agent planner, triggering the ensureTools
			// callback path. Both wiring functions run against planner.
			application.wireDelegateToolIfEnabled(eng, plannerManifest)
			application.wireSuggestDelegateToolIfDisabled(eng, plannerManifest)

			Expect(eng.HasTool("delegate")).To(BeTrue(),
				"delegate must be registered on a delegating manifest")
			Expect(eng.HasTool("suggest_delegate")).To(BeFalse(),
				"suggest_delegate must be removed when swapping to a delegating manifest; "+
					"leaving it in place makes Anthropic reject with 'tool names must be unique'")
		})

		It("removes delegate tools when swapping planner -> executor", func() {
			// Boot with planner first. buildTestEngine always wires against
			// executorManifest (its hardcoded startup manifest), so an extra
			// pass wires the delegating-planner profile onto the same engine.
			buildTestEngine(plannerManifest)
			application.wireDelegateToolIfEnabled(eng, plannerManifest)
			application.wireSuggestDelegateToolIfDisabled(eng, plannerManifest)

			Expect(eng.HasTool("delegate")).To(BeTrue(),
				"precondition: delegate is wired on the planner engine")
			Expect(eng.HasTool("background_output")).To(BeTrue(),
				"precondition: background_output is wired on the planner engine")
			Expect(eng.HasTool("background_cancel")).To(BeTrue(),
				"precondition: background_cancel is wired on the planner engine")
			Expect(eng.HasTool("coordination_store")).To(BeTrue(),
				"precondition: coordination_store is wired on the planner engine "+
					"(capabilities.tools includes coordination_store)")

			// Swap to executor (can_delegate=false).
			application.wireDelegateToolIfEnabled(eng, executorManifest)
			application.wireSuggestDelegateToolIfDisabled(eng, executorManifest)

			Expect(eng.HasTool("suggest_delegate")).To(BeTrue(),
				"suggest_delegate must be registered on a non-delegating manifest")
			Expect(eng.HasTool("delegate")).To(BeFalse(),
				"delegate must be removed when swapping to a non-delegating manifest")
			Expect(eng.HasTool("background_output")).To(BeFalse(),
				"background_output is only meaningful alongside delegate; "+
					"it must be removed when swapping to a non-delegating manifest")
			Expect(eng.HasTool("background_cancel")).To(BeFalse(),
				"background_cancel is only meaningful alongside delegate; "+
					"it must be removed when swapping to a non-delegating manifest")
			Expect(eng.HasTool("coordination_store")).To(BeFalse(),
				"coordination_store is only meaningful alongside delegate; "+
					"it must be removed when swapping to a non-delegating manifest")
		})

		It("reaches the provider with exactly one of delegate/suggest_delegate after swap", func() {
			// End-to-end assertion: what the provider sees. Executor ->
			// planner swap must not produce duplicate-named tools in the
			// ChatRequest. Anthropic's 400 is about tools reaching the
			// wire, not just the in-memory registry.
			buildTestEngine(executorManifest)
			application.wireSuggestDelegateToolIfDisabled(eng, executorManifest)

			application.wireDelegateToolIfEnabled(eng, plannerManifest)
			application.wireSuggestDelegateToolIfDisabled(eng, plannerManifest)

			_, err := eng.Stream(context.Background(), "planner", "hello")
			Expect(err).NotTo(HaveOccurred())

			Expect(spy.capturedRequest).NotTo(BeNil())
			counts := map[string]int{}
			for _, t := range spy.capturedRequest.Tools {
				counts[t.Name]++
			}
			Expect(counts["delegate"]).To(Equal(1),
				"delegate must appear exactly once in the provider request")
			Expect(counts["suggest_delegate"]).To(Equal(0),
				"suggest_delegate must not reach the provider when the active manifest "+
					"can delegate; duplicate-purpose tools cause Anthropic to 400")
		})

		It("is idempotent when wireSuggestDelegateToolIfDisabled runs twice", func() {
			// Mirror of the existing delegate-idempotency context above.
			// wireSuggestDelegateToolIfDisabled currently appends without
			// a HasTool guard, so calling it twice on a non-delegating
			// manifest produces duplicate suggest_delegate entries —
			// latent surface of the same accumulation bug.
			buildTestEngine(executorManifest)
			application.wireSuggestDelegateToolIfDisabled(eng, executorManifest)
			application.wireSuggestDelegateToolIfDisabled(eng, executorManifest)

			_, err := eng.Stream(context.Background(), "executor", "hello")
			Expect(err).NotTo(HaveOccurred())

			Expect(spy.capturedRequest).NotTo(BeNil())
			counts := map[string]int{}
			for _, t := range spy.capturedRequest.Tools {
				counts[t.Name]++
			}
			Expect(counts["suggest_delegate"]).To(Equal(1),
				"suggest_delegate must appear exactly once even after "+
					"wireSuggestDelegateToolIfDisabled is called twice")
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

	// Coordination store decoupling: coordination_store's lifecycle is
	// governed solely by manifest.Capabilities.Tools, regardless of
	// CanDelegate. Non-delegating agents that opt in (plan-writer,
	// plan-reviewer, explorer, analyst, librarian) must receive the tool;
	// non-delegating agents that opt out must not.
	Context("when a non-delegating manifest declares coordination_store in capabilities.tools", func() {
		var coordManifest agent.Manifest

		BeforeEach(func() {
			coordManifest = agent.Manifest{
				ID:   "plan-writer",
				Name: "Plan Writer",
				Capabilities: agent.Capabilities{
					Tools: []string{"bash", "file", "web", "coordination_store"},
				},
				Delegation: agent.Delegation{
					CanDelegate: false,
				},
			}
			agentReg.Register(&coordManifest)
		})

		It("registers coordination_store in the engine", func() {
			buildTestEngine(coordManifest)
			application.wireDelegateToolIfEnabled(eng, coordManifest)

			Expect(eng.HasTool("coordination_store")).To(BeTrue(),
				"non-delegating manifest opted in via capabilities.tools; "+
					"coordination_store must be wired regardless of CanDelegate")
		})

		It("surfaces coordination_store in the provider request", func() {
			buildTestEngine(coordManifest)
			application.wireDelegateToolIfEnabled(eng, coordManifest)

			_, err := eng.Stream(context.Background(), "plan-writer", "hello")
			Expect(err).NotTo(HaveOccurred())
			Expect(spy.capturedRequest).NotTo(BeNil())

			toolNames := make([]string, 0, len(spy.capturedRequest.Tools))
			for _, t := range spy.capturedRequest.Tools {
				toolNames = append(toolNames, t.Name)
			}
			Expect(toolNames).To(ContainElement("coordination_store"))
		})
	})

	// Regression guard for "thinking a skill was a tool" runtime error.
	// When a delegate agent declares skill_load in capabilities.tools but
	// buildToolsForManifestWithStore omits the tool implementation, the engine
	// allowlist includes "skill_load" but no handler is registered. The
	// SkillAutoLoaderHook still injects "Use skill_load(name) only when relevant"
	// into the system prompt; the model tries to call skill_load, gets
	// ErrToolNotFound, and falls back to calling the skill name directly as if it
	// were a tool — which is never registered either.
	Context("when a delegate agent declares skill_load in capabilities.tools", func() {
		var skillDir string

		BeforeEach(func() {
			var err error
			skillDir, err = os.MkdirTemp("", "skills-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(skillDir) })
		})

		It("registers skill_load in the delegate engine's tools", func() {
			delegateManifest := agent.Manifest{
				ID:   "code-reviewer",
				Name: "Code Reviewer",
				Capabilities: agent.Capabilities{
					Tools: []string{"file", "coordination_store", "skill_load"},
				},
				Delegation: agent.Delegation{CanDelegate: false},
			}
			agentReg.Register(&delegateManifest)
			application.Config = &config.AppConfig{SkillDir: skillDir}

			tools := application.buildToolsForManifestWithStore(delegateManifest, nil)

			names := make([]string, 0, len(tools))
			for _, t := range tools {
				names = append(names, t.Name())
			}
			Expect(names).To(ContainElement("skill_load"),
				"delegate engine must register skill_load when the manifest declares it; "+
					"without it the SkillAutoLoaderHook injection causes ErrToolNotFound "+
					"and the model calls skill names directly as tools")
		})

		It("surfaces skill_load in the provider request for a delegate agent", func() {
			delegateManifest := agent.Manifest{
				ID:   "code-reviewer",
				Name: "Code Reviewer",
				Capabilities: agent.Capabilities{
					Tools: []string{"file", "coordination_store", "skill_load"},
				},
				Delegation: agent.Delegation{CanDelegate: false},
			}
			agentReg.Register(&delegateManifest)
			application.Config = &config.AppConfig{SkillDir: skillDir}

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
				manifestGetter: func() agent.Manifest { return delegateManifest },
				twc:            twc,
			})

			delegateTools := application.buildToolsForManifestWithStore(delegateManifest, nil)
			eng = engine.New(engine.Config{
				Manifest:      delegateManifest,
				AgentRegistry: agentReg,
				Registry:      providerReg,
				ChatProvider:  spy,
				HookChain:     hookChain,
				Tools:         delegateTools,
			})
			ensureToolsFn = func(m agent.Manifest) {
				application.wireDelegateToolIfEnabled(eng, m)
			}
			application.wireDelegateToolIfEnabled(eng, delegateManifest)

			_, err := eng.Stream(context.Background(), "code-reviewer", "review this code")
			Expect(err).NotTo(HaveOccurred())

			Expect(spy.capturedRequest).NotTo(BeNil())
			names := make([]string, 0, len(spy.capturedRequest.Tools))
			for _, t := range spy.capturedRequest.Tools {
				names = append(names, t.Name)
			}
			Expect(names).To(ContainElement("skill_load"),
				"skill_load must reach the provider for a delegate agent that declares it; "+
					"a missing entry causes ErrToolNotFound and skill-as-tool fallback errors")
		})
	})

	Context("when a non-delegating manifest omits coordination_store from capabilities.tools", func() {
		It("preserves the guard: coordination_store is not wired", func() {
			noCoordManifest := agent.Manifest{
				ID:   "executor",
				Name: "Executor",
				Capabilities: agent.Capabilities{
					Tools: []string{"bash", "file", "web"},
				},
				Delegation: agent.Delegation{
					CanDelegate: false,
				},
			}
			agentReg.Register(&noCoordManifest)
			buildTestEngine(noCoordManifest)
			application.wireDelegateToolIfEnabled(eng, noCoordManifest)

			Expect(eng.HasTool("coordination_store")).To(BeFalse(),
				"manifest does not declare coordination_store; the canonical "+
					"hasCoordinationTool guard must still drop it")
		})
	})

	Context("when swapping from a delegating manifest to a non-delegating manifest that declares coordination_store", func() {
		It("keeps coordination_store wired across the swap", func() {
			plannerManifest := agent.Manifest{
				ID:   "planner",
				Name: "Planner",
				Capabilities: agent.Capabilities{
					Tools: []string{"delegate", "coordination_store"},
				},
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			planWriterManifest := agent.Manifest{
				ID:   "plan-writer",
				Name: "Plan Writer",
				Capabilities: agent.Capabilities{
					Tools: []string{"bash", "file", "coordination_store"},
				},
				Delegation: agent.Delegation{
					CanDelegate: false,
				},
			}
			agentReg.Register(&plannerManifest)
			agentReg.Register(&planWriterManifest)

			buildTestEngine(plannerManifest)
			application.wireDelegateToolIfEnabled(eng, plannerManifest)
			Expect(eng.HasTool("coordination_store")).To(BeTrue(),
				"sanity: planner declares coordination_store and is wired pre-swap")

			application.wireDelegateToolIfEnabled(eng, planWriterManifest)
			Expect(eng.HasTool("coordination_store")).To(BeTrue(),
				"plan-writer declares coordination_store; the swap must NOT "+
					"strip it just because CanDelegate flipped to false")
		})
	})
})
