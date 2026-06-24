package engine_test

import (
	"context"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/todo"
)

// scriptedTodoProvider drives a configurable sequence of provider responses.
// Each element of script is emitted in order; once the script is exhausted the
// provider returns a clean text Done on every subsequent call. This models the
// scenario where the model stops early several times before finally completing
// all outstanding work.
type scriptedTodoProvider struct {
	name   string
	script []todoProviderTurn

	mu    sync.Mutex
	calls int
}

type todoProviderTurn struct {
	content    string
	toolCalls  []*provider.ToolCall
	stopReason string
}

func (p *scriptedTodoProvider) Name() string { return p.name }

func (p *scriptedTodoProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()

	ch := make(chan provider.StreamChunk, 8)
	go func() {
		defer close(ch)
		if idx >= len(p.script) {
			ch <- provider.StreamChunk{Content: "All tasks complete.", Done: true}
			return
		}
		turn := p.script[idx]
		if turn.content != "" {
			ch <- provider.StreamChunk{Content: turn.content}
		}
		for _, tc := range turn.toolCalls {
			tcCopy := *tc
			ch <- provider.StreamChunk{EventType: "tool_call", ToolCall: &tcCopy}
		}
		chunk := provider.StreamChunk{Done: true}
		if turn.stopReason != "" {
			chunk.StopReason = turn.stopReason
		}
		ch <- chunk
	}()
	return ch, nil
}

func (p *scriptedTodoProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *scriptedTodoProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

func (p *scriptedTodoProvider) Models() ([]provider.Model, error) { return nil, nil }

func (p *scriptedTodoProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func drain(chunks <-chan provider.StreamChunk) ([]provider.StreamChunk, bool) {
	var received []provider.StreamChunk
	done := make(chan struct{})
	go func() {
		defer close(done)
		for c := range chunks {
			received = append(received, c)
		}
	}()
	select {
	case <-done:
		return received, true
	case <-time.After(5 * time.Second):
		return received, false
	}
}

var _ = Describe("Engine todo-completion continuation", func() {
	var (
		manifest  agent.Manifest
		todoStore *todo.MemoryStore
		sessionID string
	)

	BeforeEach(func() {
		manifest = agent.Manifest{
			ID:   "todo-agent",
			Name: "Todo Agent",
			Capabilities: agent.Capabilities{
				Tools: []string{"echo", "todowrite"},
			},
		}
		todoStore = todo.NewMemoryStore()
		sessionID = "test-session-todo"
	})

	Context("when the model ends a turn cleanly with pending todos", func() {
		It("injects a continuation prompt and retries indefinitely", func() {
			prov := &scriptedTodoProvider{
				name: "todo-prov",
				script: []todoProviderTurn{
					{content: "I'll start..."},
					{content: "Almost done..."},
				},
			}

			todoStore.Set(sessionID, []todo.Item{
				{Content: "finish step one", Status: "pending", Priority: "high"},
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
				BeNumerically(">=", 6),
				"engine must retry past the old retry limit when todos remain incomplete",
			)

			cancel()

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue(),
				"channel must close after context cancellation")

			var hasCompletionContent bool
			for _, c := range received {
				if c.Content != "" && strings.Contains(c.Content, "All tasks complete.") {
					hasCompletionContent = true
				}
			}
			Expect(hasCompletionContent).To(BeTrue(),
				"engine should have retried and received the provider's default completion response")
		})
	})

	Context("when there are no todos in the session", func() {
		It("completes normally on the first turn without retrying", func() {
			prov := &scriptedTodoProvider{
				name:   "no-todo-prov",
				script: []todoProviderTurn{},
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

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue())

			var terminalDone *provider.StreamChunk
			for i := range received {
				if received[i].Done {
					terminalDone = &received[i]
				}
			}
			Expect(terminalDone).NotTo(BeNil())

			Expect(prov.callCount()).To(Equal(1),
				"no retries needed when the todo list is empty")
		})
	})

	Context("when all todos are completed or cancelled", func() {
		It("completes normally without retrying", func() {
			prov := &scriptedTodoProvider{
				name:   "done-todo-prov",
				script: []todoProviderTurn{},
			}

			todoStore.Set(sessionID, []todo.Item{
				{Content: "step one", Status: "completed", Priority: "high"},
				{Content: "step two", Status: "cancelled", Priority: "low"},
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
			Expect(closed).To(BeTrue())

			Expect(prov.callCount()).To(Equal(1),
				"no retries when all todos are terminal")
		})
	})

	Context("when the model never completes its todos", func() {
		It("keeps retrying indefinitely until the context is cancelled", func() {
			prov := &scriptedTodoProvider{
				name: "stubborn-prov",
				script: []todoProviderTurn{
					{content: "nope 1"},
					{content: "nope 2"},
					{content: "nope 3"},
					{content: "nope 4"},
					{content: "nope 5"},
				},
			}

			todoStore.Set(sessionID, []todo.Item{
				{Content: "never done", Status: "pending", Priority: "high"},
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
				BeNumerically(">=", 6),
				"engine must keep retrying indefinitely, not stop at the old retry limit",
			)

			cancel()

			_, closed := drain(chunks)
			Expect(closed).To(BeTrue(),
				"channel must close after context cancellation")
		})
	})

	Context("when todoStore is nil", func() {
		It("completes normally without any todo checking", func() {
			prov := &scriptedTodoProvider{
				name:   "nil-store-prov",
				script: []todoProviderTurn{},
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

			Expect(prov.callCount()).To(Equal(1),
				"no retry logic fires when todoStore is nil")
		})
	})

	Context("when TodoStore is wired through Config (as createDelegateEngine does)", func() {
		It("retries when todos are incomplete using the Config-wired store", func() {
			configStore := todo.NewMemoryStore()
			configStore.Set(sessionID, []todo.Item{
				{Content: "finish step one", Status: "pending", Priority: "high"},
			})

			prov := &scriptedTodoProvider{
				name: "config-store-prov",
				script: []todoProviderTurn{
					{content: "Starting..."},
					{content: "Still working..."},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{},
				TodoStore:    configStore,
			})
			// Deliberately do NOT call SetTodoStoreForTest —
			// the store must come from Config, exactly as
			// createDelegateEngine does.

			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			ctx = context.WithValue(ctx, session.IDKey{}, sessionID)
			chunks, err := eng.Stream(ctx, sessionID, "Go")
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() int { return prov.callCount() }, "3s", "100ms").Should(
				BeNumerically(">=", 6),
				"engine must retry past the old limit when TodoStore comes from Config",
			)

			cancel()

			_, closed := drain(chunks)
			Expect(closed).To(BeTrue(),
				"channel must close after context cancellation")

			// Verify the store in Config was actually used: the
			// todos must still be present (engine never completed them)
			items := configStore.Get(sessionID)
			Expect(items).To(HaveLen(1),
				"incomplete todo must remain in the store")
			Expect(items[0].Status).To(Equal("pending"),
				"todo must still be pending after retries exhausted")
		})
	})
})
