package engine_test

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// blockingMockTool records execution timing to verify concurrency.
type blockingMockTool struct {
	name      string
	delay     time.Duration
	execCalled atomic.Bool
	startedAt  atomic.Int64 // UnixNano
	finishedAt atomic.Int64 // UnixNano
	result     tool.Result
}

func (t *blockingMockTool) Name() string        { return t.name }
func (t *blockingMockTool) Description() string { return t.name }
func (t *blockingMockTool) Schema() tool.Schema { return tool.Schema{} }
func (t *blockingMockTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	t.execCalled.Store(true)
	t.startedAt.Store(time.Now().UnixNano())
	if t.delay > 0 {
		time.Sleep(t.delay)
	}
	t.finishedAt.Store(time.Now().UnixNano())
	return t.result, nil
}

var _ = Describe("Engine parallel tool dispatch", func() {
	var (
		registry *tool.Registry
		manifest agent.Manifest
	)

	BeforeEach(func() {
		registry = tool.NewRegistry()
		manifest = agent.Manifest{ID: "parallel-test-agent"}
	})

	Context("when the model emits two tool calls in a single message", func() {
		It("executes both tools, not just the first", func() {
			toolA := &blockingMockTool{name: "tool_a", result: tool.Result{Output: "result_a"}}
			toolB := &blockingMockTool{name: "tool_b", result: tool.Result{Output: "result_b"}}

			registry.Register(toolA)
			registry.Register(toolB)
			registry.SetPermission("tool_a", tool.Allow)
			registry.SetPermission("tool_b", tool.Allow)

			chatProvider := &streamSequenceProvider{
				name: "parallel-dispatch",
				sequences: [][]provider.StreamChunk{
					// First response: model emits two tool calls in ONE sequence
					{
						{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "call_a", Name: "tool_a", Arguments: map[string]any{}}},
						{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "call_b", Name: "tool_b", Arguments: map[string]any{}}},
						{Done: true},
					},
					// Second response: final text after both results
					{
						{Content: "Both tools ran.", Done: true},
					},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Tools:        []tool.Tool{toolA, toolB},
				ToolRegistry: registry,
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "parallel-session", "Use both tools")
			Expect(err).NotTo(HaveOccurred())
			for range chunks {
			}

			Expect(toolA.execCalled.Load()).To(BeTrue(), "tool_a must have been called")
			Expect(toolB.execCalled.Load()).To(BeTrue(), "tool_b must have been called")
		})

		It("runs the two tools concurrently, not sequentially", func() {
			const delay = 80 * time.Millisecond

			toolA := &blockingMockTool{name: "tool_a", delay: delay, result: tool.Result{Output: "a"}}
			toolB := &blockingMockTool{name: "tool_b", delay: delay, result: tool.Result{Output: "b"}}

			registry.Register(toolA)
			registry.Register(toolB)
			registry.SetPermission("tool_a", tool.Allow)
			registry.SetPermission("tool_b", tool.Allow)

			chatProvider := &streamSequenceProvider{
				name: "parallel-timing",
				sequences: [][]provider.StreamChunk{
					{
						{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "call_a", Name: "tool_a", Arguments: map[string]any{}}},
						{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "call_b", Name: "tool_b", Arguments: map[string]any{}}},
						{Done: true},
					},
					{
						{Content: "Done.", Done: true},
					},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Tools:        []tool.Tool{toolA, toolB},
				ToolRegistry: registry,
			})

			start := time.Now()
			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "parallel-timing-session", "Use both tools")
			Expect(err).NotTo(HaveOccurred())
			for range chunks {
			}
			elapsed := time.Since(start)

			// Sequential execution takes ≥ 2*delay; parallel takes ≥ delay.
			// Allow 1.6× the single delay to tolerate scheduling jitter.
			Expect(toolA.execCalled.Load()).To(BeTrue())
			Expect(toolB.execCalled.Load()).To(BeTrue())
			Expect(elapsed).To(BeNumerically("<", 2*delay-10*time.Millisecond),
				"parallel execution should complete in ~1× delay, not 2×")
		})
	})

	Context("when the model emits three tool calls in one message", func() {
		It("executes all three", func() {
			var mu sync.Mutex
			var executedTools []string

			makeTracked := func(name string) tool.Tool {
				return &funcTool{
					name: name,
					execute: func(_ context.Context, _ tool.Input) (tool.Result, error) {
						mu.Lock()
						executedTools = append(executedTools, name)
						mu.Unlock()
						return tool.Result{Output: name + "-done"}, nil
					},
				}
			}

			toolA := makeTracked("alpha")
			toolB := makeTracked("beta")
			toolC := makeTracked("gamma")

			registry.Register(toolA)
			registry.Register(toolB)
			registry.Register(toolC)
			registry.SetPermission("alpha", tool.Allow)
			registry.SetPermission("beta", tool.Allow)
			registry.SetPermission("gamma", tool.Allow)

			chatProvider := &streamSequenceProvider{
				name: "three-parallel",
				sequences: [][]provider.StreamChunk{
					{
						{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "c1", Name: "alpha", Arguments: map[string]any{}}},
						{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "c2", Name: "beta", Arguments: map[string]any{}}},
						{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "c3", Name: "gamma", Arguments: map[string]any{}}},
						{Done: true},
					},
					{
						{Content: "All three done.", Done: true},
					},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Tools:        []tool.Tool{toolA, toolB, toolC},
				ToolRegistry: registry,
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "three-session", "Run all three")
			Expect(err).NotTo(HaveOccurred())
			for range chunks {
			}

			Expect(executedTools).To(ConsistOf("alpha", "beta", "gamma"))
		})
	})

	Context("single tool call (regression: existing behaviour unchanged)", func() {
		It("still executes the single tool and completes", func() {
			toolA := &blockingMockTool{name: "tool_a", result: tool.Result{Output: "ok"}}
			registry.Register(toolA)
			registry.SetPermission("tool_a", tool.Allow)

			chatProvider := &streamSequenceProvider{
				name: "single-tool",
				sequences: [][]provider.StreamChunk{
					{
						{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "call_a", Name: "tool_a", Arguments: map[string]any{}}},
						{Done: true},
					},
					{
						{Content: "Done.", Done: true},
					},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Tools:        []tool.Tool{toolA},
				ToolRegistry: registry,
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "single-session", "Run one tool")
			Expect(err).NotTo(HaveOccurred())
			for range chunks {
			}

			Expect(toolA.execCalled.Load()).To(BeTrue())
		})
	})
})

// funcTool is a lightweight test tool backed by a function closure.
type funcTool struct {
	name    string
	execute func(context.Context, tool.Input) (tool.Result, error)
}

func (t *funcTool) Name() string        { return t.name }
func (t *funcTool) Description() string { return t.name }
func (t *funcTool) Schema() tool.Schema { return tool.Schema{} }
func (t *funcTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	return t.execute(ctx, input)
}
