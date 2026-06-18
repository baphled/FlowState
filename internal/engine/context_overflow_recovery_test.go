package engine_test

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
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

	mu    sync.Mutex
	calls int
}

type overflowProviderTurn struct {
	content         string
	contextOverflow bool
}

func (p *overflowScriptedProvider) Name() string { return p.name }

func (p *overflowScriptedProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()

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
		It("exits cleanly without retrying the provider", func() {
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

			Expect(prov.callCount()).To(Equal(1),
				"engine must not retry the provider when no compactor is configured")
		})

		It("does not trigger todo-continuation even when pending todos exist", func() {
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

			Expect(prov.callCount()).To(Equal(1),
				"todo-continuation must not fire when the turn ended with context overflow")
		})

		It("does not fire todo-continuation even when todoStore is nil", func() {
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
			Expect(prov.callCount()).To(Equal(1))
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
