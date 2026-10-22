package engine_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/todo"
)

var _ = Describe("Engine todo context injection", func() {
	var (
		manifest  agent.Manifest
		todoStore *todo.MemoryStore
		sessionID string
	)

	BeforeEach(func() {
		manifest = agent.Manifest{
			ID:   "todo-ctx-agent",
			Name: "Todo Ctx Agent",
			Capabilities: agent.Capabilities{
				Tools: []string{"echo", "todowrite"},
			},
		}
		todoStore = todo.NewMemoryStore()
		sessionID = "test-session-todo-ctx"
	})

	Context("when the session has todos in the store", func() {
		It("injects the current todo list into the context window", func() {
			todoStore.Set(sessionID, []todo.Item{
				{Content: "search vaults", Status: "in_progress", Priority: "high"},
				{Content: "write findings", Status: "pending", Priority: "medium"},
			})

			eng := engine.New(engine.Config{
				ChatProvider: &todoCtxNoopProvider{},
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})
			eng.SetTodoStoreForTest(todoStore)

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
			msgs := eng.BuildContextWindowForTest(ctx, sessionID, "go")

			Expect(msgs).NotTo(BeEmpty())

			found := false
			for _, msg := range msgs {
				if msg.Role == "system" && strings.Contains(msg.Content, "Current Task List") {
					found = true
					Expect(msg.Content).To(ContainSubstring("search vaults"))
					Expect(msg.Content).To(ContainSubstring("write findings"))
					Expect(msg.Content).To(ContainSubstring("[~]"))
					Expect(msg.Content).To(ContainSubstring("[ ]"))
				}
			}
			Expect(found).To(BeTrue(),
				"context window must contain a system message with the current todo list")
		})

		It("places the todo message after the main system prompt", func() {
			todoStore.Set(sessionID, []todo.Item{
				{Content: "task one", Status: "pending", Priority: "low"},
			})

			eng := engine.New(engine.Config{
				ChatProvider: &todoCtxNoopProvider{},
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})
			eng.SetTodoStoreForTest(todoStore)

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
			msgs := eng.BuildContextWindowForTest(ctx, sessionID, "go")

			Expect(msgs).To(HaveLen(3))
			Expect(msgs[0].Role).To(Equal("system"))
			Expect(msgs[1].Role).To(Equal("system"))
			Expect(msgs[1].Content).To(ContainSubstring("Current Task List"))
			Expect(msgs[2].Role).To(Equal("user"))
		})

		It("reflects the latest store state even after mid-session updates", func() {
			todoStore.Set(sessionID, []todo.Item{
				{Content: "old task", Status: "pending", Priority: "low"},
			})

			eng := engine.New(engine.Config{
				ChatProvider: &todoCtxNoopProvider{},
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})
			eng.SetTodoStoreForTest(todoStore)

			todoStore.Set(sessionID, []todo.Item{
				{Content: "new task", Status: "in_progress", Priority: "high"},
			})

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
			msgs := eng.BuildContextWindowForTest(ctx, sessionID, "go")

			for _, msg := range msgs {
				if msg.Role == "system" && strings.Contains(msg.Content, "Current Task List") {
					Expect(msg.Content).To(ContainSubstring("new task"))
					Expect(msg.Content).NotTo(ContainSubstring("old task"))
				}
			}
		})
	})

	Context("when the session has no todos", func() {
		It("does not inject a todo system message", func() {
			eng := engine.New(engine.Config{
				ChatProvider: &todoCtxNoopProvider{},
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})
			eng.SetTodoStoreForTest(todoStore)

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
			msgs := eng.BuildContextWindowForTest(ctx, sessionID, "go")

			for _, msg := range msgs {
				Expect(msg.Content).NotTo(ContainSubstring("Current Task List"),
					"empty sessions should not get a todo injection")
			}
		})
	})

	Context("when all todos are completed or cancelled", func() {
		It("still injects the list so the model can see the terminal state", func() {
			todoStore.Set(sessionID, []todo.Item{
				{Content: "done task", Status: "completed", Priority: "high"},
				{Content: "skipped task", Status: "cancelled", Priority: "low"},
			})

			eng := engine.New(engine.Config{
				ChatProvider: &todoCtxNoopProvider{},
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})
			eng.SetTodoStoreForTest(todoStore)

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
			msgs := eng.BuildContextWindowForTest(ctx, sessionID, "go")

			found := false
			for _, msg := range msgs {
				if msg.Role == "system" && strings.Contains(msg.Content, "Current Task List") {
					found = true
					Expect(msg.Content).To(ContainSubstring("[x]"))
					Expect(msg.Content).To(ContainSubstring("[-]"))
				}
			}
			Expect(found).To(BeTrue(),
				"completed/cancelled lists should still be visible so the model knows the work is done")
		})
	})

	Context("when todoStore is nil", func() {
		It("does not inject anything", func() {
			eng := engine.New(engine.Config{
				ChatProvider: &todoCtxNoopProvider{},
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
			msgs := eng.BuildContextWindowForTest(ctx, sessionID, "go")

			for _, msg := range msgs {
				Expect(msg.Content).NotTo(ContainSubstring("Current Task List"))
			}
		})
	})
})

type todoCtxNoopProvider struct{}

func (p *todoCtxNoopProvider) Name() string { return "noop-ctx" }
func (p *todoCtxNoopProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: "ok", Done: true}
	}()
	return ch, nil
}
func (p *todoCtxNoopProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (p *todoCtxNoopProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1}, nil
}
func (p *todoCtxNoopProvider) Models() ([]provider.Model, error) { return nil, nil }
