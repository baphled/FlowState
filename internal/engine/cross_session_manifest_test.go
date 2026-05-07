package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// newTempContextStore returns a FileContextStore rooted in a per-test
// temp directory, with cleanup deferred to the spec. The chain-store
// dual-write only fires when storeResponse sees a non-nil context
// store, so background-hook tests need one wired up.
func newTempContextStore(prefix string) *recall.FileContextStore {
	tmpDir, err := os.MkdirTemp("", prefix)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(tmpDir) })

	store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "context.json"), "")
	Expect(err).NotTo(HaveOccurred())
	return store
}

// capturingChainStore records every Append call's agentID + message in
// arrival order. Tests use it as the observation point for the
// "background goroutine stamps the spawn-time manifest, not the live
// engine-state manifest" behaviour pinned by the per-session-binding
// fix.
type capturingChainStore struct {
	mu       sync.Mutex
	appends  []chainAppend
	released chan struct{} // closed by the test once it has mutated e.manifest
	signal   chan struct{} // closed once the first Append has parked at the gate
	once     sync.Once
}

type chainAppend struct {
	agentID string
	role    string
	content string
}

func newCapturingChainStore() *capturingChainStore {
	return &capturingChainStore{
		released: make(chan struct{}),
		signal:   make(chan struct{}),
	}
}

func (s *capturingChainStore) Append(agentID string, msg provider.Message) error {
	// Park the first Append until the test mutates e.manifest. This
	// is the deterministic seam that exposes whether the goroutine
	// reads e.manifest.ID live (post-mutation = "beta") or from the
	// snapshot it inherited at spawn (= "alpha").
	s.once.Do(func() { close(s.signal) })
	<-s.released

	s.mu.Lock()
	s.appends = append(s.appends, chainAppend{
		agentID: agentID,
		role:    msg.Role,
		content: msg.Content,
	})
	s.mu.Unlock()
	return nil
}

func (s *capturingChainStore) Search(_ context.Context, _ string, _ int) ([]recall.SearchResult, error) {
	return nil, nil
}

func (s *capturingChainStore) GetByAgent(_ string, _ int) ([]provider.Message, error) {
	return nil, nil
}

func (s *capturingChainStore) ChainID() string { return "test-chain" }

func (s *capturingChainStore) waitParked() { <-s.signal }
func (s *capturingChainStore) release()    { close(s.released) }

func (s *capturingChainStore) snapshot() []chainAppend {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]chainAppend, len(s.appends))
	copy(out, s.appends)
	return out
}

// gatingProvider is a thread-safe Provider that records every Stream
// invocation's system prompt and tool names, then parks each call on a
// per-call gate channel until the test signals it to drain.
//
// Used to interleave two concurrent Stream calls so that, while both are
// in flight, the engine cannot have "swung back" to its original
// manifest. Captures evidence at the model boundary the LLM observes.
type gatingProvider struct {
	mu       sync.Mutex
	captures []capturedStream
	// release[i] receives a value before capture i is allowed to drain.
	// The test pushes to releases to control ordering.
	releases []chan struct{}
}

type capturedStream struct {
	systemPrompt string
	toolNames    []string
}

func (g *gatingProvider) Name() string { return "gating-provider" }

func (g *gatingProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	g.mu.Lock()
	idx := len(g.captures)
	cap := capturedStream{}
	if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
		cap.systemPrompt = req.Messages[0].Content
	}
	for _, t := range req.Tools {
		cap.toolNames = append(cap.toolNames, t.Name)
	}
	g.captures = append(g.captures, cap)
	// Lazily allocate a gate for this capture if the test did not
	// pre-arm one — default to "released immediately".
	for len(g.releases) <= idx {
		ch := make(chan struct{}, 1)
		ch <- struct{}{}
		g.releases = append(g.releases, ch)
	}
	gate := g.releases[idx]
	g.mu.Unlock()

	out := make(chan provider.StreamChunk, 1)
	go func() {
		defer close(out)
		<-gate
		out <- provider.StreamChunk{Content: "ok", Done: true}
	}()
	return out, nil
}

