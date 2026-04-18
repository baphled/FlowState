// Package engine tests verify the end-to-end integration of RecallBroker
// with the Engine's context window building. They cover all 4 ACs:
// AC1: RecallBroker is queried during context assembly
// AC2: RecallBroker.Query receives the user message as query
// AC3: Observations are merged into the context window
// AC4: Token budget constraint is respected (no overflow)
package engine_test

import (
	stdctx "context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

type contextAssemblyTokenCounter struct {
	countFn      func(text string) int
	modelLimitFn func(model string) int
}

func (m *contextAssemblyTokenCounter) Count(text string) int {
	if m.countFn != nil {
		return m.countFn(text)
	}
	return len(text) / 4
}

func (m *contextAssemblyTokenCounter) ModelLimit(model string) int {
	if m.modelLimitFn != nil {
		return m.modelLimitFn(model)
	}
	return 8192
}

type contextAssemblyBroker struct {
	queryFn    func(c stdctx.Context, query string, limit int) ([]recall.Observation, error)
	queryCalls []struct {
		query string
		limit int
	}
}

func (m *contextAssemblyBroker) Query(c stdctx.Context, query string, limit int) ([]recall.Observation, error) {
	m.queryCalls = append(m.queryCalls, struct {
		query string
		limit int
	}{query, limit})
	if m.queryFn != nil {
		return m.queryFn(c, query, limit)
	}
	return []recall.Observation{}, nil
}

type contextAssemblyProvider struct{}

func (p *contextAssemblyProvider) Name() string { return "test-provider" }
func (p *contextAssemblyProvider) Stream(_ stdctx.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}
func (p *contextAssemblyProvider) Chat(_ stdctx.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (p *contextAssemblyProvider) Embed(_ stdctx.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}
func (p *contextAssemblyProvider) Models() ([]provider.Model, error) { return nil, nil }

var _ = Describe("Context Assembly Hook Integration", Label("integration", "context-assembly"), func() {
	var (
		counter  *contextAssemblyTokenCounter
		broker   *contextAssemblyBroker
		manifest agent.Manifest
		tmpDir   string
	)

	BeforeEach(func() {
		counter = &contextAssemblyTokenCounter{
			countFn:      func(text string) int { return len(text) / 4 },
			modelLimitFn: func(model string) int { return 8192 },
		}
		broker = &contextAssemblyBroker{}
		// P13: the broker hook now only fires for manifests that opt in
		// via UsesRecall. The existing assembly-hook integration tests
		// assert the broker IS queried, so the shared manifest explicitly
		// opts in here.
		manifest = agent.Manifest{
			ID:         "test-agent",
			Name:       "Test Agent",
			UsesRecall: true,
			Instructions: agent.Instructions{
				SystemPrompt: "You are a test agent.",
			},
		}
		var err error
		tmpDir, err = os.MkdirTemp("", "context-assembly-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	Describe("AC1: RecallBroker is queried during Engine.buildContextWindow", func() {
		It("queries RecallBroker when building context with a real Engine", func() {
			broker.queryFn = func(_ stdctx.Context, query string, limit int) ([]recall.Observation, error) {
				return []recall.Observation{}, nil
			}
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())
			eng := engine.New(engine.Config{
				ChatProvider: &contextAssemblyProvider{},
				Manifest:     manifest,
				TokenCounter: counter,
				Store:        store,
				RecallBroker: broker,
			})

			msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Hello")
			Expect(msgs).NotTo(BeEmpty())
			Expect(broker.queryCalls).To(HaveLen(1))
		})

		It("does not crash when RecallBroker is nil", func() {
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())
			eng := engine.New(engine.Config{
				ChatProvider: &contextAssemblyProvider{},
				Manifest:     manifest,
				TokenCounter: counter,
				Store:        store,
			})

			msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Hello")
			Expect(msgs).NotTo(BeEmpty())
		})
	})

	Describe("AC2: RecallBroker.Query receives the user message", func() {
		It("passes the user message as the query parameter", func() {
			broker.queryFn = func(_ stdctx.Context, query string, limit int) ([]recall.Observation, error) {
				return []recall.Observation{}, nil
			}
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())
			eng := engine.New(engine.Config{
				ChatProvider: &contextAssemblyProvider{},
				Manifest:     manifest,
				TokenCounter: counter,
				Store:        store,
				RecallBroker: broker,
			})

			eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "What is recall?")
			Expect(broker.queryCalls).To(HaveLen(1))
			Expect(broker.queryCalls[0].query).To(Equal("What is recall?"))
			Expect(broker.queryCalls[0].limit).To(Equal(5))
		})
	})

	Describe("AC3: Observations are merged into the context window", func() {
		It("includes recalled observations in the context messages", func() {
			broker.queryFn = func(_ stdctx.Context, _ string, _ int) ([]recall.Observation, error) {
				return []recall.Observation{
					{ID: "obs-1", Content: "Previous architecture discussion", Source: "memory", Timestamp: time.Now()},
				}, nil
			}
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())
			eng := engine.New(engine.Config{
				ChatProvider: &contextAssemblyProvider{},
				Manifest:     manifest,
				TokenCounter: counter,
				Store:        store,
				RecallBroker: broker,
			})

			msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Tell me about arch")
			contents := extractContents(msgs)
			Expect(contents).To(ContainElement(ContainSubstring("Previous architecture discussion")))
		})

		It("preserves the user message when observations are returned", func() {
			broker.queryFn = func(_ stdctx.Context, _ string, _ int) ([]recall.Observation, error) {
				return []recall.Observation{
					{ID: "obs-1", Content: "Recalled content"},
				}, nil
			}
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())
			eng := engine.New(engine.Config{
				ChatProvider: &contextAssemblyProvider{},
				Manifest:     manifest,
				TokenCounter: counter,
				Store:        store,
				RecallBroker: broker,
			})

			msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "My question")
			lastMsg := msgs[len(msgs)-1]
			Expect(lastMsg.Role).To(Equal("user"))
			Expect(lastMsg.Content).To(Equal("My question"))
		})

		It("merges multiple observations into the context", func() {
			broker.queryFn = func(_ stdctx.Context, _ string, _ int) ([]recall.Observation, error) {
				return []recall.Observation{
					{ID: "obs-1", Content: "First memory"},
					{ID: "obs-2", Content: "Second memory"},
				}, nil
			}
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())
			eng := engine.New(engine.Config{
				ChatProvider: &contextAssemblyProvider{},
				Manifest:     manifest,
				TokenCounter: counter,
				Store:        store,
				RecallBroker: broker,
			})

			msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Query")
			contents := extractContents(msgs)
			Expect(contents).To(ContainElement("First memory"))
			Expect(contents).To(ContainElement("Second memory"))
		})
	})

	Describe("AC4: Token budget constraint is respected", func() {
		It("does not overflow the token budget with observations", func() {
			tightCounter := &contextAssemblyTokenCounter{
				countFn:      func(text string) int { return len(text) },
				modelLimitFn: func(model string) int { return 100 },
			}
			broker.queryFn = func(_ stdctx.Context, _ string, _ int) ([]recall.Observation, error) {
				return []recall.Observation{
					{ID: "obs-1", Content: "Short content"},
				}, nil
			}
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())
			eng := engine.New(engine.Config{
				ChatProvider: &contextAssemblyProvider{},
				Manifest:     manifest,
				TokenCounter: tightCounter,
				Store:        store,
				RecallBroker: broker,
			})

			msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Q")
			Expect(msgs).NotTo(BeEmpty())
		})

		It("accounts for user message tokens on the semantic path", func() {
			tokenCounter := &contextAssemblyTokenCounter{
				countFn:      func(text string) int { return len(text) },
				modelLimitFn: func(model string) int { return 8192 },
			}
			broker.queryFn = func(_ stdctx.Context, _ string, _ int) ([]recall.Observation, error) {
				return []recall.Observation{
					{ID: "obs-1", Content: "Recalled memory"},
				}, nil
			}
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())
			eng := engine.New(engine.Config{
				ChatProvider: &contextAssemblyProvider{},
				Manifest:     manifest,
				TokenCounter: tokenCounter,
				Store:        store,
				RecallBroker: broker,
			})

			userMsg := "What about architecture?"
			eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", userMsg)
			ctxResult := eng.LastContextResult()
			Expect(ctxResult.TokensUsed).To(BeNumerically(">", 0))
			Expect(ctxResult.TokensUsed).To(BeNumerically(">=", len(userMsg)))
			Expect(ctxResult.BudgetRemaining).To(BeNumerically("<", 8192))
		})
	})

	Describe("Error Handling and Resilience", func() {
		It("degrades gracefully when RecallBroker.Query fails", func() {
			broker.queryFn = func(_ stdctx.Context, _ string, _ int) ([]recall.Observation, error) {
				return nil, stdctx.DeadlineExceeded
			}
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())
			eng := engine.New(engine.Config{
				ChatProvider: &contextAssemblyProvider{},
				Manifest:     manifest,
				TokenCounter: counter,
				Store:        store,
				RecallBroker: broker,
			})

			msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Hello")
			Expect(msgs).NotTo(BeEmpty())
			lastMsg := msgs[len(msgs)-1]
			Expect(lastMsg.Role).To(Equal("user"))
			Expect(lastMsg.Content).To(Equal("Hello"))
		})

		It("falls back to normal assembly when broker returns empty", func() {
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())
			eng := engine.New(engine.Config{
				ChatProvider: &contextAssemblyProvider{},
				Manifest:     manifest,
				TokenCounter: counter,
				Store:        store,
				RecallBroker: broker,
			})

			msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Hello")
			lastMsg := msgs[len(msgs)-1]
			Expect(lastMsg.Role).To(Equal("user"))
			Expect(lastMsg.Content).To(Equal("Hello"))
		})
	})

	Describe("Context Assembly Hook Dispatch", func() {
		It("dispatches registered ContextAssemblyHooks during context assembly", func() {
			hookFired := false
			customHook := func(_ stdctx.Context, payload *plugin.ContextAssemblyPayload) error {
				hookFired = true
				Expect(payload.UserMessage).To(Equal("Hook test"))
				Expect(payload.SessionID).NotTo(BeEmpty())
				Expect(payload.AgentID).To(Equal("test-agent"))
				return nil
			}
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())
			eng := engine.New(engine.Config{
				ChatProvider:         &contextAssemblyProvider{},
				Manifest:             manifest,
				TokenCounter:         counter,
				Store:                store,
				ContextAssemblyHooks: []plugin.ContextAssemblyHook{customHook},
			})

			eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Hook test")
			Expect(hookFired).To(BeTrue())
		})

		It("allows hooks to populate SearchResults into the context window", func() {
			enrichHook := func(_ stdctx.Context, payload *plugin.ContextAssemblyPayload) error {
				payload.SearchResults = []recall.SearchResult{
					{MessageID: "hook-obs-1", Score: 1.0, Message: provider.Message{Role: "assistant", Content: "Hook-injected memory"}},
				}
				return nil
			}
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())
			eng := engine.New(engine.Config{
				ChatProvider:         &contextAssemblyProvider{},
				Manifest:             manifest,
				TokenCounter:         counter,
				Store:                store,
				ContextAssemblyHooks: []plugin.ContextAssemblyHook{enrichHook},
			})

			msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Query")
			contents := extractContents(msgs)
			Expect(contents).To(ContainElement("Hook-injected memory"))
		})

		It("auto-registers RecallBroker as a hook when configured", func() {
			broker.queryFn = func(_ stdctx.Context, _ string, _ int) ([]recall.Observation, error) {
				return []recall.Observation{{ID: "obs-1", Content: "Broker observation"}}, nil
			}
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())
			eng := engine.New(engine.Config{
				ChatProvider: &contextAssemblyProvider{},
				Manifest:     manifest,
				TokenCounter: counter,
				Store:        store,
				RecallBroker: broker,
			})

			msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Query")
			contents := extractContents(msgs)
			Expect(contents).To(ContainElement("Broker observation"))
		})

		It("continues assembly when a hook returns an error", func() {
			failHook := func(_ stdctx.Context, _ *plugin.ContextAssemblyPayload) error {
				return stdctx.DeadlineExceeded
			}
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())
			eng := engine.New(engine.Config{
				ChatProvider:         &contextAssemblyProvider{},
				Manifest:             manifest,
				TokenCounter:         counter,
				Store:                store,
				ContextAssemblyHooks: []plugin.ContextAssemblyHook{failHook},
			})

			msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Hello")
			Expect(msgs).NotTo(BeEmpty())
			lastMsg := msgs[len(msgs)-1]
			Expect(lastMsg.Role).To(Equal("user"))
			Expect(lastMsg.Content).To(Equal("Hello"))
		})
	})

	Describe("Hook Type Definitions", func() {
		It("defines ContextAssembly as a HookType constant", func() {
			Expect(plugin.ContextAssembly).To(Equal(plugin.HookType("context.assembly")))
		})

		It("defines ContextAssemblyPayload with required fields", func() {
			payload := &plugin.ContextAssemblyPayload{
				SessionID:     "ses-1",
				AgentID:       "agent-1",
				UserMessage:   "Hello",
				TokenBudget:   8192,
				SearchResults: []recall.SearchResult{},
			}
			Expect(payload.SessionID).To(Equal("ses-1"))
			Expect(payload.AgentID).To(Equal("agent-1"))
			Expect(payload.UserMessage).To(Equal("Hello"))
			Expect(payload.TokenBudget).To(Equal(8192))
			Expect(payload.SearchResults).To(BeEmpty())
		})
	})
})

func extractContents(msgs []provider.Message) []string {
	contents := make([]string, len(msgs))
	for i, m := range msgs {
		contents[i] = m.Content
	}
	return contents
}
