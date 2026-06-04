package engine_test

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
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
			// calls; leave it at the production default.

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
})
