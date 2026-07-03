package engine_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/todo"
)

// overflowScriptedProvider emits a scripted sequence of turns. Turns with
// contextOverflow == true emit a Done chunk carrying a
// provider.ErrorTypeContextWindowExceeded error, modelling what the engine
// sees when either the proactive overflow gate fires or the upstream provider
// rejects the request with a 400/context-length error.
type overflowScriptedProvider struct {
	name   string
	script []overflowProviderTurn
	onCall func(int)

	mu    sync.Mutex
	calls int
}

type overflowProviderTurn struct {
	content         string
	contextOverflow bool
}

type rateLimitSpec struct {
	provider string
	model    string
	retryAt  time.Time
}

func (p *overflowScriptedProvider) Name() string { return p.name }

func (p *overflowScriptedProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()
	if p.onCall != nil {
		p.onCall(idx + 1)
	}

	ch := make(chan provider.StreamChunk, 4)
	go func() {
		defer close(ch)
		if idx >= len(p.script) {
			ch <- provider.StreamChunk{Content: "All done.", Done: true}
			return
		}
		turn := p.script[idx]
		if turn.contextOverflow {
			ch <- provider.StreamChunk{
				Done:  true,
				Error: &provider.Error{ErrorType: provider.ErrorTypeContextWindowExceeded},
			}
			return
		}
		if turn.content != "" {
			ch <- provider.StreamChunk{Content: turn.content}
		}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch, nil
}

func (p *overflowScriptedProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *overflowScriptedProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

func (p *overflowScriptedProvider) Models() ([]provider.Model, error) { return nil, nil }

func (p *overflowScriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func newTestFailoverManager(prefs []provider.ModelPreference, limits []rateLimitSpec) *failover.Manager {
	registry := provider.NewRegistry()
	health := failover.NewHealthManager()
	mgr := failover.NewManager(registry, health, time.Second)
	mgr.SetBasePreferences(prefs)
	for _, limit := range limits {
		health.MarkRateLimited(limit.provider, limit.model, limit.retryAt)
	}
	return mgr
}

func hasEventType(chunks []provider.StreamChunk, eventType string) bool {
	for _, chunk := range chunks {
		if chunk.EventType == eventType {
			return true
		}
	}
	return false
}

func hasContentContaining(chunks []provider.StreamChunk, fragment string) bool {
	for _, chunk := range chunks {
		if strings.Contains(chunk.Content, fragment) {
			return true
		}
	}
	return false
}

var _ = Describe("Engine context-window overflow recovery", func() {
	var (
		manifest  agent.Manifest
		todoStore *todo.MemoryStore
		sessionID string
	)

	BeforeEach(func() {
		manifest = agent.Manifest{
			ID:   "overflow-agent",
			Name: "Overflow Agent",
			Capabilities: agent.Capabilities{
				Tools: []string{"echo", "todowrite"},
			},
		}
		todoStore = todo.NewMemoryStore()
		sessionID = "test-session-overflow"
	})

	Context("when the provider returns a context-window-exceeded error", func() {
		It("truncates and retries the provider", func() {
			prov := &overflowScriptedProvider{
				name: "overflow-prov",
				script: []overflowProviderTurn{
					{contextOverflow: true},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})
			eng.SetTodoStoreForTest(todoStore)

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
			chunks, err := eng.Stream(ctx, sessionID, "Go")
			Expect(err).NotTo(HaveOccurred())

			_, closed := drain(chunks)
			Expect(closed).To(BeTrue(), "channel must close after overflow")

			Expect(prov.callCount()).To(Equal(2),
				"engine must truncate and retry the provider when no compactor is configured")
		})

		It("still triggers todo-continuation after overflow recovery when pending todos exist", func() {
			prov := &overflowScriptedProvider{
				name: "overflow-todo-prov",
				script: []overflowProviderTurn{
					{contextOverflow: true},
				},
			}

			todoStore.Set(sessionID, []todo.Item{
				{Content: "deploy the release", Status: "pending", Priority: "high"},
			})

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})
			eng.SetTodoStoreForTest(todoStore)

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
			chunks, err := eng.Stream(ctx, sessionID, "Go")
			Expect(err).NotTo(HaveOccurred())

			_, closed := drain(chunks)
			Expect(closed).To(BeTrue(), "channel must close")

			Expect(prov.callCount()).To(Equal(5),
				"todo-continuation should continue after overflow recovery when pending todos remain")
		})

		It("does not fire todo-continuation when todoStore is nil", func() {
			prov := &overflowScriptedProvider{
				name: "overflow-nil-store-prov",
				script: []overflowProviderTurn{
					{contextOverflow: true},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
			chunks, err := eng.Stream(ctx, sessionID, "Go")
			Expect(err).NotTo(HaveOccurred())

			_, closed := drain(chunks)
			Expect(closed).To(BeTrue())
			Expect(prov.callCount()).To(Equal(2))
		})
	})

	Context("when the turn ends cleanly (no overflow) with pending todos", func() {
		It("triggers todo-continuation and retries indefinitely", func() {
			prov := &overflowScriptedProvider{
				name: "normal-with-todos-prov",
				script: []overflowProviderTurn{
					{content: "Working on it..."},
					{content: "Still working..."},
				},
			}

			todoStore.Set(sessionID, []todo.Item{
				{Content: "write the report", Status: "pending", Priority: "high"},
			})

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})
			eng.SetTodoStoreForTest(todoStore)

			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			ctx = context.WithValue(ctx, session.IDKey{}, sessionID)
			chunks, err := eng.Stream(ctx, sessionID, "Go")
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() int { return prov.callCount() }, "3s", "100ms").Should(
				BeNumerically(">=", 4),
				"todo-continuation must fire repeatedly on clean turn ends with pending todos",
			)

			cancel()

			_, closed := drain(chunks)
			Expect(closed).To(BeTrue(),
				"channel must close after context cancellation")
		})

		It("stops after three no-progress continuations", func() {
			prov := &overflowScriptedProvider{
				name: "stuck-with-todos-prov",
				script: []overflowProviderTurn{
					{content: "Working on it..."},
					{content: "Still working..."},
					{content: "Still working..."},
					{content: "Still working..."},
					{content: "Still working..."},
				},
			}

			todoStore.Set(sessionID, []todo.Item{
				{Content: "write the report", Status: "pending", Priority: "high"},
			})

			failoverMgr := newTestFailoverManager(
				[]provider.ModelPreference{{Provider: "healthy-provider", Model: "healthy-model"}},
				nil,
			)

			eng := engine.New(engine.Config{
				ChatProvider:    prov,
				Manifest:        manifest,
				Tools:           []tool.Tool{},
				FailoverManager: failoverMgr,
			})
			eng.SetTodoStoreForTest(todoStore)

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
			chunks, err := eng.Stream(ctx, sessionID, "Go")
			Expect(err).NotTo(HaveOccurred())

			_, closed := drain(chunks)
			Expect(closed).To(BeTrue(), "channel must close after no-progress continuation gate trips")
			Expect(prov.callCount()).To(Equal(4), "engine must stop after three no-progress continuations")
		})
	})

	Context("when all providers are rate-limited", func() {
		It("retries after cooldown when all providers are rate-limited", func() {
			health := failover.NewHealthManager()
			failoverMgr := failover.NewManager(provider.NewRegistry(), health, time.Second)
			failoverMgr.SetBasePreferences([]provider.ModelPreference{{Provider: "rate-limited-provider", Model: "rate-limited-model"}})
			prov := &overflowScriptedProvider{
				name: "rate-limited-prov",
				script: []overflowProviderTurn{
					{content: "Working on it..."},
					{content: "Still working..."},
				},
				onCall: func(call int) {
					if call == 4 {
						health.MarkRateLimited("rate-limited-provider", "rate-limited-model", time.Now().Add(1*time.Second))
					}
				},
			}

			todoStore.Set(sessionID, []todo.Item{
				{Content: "write the report", Status: "pending", Priority: "high"},
			})

			eng := engine.New(engine.Config{
				ChatProvider:    prov,
				Manifest:        manifest,
				Tools:           []tool.Tool{},
				FailoverManager: failoverMgr,
			})
			eng.SetTodoStoreForTest(todoStore)

			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			ctx = context.WithValue(ctx, session.IDKey{}, sessionID)
			chunks, err := eng.Stream(ctx, sessionID, "Go")
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() int { return prov.callCount() }, "5s", "50ms").Should(BeNumerically(">", 4))
			cancel()

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue())
			Expect(hasEventType(received, "provider_retry_scheduled")).To(BeTrue())
			Expect(hasContentContaining(received, "All providers unavailable. Retrying in")).To(BeTrue())
		})

		It("completes with a retry-too-far message when cooldown exceeds the maximum", func() {
			health := failover.NewHealthManager()
			failoverMgr := failover.NewManager(provider.NewRegistry(), health, time.Second)
			failoverMgr.SetBasePreferences([]provider.ModelPreference{{Provider: "too-far-provider", Model: "too-far-model"}})
			prov := &overflowScriptedProvider{
				name: "too-far-prov",
				script: []overflowProviderTurn{
					{content: "Working on it..."},
					{content: "Still working..."},
				},
				onCall: func(call int) {
					if call == 4 {
						health.MarkRateLimited("too-far-provider", "too-far-model", time.Now().Add(24*time.Hour))
					}
				},
			}

			todoStore.Set(sessionID, []todo.Item{
				{Content: "write the report", Status: "pending", Priority: "high"},
			})

			eng := engine.New(engine.Config{
				ChatProvider:    prov,
				Manifest:        manifest,
				Tools:           []tool.Tool{},
				FailoverManager: failoverMgr,
			})
			eng.SetTodoStoreForTest(todoStore)

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
			chunks, err := eng.Stream(ctx, sessionID, "Go")
			Expect(err).NotTo(HaveOccurred())

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue())
			Expect(prov.callCount()).To(Equal(4))
			Expect(hasEventType(received, "provider_retry_too_far")).To(BeTrue())
			Expect(hasContentContaining(received, "All providers unavailable until")).To(BeTrue())
		})
	})

	Context("when the provider alternates overflow then success", func() {
		It("retries after overflow when a compactor fires and succeeds", func() {
			prov := &overflowScriptedProvider{
				name: "overflow-then-ok-prov",
				script: []overflowProviderTurn{
					{contextOverflow: true},
					{content: "Recovered successfully."},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})
			eng.SetTodoStoreForTest(todoStore)

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
			chunks, err := eng.Stream(ctx, sessionID, "Go")
			Expect(err).NotTo(HaveOccurred())

			_, closed := drain(chunks)
			Expect(closed).To(BeTrue(), "channel must close after overflow + retry")
		})
	})
})