func (g *gatingProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (g *gatingProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (g *gatingProvider) Models() ([]provider.Model, error) { return nil, nil }

// armGate replaces the default released gate at idx with a manual one.
// The test calls it before triggering Stream so the call parks.
func (g *gatingProvider) armGate(idx int) chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	for len(g.releases) <= idx {
		g.releases = append(g.releases, make(chan struct{}, 1))
	}
	if len(g.releases[idx]) == 1 { // drain default-armed
		<-g.releases[idx]
	}
	return g.releases[idx]
}

func (g *gatingProvider) capture(idx int) capturedStream {
	g.mu.Lock()
	defer g.mu.Unlock()
	if idx >= len(g.captures) {
		return capturedStream{}
	}
	return g.captures[idx]
}

func (g *gatingProvider) waitForCaptures(n int) {
	for {
		g.mu.Lock()
		got := len(g.captures)
		g.mu.Unlock()
		if got >= n {
			return
		}
	}
}

var _ = Describe("Cross-session manifest binding", func() {
	// Two concurrent Stream() calls with different agentIDs must each
	// receive their own system prompt and tool definitions, regardless
	// of order or interleaving. Pre-fix, the engine holds a single
	// process-wide manifest field; whichever Stream() most recently
	// called SetManifest wins for both calls' BuildSystemPrompt /
	// buildToolSchemas reads.
	Describe("two concurrent Stream calls on the same engine", func() {
		It("each call sees its own agent's system prompt and tool schemas", func() {
			gp := &gatingProvider{}

			plannerManifest := &agent.Manifest{
				ID:   "planner",
				Name: "Planner",
				Instructions: agent.Instructions{
					SystemPrompt: "PLANNER_SYSTEM_PROMPT_MARKER",
				},
				Capabilities: agent.Capabilities{
					Tools: []string{"plan_tool"},
				},
				ContextManagement: agent.DefaultContextManagement(),
			}
			techLeadManifest := &agent.Manifest{
				ID:   "tech-lead",
				Name: "Tech Lead",
				Instructions: agent.Instructions{
					SystemPrompt: "TECH_LEAD_SYSTEM_PROMPT_MARKER",
				},
				Capabilities: agent.Capabilities{
					Tools: []string{"lead_tool"},
				},
				ContextManagement: agent.DefaultContextManagement(),
			}

			registry := agent.NewRegistry()
			registry.Register(plannerManifest)
			registry.Register(techLeadManifest)

			eng := engine.New(engine.Config{
				ChatProvider:  gp,
				AgentRegistry: registry,
				Manifest:      *plannerManifest,
				Tools: []tool.Tool{
					&mockTool{name: "plan_tool", description: "planner tool"},
					&mockTool{name: "lead_tool", description: "tech-lead tool"},
				},
			})

			// Park both calls before they hit the provider so the
			// engine cannot serialise its way out of the bug.
			gateA := gp.armGate(0)
			gateB := gp.armGate(1)

			ctxA := context.WithValue(context.Background(), session.IDKey{}, "session-A")
			ctxB := context.WithValue(context.Background(), session.IDKey{}, "session-B")

			var wg sync.WaitGroup
			wg.Add(2)

			go func() {
				defer wg.Done()
				chunks, err := eng.Stream(ctxA, "planner", "hello A")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain
				}
			}()

			// Wait until A has been captured by the provider, so its
			// system prompt is fixed in the recorded request, before B
			// arrives and triggers SetManifest.
			gp.waitForCaptures(1)

			go func() {
				defer wg.Done()
				chunks, err := eng.Stream(ctxB, "tech-lead", "hello B")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain
				}
			}()

			gp.waitForCaptures(2)

			close(gateA)
			close(gateB)
			wg.Wait()

			capA := gp.capture(0)
			capB := gp.capture(1)

			// Spec — Each invocation MUST carry the system prompt
			// derived from its own agent's manifest. Pre-fix, both
			// captures see whichever manifest SetManifest most
			// recently swapped in.
			Expect(capA.systemPrompt).To(ContainSubstring("PLANNER_SYSTEM_PROMPT_MARKER"),
				"session-A's call must use the planner manifest")
			Expect(capA.systemPrompt).NotTo(ContainSubstring("TECH_LEAD_SYSTEM_PROMPT_MARKER"),
				"session-A must not see tech-lead's prompt")

			Expect(capB.systemPrompt).To(ContainSubstring("TECH_LEAD_SYSTEM_PROMPT_MARKER"),
				"session-B's call must use the tech-lead manifest")
			Expect(capB.systemPrompt).NotTo(ContainSubstring("PLANNER_SYSTEM_PROMPT_MARKER"),
				"session-B must not see planner's prompt")

			// Tool schemas MUST also reflect each call's manifest
			// capabilities.
			Expect(capA.toolNames).To(ContainElement("plan_tool"))
			Expect(capA.toolNames).NotTo(ContainElement("lead_tool"))
			Expect(capB.toolNames).To(ContainElement("lead_tool"))
			Expect(capB.toolNames).NotTo(ContainElement("plan_tool"))
		})
	})

	// Swap-for-next-turn semantic: SetManifest issued mid-stream
	// affects the NEXT Stream call only. The in-flight call must
	// complete with its original manifest.
	Describe("SetManifest issued while a Stream is in flight", func() {
		It("the in-flight stream completes with its original manifest and the next Stream uses the new manifest", func() {
			gp := &gatingProvider{}

			alphaManifest := agent.Manifest{
				ID:   "alpha",
				Name: "Alpha",
				Instructions: agent.Instructions{
					SystemPrompt: "ALPHA_SYSTEM_PROMPT",
				},
				Capabilities: agent.Capabilities{
					Tools: []string{"alpha_tool"},
				},
				ContextManagement: agent.DefaultContextManagement(),
			}
			betaManifest := agent.Manifest{
				ID:   "beta",
				Name: "Beta",
				Instructions: agent.Instructions{
					SystemPrompt: "BETA_SYSTEM_PROMPT",
				},
				Capabilities: agent.Capabilities{
					Tools: []string{"beta_tool"},
				},
				ContextManagement: agent.DefaultContextManagement(),
			}

			eng := engine.New(engine.Config{
				ChatProvider: gp,
				Manifest:     alphaManifest,
				Tools: []tool.Tool{
					&mockTool{name: "alpha_tool", description: "alpha"},
					&mockTool{name: "beta_tool", description: "beta"},
				},
			})

			gateAlpha := gp.armGate(0)
			gateBeta := gp.armGate(1)

			ctxAlpha := context.WithValue(context.Background(), session.IDKey{}, "session-alpha")
			ctxBeta := context.WithValue(context.Background(), session.IDKey{}, "session-beta")

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				chunks, err := eng.Stream(ctxAlpha, "", "hello alpha")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain
				}
			}()

			gp.waitForCaptures(1)

			// Mid-stream PATCH: agent swapped to beta.
			eng.SetManifest(betaManifest)

			wg.Add(1)
			go func() {
				defer wg.Done()
				chunks, err := eng.Stream(ctxBeta, "", "hello beta")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain
				}
			}()

			gp.waitForCaptures(2)
			close(gateAlpha)
			close(gateBeta)
			wg.Wait()

			capAlpha := gp.capture(0)
			capBeta := gp.capture(1)

			// In-flight call retains the manifest it captured at
			// Stream() entry.
			Expect(capAlpha.systemPrompt).To(ContainSubstring("ALPHA_SYSTEM_PROMPT"))
			Expect(capAlpha.systemPrompt).NotTo(ContainSubstring("BETA_SYSTEM_PROMPT"))
			Expect(capAlpha.toolNames).To(ContainElement("alpha_tool"))
			Expect(capAlpha.toolNames).NotTo(ContainElement("beta_tool"))

			// Next Stream picks up the SetManifest swap.
			Expect(capBeta.systemPrompt).To(ContainSubstring("BETA_SYSTEM_PROMPT"))
			Expect(capBeta.systemPrompt).NotTo(ContainSubstring("ALPHA_SYSTEM_PROMPT"))
			Expect(capBeta.toolNames).To(ContainElement("beta_tool"))
			Expect(capBeta.toolNames).NotTo(ContainElement("alpha_tool"))
		})
	})

	// Direct seam pin — the public BuildSystemPrompt /
	// ToolSchemas pair must accept a per-session manifest
	// override so callers can build prompt + tools for a
	// given agent without mutating engine-wide state. Pre-fix,
	// the engine offers no such API, so concurrent callers
	// must SetManifest globally and any other concurrent
	// caller's prompt is corrupted.
	Describe("per-context manifest binding on the build seam", func() {
		It("BuildSystemPrompt and ToolSchemas honour a manifest bound to the context", func() {
			gp := &gatingProvider{}

			plannerManifest := agent.Manifest{
				ID:   "planner",
				Name: "Planner",
				Instructions: agent.Instructions{
					SystemPrompt: "PLANNER_SYSTEM_PROMPT_MARKER",
				},
				Capabilities: agent.Capabilities{
					Tools: []string{"plan_tool"},
				},
				ContextManagement: agent.DefaultContextManagement(),
			}
			techLeadManifest := agent.Manifest{
				ID:   "tech-lead",
				Name: "Tech Lead",
				Instructions: agent.Instructions{
					SystemPrompt: "TECH_LEAD_SYSTEM_PROMPT_MARKER",
				},
				Capabilities: agent.Capabilities{
					Tools: []string{"lead_tool"},
				},
				ContextManagement: agent.DefaultContextManagement(),
			}

			eng := engine.New(engine.Config{
				ChatProvider: gp,
				Manifest:     plannerManifest,
				Tools: []tool.Tool{
					&mockTool{name: "plan_tool", description: "planner tool"},
					&mockTool{name: "lead_tool", description: "tech-lead tool"},
				},
			})

			ctxPlanner := engine.WithBoundManifest(context.Background(), plannerManifest)
			ctxLead := engine.WithBoundManifest(context.Background(), techLeadManifest)

			plannerPrompt := eng.BuildSystemPromptCtx(ctxPlanner)
			leadPrompt := eng.BuildSystemPromptCtx(ctxLead)

			Expect(plannerPrompt).To(ContainSubstring("PLANNER_SYSTEM_PROMPT_MARKER"))
			Expect(plannerPrompt).NotTo(ContainSubstring("TECH_LEAD_SYSTEM_PROMPT_MARKER"))
			Expect(leadPrompt).To(ContainSubstring("TECH_LEAD_SYSTEM_PROMPT_MARKER"))
			Expect(leadPrompt).NotTo(ContainSubstring("PLANNER_SYSTEM_PROMPT_MARKER"))

			plannerTools := eng.ToolSchemasCtx(ctxPlanner)
			leadTools := eng.ToolSchemasCtx(ctxLead)

			plannerNames := toolNames(plannerTools)
			leadNames := toolNames(leadTools)

			Expect(plannerNames).To(ContainElement("plan_tool"))
			Expect(plannerNames).NotTo(ContainElement("lead_tool"))
			Expect(leadNames).To(ContainElement("lead_tool"))
			Expect(leadNames).NotTo(ContainElement("plan_tool"))
		})
	})

	// Existing single-session, single-agent flow must not regress.
	// One engine, one manifest, sequential Stream calls — the
	// captured prompt always matches the active manifest.
	Describe("single-session, single-agent flow", func() {
		It("does not regress when no agent override is supplied", func() {
			gp := &gatingProvider{}

			soloManifest := agent.Manifest{
				ID:   "solo",
				Name: "Solo",
				Instructions: agent.Instructions{
					SystemPrompt: "SOLO_SYSTEM_PROMPT",
				},
				Capabilities: agent.Capabilities{
					Tools: []string{"solo_tool"},
				},
				ContextManagement: agent.DefaultContextManagement(),
			}

			eng := engine.New(engine.Config{
				ChatProvider: gp,
				Manifest:     soloManifest,
				Tools: []tool.Tool{
					&mockTool{name: "solo_tool", description: "solo"},
				},
			})

			ctx := context.WithValue(context.Background(), session.IDKey{}, "solo-session")
			var calls atomic.Int32
			for i := 0; i < 3; i++ {
				calls.Add(1)
				chunks, err := eng.Stream(ctx, "", "hello solo")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain
				}
			}
			Expect(calls.Load()).To(Equal(int32(3)))

			for i := 0; i < 3; i++ {
				cap := gp.capture(i)
				Expect(cap.systemPrompt).To(ContainSubstring("SOLO_SYSTEM_PROMPT"))
				Expect(cap.toolNames).To(ContainElement("solo_tool"))
			}
		})
	})

	// Background-hook tightening: a Stream() spawned with manifest
	// alpha must complete its detached post-stream work — the chain
	// store dual-write that fires from the streamWithToolLoop
	// goroutine — using alpha's identity, even when a concurrent
	// Stream on the same engine has called SetManifest(beta) before
	// the goroutine reaches its e.manifest.ID read.
	//
	// Pre-fix, dualWriteToChainStore reads e.manifest.ID directly:
	// the post-mutation field wins and the chain store records the
	// wrong agent. Post-fix, the bound manifest threaded through ctx
	// from Stream() entry into the detached goroutine is the source
	// of truth.
	Describe("background hook stamping under concurrent SetManifest", func() {
		It("dualWriteToChainStore stamps the spawn-time agent ID, not the post-mutation one", func() {
			gp := &gatingProvider{}
			chain := newCapturingChainStore()

			alphaManifest := agent.Manifest{
				ID:                "alpha",
				Name:              "Alpha",
				Instructions:      agent.Instructions{SystemPrompt: "ALPHA_SYSTEM_PROMPT"},
				Capabilities:      agent.Capabilities{Tools: []string{"alpha_tool"}},
				ContextManagement: agent.DefaultContextManagement(),
			}
			betaManifest := agent.Manifest{
				ID:                "beta",
				Name:              "Beta",
				Instructions:      agent.Instructions{SystemPrompt: "BETA_SYSTEM_PROMPT"},
				Capabilities:      agent.Capabilities{Tools: []string{"beta_tool"}},
				ContextManagement: agent.DefaultContextManagement(),
			}

			eng := engine.New(engine.Config{
				ChatProvider: gp,
				ChainStore:   chain,
				Manifest:     alphaManifest,
				Tools: []tool.Tool{
					&mockTool{name: "alpha_tool", description: "alpha"},
					&mockTool{name: "beta_tool", description: "beta"},
				},
			})
			eng.SetContextStore(newTempContextStore("xsess-mfst-alpha"), "session-alpha")

			ctxAlpha := context.WithValue(context.Background(), session.IDKey{}, "session-alpha")

			// Pre-arm the provider gate so the call parks rather than
			// drains on the auto-released default. Released explicitly
			// below once the test has confirmed the request was captured.
			gateAlpha := gp.armGate(0)

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				chunks, err := eng.Stream(ctxAlpha, "", "hello alpha")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain
				}
			}()

			// Wait until the provider has captured alpha's request,
			// then let it drain. The detached streamWithToolLoop
			// goroutine then drives completeResponse →
			// dualWriteToChainStore where it parks at the chain
			// store's gate.
			gp.waitForCaptures(1)
			close(gateAlpha)

			chain.waitParked()

			// Mid-flight mutation: a concurrent Stream(beta) would
			// fire SetManifest(beta). Simulate that race with a
			// direct call so the test stays deterministic.
			eng.SetManifest(betaManifest)

			// Release the chain store gate. The goroutine resumes
			// and stamps the agent ID on its Append call. Pre-fix
			// it reads the post-mutation e.manifest.ID = "beta".
			chain.release()
			wg.Wait()

			appends := chain.snapshot()
			Expect(appends).NotTo(BeEmpty(), "the streamWithToolLoop goroutine must have appended an assistant message")

			// Spec — every chain-store append fired by the alpha
			// stream MUST carry alpha's ID, even though the engine's
			// live manifest has since been swung to beta.
			for _, a := range appends {
				Expect(a.agentID).To(Equal("alpha"),
					"expected chain-store append to carry the spawn-time agent ID; got %q (post-mutation engine.manifest = %q)", a.agentID, "beta")
			}
		})

		// Same shape via the agent registry path: Stream(ctx, "alpha", …)
		// resolves alpha from the registry, snapshots into ctx, and the
		// goroutine must keep alpha as the chain-store agent even when a
		// concurrent Stream(ctx, "beta", …) is also in flight and triggers
		// SetManifest(beta) at registry-resolution time.
		It("two concurrent Streams with different agentIDs stamp their own agent on chain-store appends", func() {
			gp := &gatingProvider{}
			chain := newCapturingChainStore()

			plannerManifest := &agent.Manifest{
				ID:                "planner",
				Name:              "Planner",
				Instructions:      agent.Instructions{SystemPrompt: "PLANNER_SYSTEM_PROMPT"},
				Capabilities:      agent.Capabilities{Tools: []string{"plan_tool"}},
				ContextManagement: agent.DefaultContextManagement(),
			}
			techLeadManifest := &agent.Manifest{
				ID:                "tech-lead",
				Name:              "Tech Lead",
				Instructions:      agent.Instructions{SystemPrompt: "TECH_LEAD_SYSTEM_PROMPT"},
				Capabilities:      agent.Capabilities{Tools: []string{"lead_tool"}},
				ContextManagement: agent.DefaultContextManagement(),
			}

			registry := agent.NewRegistry()
			registry.Register(plannerManifest)
			registry.Register(techLeadManifest)

			eng := engine.New(engine.Config{
				ChatProvider:  gp,
				ChainStore:    chain,
				AgentRegistry: registry,
				Manifest:      *plannerManifest,
				Tools: []tool.Tool{
					&mockTool{name: "plan_tool", description: "planner tool"},
					&mockTool{name: "lead_tool", description: "tech-lead tool"},
				},
			})
			eng.SetContextStore(newTempContextStore("xsess-mfst-multi"), "session-multi")

			// Pre-arm both provider gates so neither call drains
			// before the other has been captured.
			gateP := gp.armGate(0)
			gateL := gp.armGate(1)

			ctxPlanner := context.WithValue(context.Background(), session.IDKey{}, "session-planner")
			ctxLead := context.WithValue(context.Background(), session.IDKey{}, "session-lead")

			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				chunks, err := eng.Stream(ctxPlanner, "planner", "plan it")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain
				}
			}()
			gp.waitForCaptures(1)
			go func() {
				defer wg.Done()
				chunks, err := eng.Stream(ctxLead, "tech-lead", "lead it")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain
				}
			}()
			gp.waitForCaptures(2)

			// Drain both provider streams. The two detached
			// streamWithToolLoop goroutines now race into
			// dualWriteToChainStore. The chain-store gate parks
			// both — release once both have arrived.
			close(gateP)
			close(gateL)

			chain.waitParked()
			chain.release()
			wg.Wait()

			appends := chain.snapshot()
			Expect(appends).To(HaveLen(2), "both streams must have appended one assistant message")

			ids := []string{appends[0].agentID, appends[1].agentID}
			Expect(ids).To(ConsistOf("planner", "tech-lead"),
				"each stream's chain-store append must carry its own agent ID; pre-fix both stamp whichever manifest most recently won SetManifest")
		})
	})
})
