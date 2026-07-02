package engine_test

import (
	"context"
	"fmt"
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

// repeatingToolProvider is a STATEFUL mock provider that re-emits a fixed
// tool_call on every stream it is asked to open. It models the pathological
// shape captured in production: a provider that re-requests the SAME
// (unregistered) tool every tool-loop continuation, so the engine's
// tool-not-found path (which returns a nil-Go-error tool.Result{Error:
// ErrToolNotFound}) falls through and re-requests forever. Without a loop
// cap the engine spins unbounded (15,404 iterations observed). With the cap
// the turn must terminate and the channel must close.
type repeatingToolProvider struct {
	name string
	call *provider.ToolCall

	mu    sync.Mutex
	calls int
}

func (p *repeatingToolProvider) Name() string { return p.name }

func (p *repeatingToolProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()

	tc := *p.call
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{EventType: "tool_call", ToolCall: &tc}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch, nil
}

func (p *repeatingToolProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *repeatingToolProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

func (p *repeatingToolProvider) Models() ([]provider.Model, error) { return nil, nil }

func (p *repeatingToolProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// scriptedToolProvider is a STATEFUL mock that drives an explicit list of
// tool_call batches across successive streams, then a clean text Done once
// the script is exhausted. It models legitimate multi-tool turns where the
// model fans out several DISTINCT calls before completing naturally — the
// turns the cap must NOT intercept.
type scriptedToolProvider struct {
	name  string
	batch [][]*provider.ToolCall

	mu    sync.Mutex
	calls int
}

func (p *scriptedToolProvider) Name() string { return p.name }

func (p *scriptedToolProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()

	ch := make(chan provider.StreamChunk, 8)
	go func() {
		defer close(ch)
		if idx >= len(p.batch) {
			// Script exhausted: emit a clean natural completion.
			ch <- provider.StreamChunk{Content: "All done.", Done: true}
			return
		}
		for _, tc := range p.batch[idx] {
			tcCopy := *tc
			ch <- provider.StreamChunk{EventType: "tool_call", ToolCall: &tcCopy}
		}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch, nil
}

func (p *scriptedToolProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *scriptedToolProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

func (p *scriptedToolProvider) Models() ([]provider.Model, error) { return nil, nil }

// emptyTextToolProvider emits a tool_call with an incrementing index argument
// and NO assistant text content on every stream. It models the pathological
// shape captured in production with z.ai/glm-5.2: a provider that spins,
// emitting only todo_update calls with incrementing index and zero reasoning
// text. The varying argument dodges the repeat-call fingerprint; the low call
// count dodges the iteration backstop; the empty text dodges the truly-empty
// guard. Only the empty-text-with-tool-calls detector catches it.
type emptyTextToolProvider struct {
	name     string
	toolName string

	mu    sync.Mutex
	calls int
}

func (p *emptyTextToolProvider) Name() string { return p.name }

func (p *emptyTextToolProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()

	tc := provider.ToolCall{
		ID:        fmt.Sprintf("call_%d", idx),
		Name:      p.toolName,
		Arguments: map[string]any{"index": idx},
	}
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{EventType: "tool_call", ToolCall: &tc}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch, nil
}

func (p *emptyTextToolProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *emptyTextToolProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

func (p *emptyTextToolProvider) Models() ([]provider.Model, error) { return nil, nil }

// scriptedBatch is one entry in a scriptedChunkProvider script: the assistant
// text content (may be empty) and the tool calls emitted on that stream.
type scriptedBatch struct {
	content   string
	toolCalls []*provider.ToolCall
}

// scriptedChunkProvider drives an explicit list of (content, toolCalls)
// batches across successive streams, then a clean text Done once the script
// is exhausted. Unlike scriptedToolProvider it allows per-batch control over
// the assistant text content so specs can exercise the empty-text-with-tool-
// calls detector and its reset semantics.
type scriptedChunkProvider struct {
	name   string
	script []scriptedBatch

	mu    sync.Mutex
	calls int
}

func (p *scriptedChunkProvider) Name() string { return p.name }

func (p *scriptedChunkProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()

	ch := make(chan provider.StreamChunk, 8)
	go func() {
		defer close(ch)
		if idx >= len(p.script) {
			ch <- provider.StreamChunk{Content: "All done.", Done: true}
			return
		}
		batch := p.script[idx]
		if batch.content != "" {
			ch <- provider.StreamChunk{Content: batch.content}
		}
		for _, tc := range batch.toolCalls {
			tcCopy := *tc
			ch <- provider.StreamChunk{EventType: "tool_call", ToolCall: &tcCopy}
		}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch, nil
}

func (p *scriptedChunkProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *scriptedChunkProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

func (p *scriptedChunkProvider) Models() ([]provider.Model, error) { return nil, nil }

var _ = Describe("Engine tool-loop cap", func() {
	var manifest agent.Manifest

	BeforeEach(func() {
		manifest = agent.Manifest{
			ID:   "loop-cap-agent",
			Name: "Loop Cap Agent",
			Capabilities: agent.Capabilities{
				Tools: []string{"alpha", "beta", "gamma", "delta", "epsilon"},
			},
		}
	})

	drain := func(chunks <-chan provider.StreamChunk) ([]provider.StreamChunk, bool) {
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

	Context("when a provider re-emits the same tool call every continuation", func() {
		It("terminates the turn with a tool_loop_exceeded StopReason and closes the channel", func() {
			prov := &repeatingToolProvider{
				name: "pathological-loop",
				call: &provider.ToolCall{
					ID:        "call_repeat",
					Name:      "missing_tool",
					Arguments: map[string]any{"x": 1},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})
			// Trip the identical-call detector quickly so the spec does not
			// have to ride the absolute 50-iteration backstop.
			eng.SetMaxIdenticalToolCallsForTest(3)

			chunks, err := eng.Stream(context.Background(), "loop-cap-agent", "Go")
			Expect(err).NotTo(HaveOccurred())

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue(), "the turn must terminate and the channel must close; it hung instead")

			var terminal *provider.StreamChunk
			for i := range received {
				if received[i].Done {
					terminal = &received[i]
				}
			}
			Expect(terminal).NotTo(BeNil(), "expected a terminal Done chunk")
			Expect(terminal.StopReason).To(Equal(session.StopReasonToolLoopExceeded),
				"a capped tool loop must stamp the tool_loop_exceeded StopReason so the UI renders a soft error")
			Expect(prov.callCount()).To(BeNumerically("<", 50),
				"the identical-call detector must trip well before the absolute backstop")
		})
	})

	Context("when the model drives several distinct tools then completes naturally", func() {
		It("executes all of them and ends with the provider's natural StopReason, not tool_loop_exceeded", func() {
			alpha := &executableMockTool{name: "alpha", execResult: tool.Result{Output: "a"}}
			beta := &executableMockTool{name: "beta", execResult: tool.Result{Output: "b"}}
			gamma := &executableMockTool{name: "gamma", execResult: tool.Result{Output: "g"}}
			delta := &executableMockTool{name: "delta", execResult: tool.Result{Output: "d"}}
			epsilon := &executableMockTool{name: "epsilon", execResult: tool.Result{Output: "e"}}

			registry := tool.NewRegistry()
			for _, t := range []tool.Tool{alpha, beta, gamma, delta, epsilon} {
				registry.Register(t)
				registry.SetPermission(t.Name(), tool.Allow)
			}

			prov := &scriptedToolProvider{
				name: "distinct-multi-tool",
				batch: [][]*provider.ToolCall{
					{{ID: "c1", Name: "alpha", Arguments: map[string]any{}}},
					{{ID: "c2", Name: "beta", Arguments: map[string]any{}}},
					{{ID: "c3", Name: "gamma", Arguments: map[string]any{}}},
					{{ID: "c4", Name: "delta", Arguments: map[string]any{}}},
					{{ID: "c5", Name: "epsilon", Arguments: map[string]any{}}},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{alpha, beta, gamma, delta, epsilon},
				ToolRegistry: registry,
			})
			// Default 50-iteration backstop is comfortably above 5 distinct
			// calls; leave it at the production default. Disable the
			// empty-text guard since scriptedToolProvider emits no assistant
			// text alongside its tool calls.
			eng.SetMaxEmptyTextToolCallsForTest(0)

			chunks, err := eng.Stream(context.Background(), "loop-cap-agent", "Use everything")
			Expect(err).NotTo(HaveOccurred())

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue(), "a legitimate multi-tool turn must complete and close")

			Expect(alpha.execCalled).To(BeTrue())
			Expect(beta.execCalled).To(BeTrue())
			Expect(gamma.execCalled).To(BeTrue())
			Expect(delta.execCalled).To(BeTrue())
			Expect(epsilon.execCalled).To(BeTrue())

			for _, c := range received {
				Expect(c.StopReason).NotTo(Equal(session.StopReasonToolLoopExceeded),
					"a varied multi-tool turn must NOT be intercepted by the loop cap")
			}
		})
	})

	Context("identical-call repeat detection boundary", func() {
		It("trips after 3 identical consecutive calls", func() {
			prov := &repeatingToolProvider{
				name: "three-identical",
				call: &provider.ToolCall{
					ID:        "call_x",
					Name:      "missing_tool",
					Arguments: map[string]any{"k": "v"},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})
			eng.SetMaxIdenticalToolCallsForTest(3)

			chunks, err := eng.Stream(context.Background(), "loop-cap-agent", "Go")
			Expect(err).NotTo(HaveOccurred())

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue())

			var tripped bool
			for _, c := range received {
				if c.Done && c.StopReason == session.StopReasonToolLoopExceeded {
					tripped = true
				}
			}
			Expect(tripped).To(BeTrue(), "3 identical consecutive calls must trip the repeat detector")
		})

		It("does NOT trip on 2 identical calls followed by a distinct call", func() {
			// First two streams emit the SAME registered tool; the third emits
			// a DIFFERENT registered tool; the script then completes cleanly.
			// With the identical-call threshold at 3, the run of identical
			// fingerprints never reaches 3 consecutive, so the cap must not
			// fire — the turn completes naturally.
			alpha := &executableMockTool{name: "alpha", execResult: tool.Result{Output: "a"}}
			beta := &executableMockTool{name: "beta", execResult: tool.Result{Output: "b"}}

			registry := tool.NewRegistry()
			for _, t := range []tool.Tool{alpha, beta} {
				registry.Register(t)
				registry.SetPermission(t.Name(), tool.Allow)
			}

			prov := &scriptedToolProvider{
				name: "two-then-distinct",
				batch: [][]*provider.ToolCall{
					{{ID: "c1", Name: "alpha", Arguments: map[string]any{"k": "v"}}},
					{{ID: "c2", Name: "alpha", Arguments: map[string]any{"k": "v"}}},
					{{ID: "c3", Name: "beta", Arguments: map[string]any{"k": "v"}}},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{alpha, beta},
				ToolRegistry: registry,
			})
			eng.SetMaxIdenticalToolCallsForTest(3)
			eng.SetMaxEmptyTextToolCallsForTest(0)

			chunks, err := eng.Stream(context.Background(), "loop-cap-agent", "Go")
			Expect(err).NotTo(HaveOccurred())

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue())

			for _, c := range received {
				Expect(c.StopReason).NotTo(Equal(session.StopReasonToolLoopExceeded),
					"2 identical then a distinct call must NOT trip the repeat detector")
			}
		})
	})

	Context("duration backstop", func() {
		It("trips the loop when wall-clock exceeds maxToolLoopDuration even with no other guard active", func() {
			prov := &repeatingToolProvider{
				name: "slow-loop",
				call: &provider.ToolCall{
					ID:        "call_slow",
					Name:      "missing_tool",
					Arguments: map[string]any{"x": 1},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})
			// Disable the other two guards so ONLY the duration backstop
			// can trip the loop.
			eng.SetMaxToolLoopIterationsForTest(0)
			eng.SetMaxIdenticalToolCallsForTest(0)
			// Set an extremely short duration so the backstop fires after
			// the first tool-loop iteration.
			eng.SetMaxToolLoopDurationForTest(time.Millisecond)

			chunks, err := eng.Stream(context.Background(), "loop-cap-agent", "Go")
			Expect(err).NotTo(HaveOccurred())

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue(), "the duration backstop must terminate the turn")

			var tripped bool
			for _, c := range received {
				if c.Done && c.StopReason == session.StopReasonToolLoopExceeded {
					tripped = true
				}
			}
			Expect(tripped).To(BeTrue(), "the duration backstop must stamp tool_loop_exceeded")
			// The loop must NOT run unbounded. With disabled iteration and
			// identical-call guards and only a 1ms duration budget, the loop
			// should trip well below the production 50-iteration or 300s
			// ceilings. Iteration count is machine-dependent (observed
			// between 47 and 105 within 1ms); we assert < 300 to confirm
			// it fires far below both defaults without being flaky.
			Expect(prov.callCount()).To(BeNumerically("<", 300),
				"the duration backstop must trip well below the production 50-iteration ceiling (1ms budget)")
		})
	})

	Context("when the cap trips but the session has incomplete todos", func() {
		It("injects todo continuations and keeps retrying until the context is cancelled", func() {
			prov := &repeatingToolProvider{
				name: "loop-with-todos",
				call: &provider.ToolCall{
					ID:        "call_todo_loop",
					Name:      "missing_tool",
					Arguments: map[string]any{"x": 1},
				},
			}

			loopSessionID := "loop-cap-todo-session"
			todoStore := todo.NewMemoryStore()
			todoStore.Set(loopSessionID, []todo.Item{
				{Content: "finish important work", Status: "pending", Priority: "high"},
			})

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{},
			})
			eng.SetMaxIdenticalToolCallsForTest(3)
			eng.SetTodoStoreForTest(todoStore)

			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			ctx = context.WithValue(ctx, session.IDKey{}, loopSessionID)
			chunks, err := eng.Stream(ctx, loopSessionID, "Go")
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() int { return prov.callCount() }, "3s", "100ms").Should(
				BeNumerically(">=", 6),
				"engine must keep retrying past the loop cap when todos are incomplete",
			)

			cancel()

			_, closed := drain(chunks)
			Expect(closed).To(BeTrue(),
				"channel must close after context cancellation")
		})
	})

	Context("empty-text-with-tool-calls detector", func() {
		It("trips after 3 consecutive empty-text responses that still carry tool calls", func() {
			alpha := &executableMockTool{name: "alpha", execResult: tool.Result{Output: "a"}}

			registry := tool.NewRegistry()
			registry.Register(alpha)
			registry.SetPermission(alpha.Name(), tool.Allow)

			prov := &emptyTextToolProvider{
				name:     "silent-spin",
				toolName: "alpha",
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{alpha},
				ToolRegistry: registry,
			})
			eng.SetMaxIdenticalToolCallsForTest(0)
			eng.SetMaxToolLoopIterationsForTest(0)
			eng.SetMaxToolLoopDurationForTest(0)
			eng.SetMaxEmptyTextToolCallsForTest(3)

			chunks, err := eng.Stream(context.Background(), "loop-cap-agent", "Go")
			Expect(err).NotTo(HaveOccurred())

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue(),
				"the silent-spin detector must terminate the turn")

			var tripped bool
			for _, c := range received {
				if c.Done && c.StopReason == session.StopReasonToolLoopExceeded {
					tripped = true
				}
			}
			Expect(tripped).To(BeTrue(),
				"3 consecutive empty-text-with-tool-calls turns must trip the detector")
		})

		It("does NOT trip on 2 consecutive empty-text turns", func() {
			alpha := &executableMockTool{name: "alpha", execResult: tool.Result{Output: "a"}}
			beta := &executableMockTool{name: "beta", execResult: tool.Result{Output: "b"}}

			registry := tool.NewRegistry()
			for _, t := range []tool.Tool{alpha, beta} {
				registry.Register(t)
				registry.SetPermission(t.Name(), tool.Allow)
			}

			prov := &scriptedChunkProvider{
				name: "two-empty-then-done",
				script: []scriptedBatch{
					{toolCalls: []*provider.ToolCall{{ID: "c1", Name: "alpha", Arguments: map[string]any{"i": 0}}}},
					{toolCalls: []*provider.ToolCall{{ID: "c2", Name: "beta", Arguments: map[string]any{"i": 1}}}},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{alpha, beta},
				ToolRegistry: registry,
			})
			eng.SetMaxIdenticalToolCallsForTest(0)
			eng.SetMaxToolLoopIterationsForTest(0)
			eng.SetMaxToolLoopDurationForTest(0)
			eng.SetMaxEmptyTextToolCallsForTest(3)

			chunks, err := eng.Stream(context.Background(), "loop-cap-agent", "Go")
			Expect(err).NotTo(HaveOccurred())

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue())

			for _, c := range received {
				Expect(c.StopReason).NotTo(Equal(session.StopReasonToolLoopExceeded),
					"2 consecutive empty-text turns must NOT trip the detector")
			}
		})

		It("resets the counter when a non-empty text response arrives", func() {
			alpha := &executableMockTool{name: "alpha", execResult: tool.Result{Output: "a"}}

			registry := tool.NewRegistry()
			registry.Register(alpha)
			registry.SetPermission(alpha.Name(), tool.Allow)

			prov := &scriptedChunkProvider{
				name: "reset-on-text",
				script: []scriptedBatch{
					{toolCalls: []*provider.ToolCall{{ID: "c1", Name: "alpha", Arguments: map[string]any{"i": 0}}}},
					{toolCalls: []*provider.ToolCall{{ID: "c2", Name: "alpha", Arguments: map[string]any{"i": 1}}}},
					{content: "reasoning text", toolCalls: []*provider.ToolCall{{ID: "c3", Name: "alpha", Arguments: map[string]any{"i": 2}}}},
					{toolCalls: []*provider.ToolCall{{ID: "c4", Name: "alpha", Arguments: map[string]any{"i": 3}}}},
					{toolCalls: []*provider.ToolCall{{ID: "c5", Name: "alpha", Arguments: map[string]any{"i": 4}}}},
					{toolCalls: []*provider.ToolCall{{ID: "c6", Name: "alpha", Arguments: map[string]any{"i": 5}}}},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{alpha},
				ToolRegistry: registry,
			})
			eng.SetMaxIdenticalToolCallsForTest(0)
			eng.SetMaxToolLoopIterationsForTest(0)
			eng.SetMaxToolLoopDurationForTest(0)
			eng.SetMaxEmptyTextToolCallsForTest(3)

			chunks, err := eng.Stream(context.Background(), "loop-cap-agent", "Go")
			Expect(err).NotTo(HaveOccurred())

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue())

			var tripped bool
			for _, c := range received {
				if c.Done && c.StopReason == session.StopReasonToolLoopExceeded {
					tripped = true
				}
			}
			Expect(tripped).To(BeTrue(),
				"after 2 empty, 1 non-empty (reset to 0), then 3 empty, the counter must reach 3 and trip")
			Expect(prov.calls).To(BeNumerically("==", 6),
				"the non-empty batch must reset the run; without reset the trip would fire at call 3, not 6")
		})

		It("stamps the tool_loop_exceeded StopReason on the terminal chunk", func() {
			alpha := &executableMockTool{name: "alpha", execResult: tool.Result{Output: "a"}}

			registry := tool.NewRegistry()
			registry.Register(alpha)
			registry.SetPermission(alpha.Name(), tool.Allow)

			prov := &emptyTextToolProvider{
				name:     "stop-reason-pin",
				toolName: "alpha",
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{alpha},
				ToolRegistry: registry,
			})
			eng.SetMaxIdenticalToolCallsForTest(0)
			eng.SetMaxToolLoopIterationsForTest(0)
			eng.SetMaxToolLoopDurationForTest(0)
			eng.SetMaxEmptyTextToolCallsForTest(3)

			chunks, err := eng.Stream(context.Background(), "loop-cap-agent", "Go")
			Expect(err).NotTo(HaveOccurred())

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue())

			var terminal *provider.StreamChunk
			for i := range received {
				if received[i].Done {
					terminal = &received[i]
				}
			}
			Expect(terminal).NotTo(BeNil(), "expected a terminal Done chunk")
			Expect(terminal.StopReason).To(Equal(session.StopReasonToolLoopExceeded),
				"the empty-text trip must stamp tool_loop_exceeded so the UI renders a soft error")
		})
	})
})