var _ = Describe("Engine naive truncation fallback when summariser is unavailable", func() {
	It("truncates to system + placeholder + hot tail when summariser fails", func() {
		summariser := &recordingSummariser{err: context.DeadlineExceeded}
		eng, _ := newTestEngineWithCompactor(summariser, 0.60, true)

		priorMsgs := make([]provider.Message, 61)
		expectedTail := make([]provider.Message, 50)
		for i := range priorMsgs {
			content := fmt.Sprintf("history-%02d token", i+1)
			priorMsgs[i] = provider.Message{Role: "assistant", Content: content}
			if i >= len(priorMsgs)-len(expectedTail) {
				expectedTail[i-(len(priorMsgs)-len(expectedTail))] = provider.Message{Role: "assistant", Content: content}
			}
		}

		ctx := session.WithPriorMessages(context.Background(), priorMsgs)
		messages := eng.BuildContextWindowForTest(ctx, "truncate-session", "next user turn")

		Expect(messages).To(HaveLen(53))
		Expect(messages[0].Role).To(Equal("system"))
		Expect(messages[0].Content).To(ContainSubstring("sys"))
		Expect(messages[1].Content).To(ContainSubstring("[truncation fallback"))
		Expect(messages[2:52]).To(Equal(expectedTail))
		Expect(messages[52]).To(Equal(provider.Message{Role: "user", Content: "next user turn"}))
	})

	It("overflow recovery truncates and retries instead of giving up", func() {
		summariser := &recordingSummariser{err: context.DeadlineExceeded}
		prov := &overflowScriptedProvider{
			name: "overflow-truncation-prov",
			script: []overflowProviderTurn{
				{contextOverflow: true},
				{content: "Recovered after truncation."},
			},
		}

		tempDir := GinkgoT().TempDir()
		store, err := recall.NewFileContextStore(tempDir+"/ctx.json", "test-model")
		Expect(err).NotTo(HaveOccurred())
		for range 6 {
			seedMessages(store)
		}

		cfg := ctxstore.DefaultCompressionConfig()
		cfg.AutoCompaction.Enabled = true
		cfg.AutoCompaction.Threshold = 0.60

		cm := agent.DefaultContextManagement()
		cm.CompactionThreshold = 0

		eng := engine.New(engine.Config{
			ChatProvider:      prov,
			Manifest:          agent.Manifest{ID: "overflow-truncation-agent", Name: "Overflow Truncation Agent", Instructions: agent.Instructions{SystemPrompt: "sys"}, ContextManagement: cm},
			Store:             store,
			TokenCounter:      &wordTokenCounter{limit: 100},
			AutoCompactor:     ctxstore.NewAutoCompactor(summariser),
			CompressionConfig: cfg,
		})

		ctx := context.WithValue(context.Background(), session.IDKey{}, "overflow-truncation-session")
		chunks, streamErr := eng.Stream(ctx, "overflow-truncation-session", "Go")
		Expect(streamErr).NotTo(HaveOccurred())

		received, closed := drain(chunks)
		Expect(closed).To(BeTrue())
		Expect(prov.callCount()).To(Equal(2))
		Expect(hasContentContaining(received, "Recovered after truncation.")).To(BeTrue())
	})
})
