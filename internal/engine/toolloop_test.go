package engine_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("Engine tool-loop total tool-time backstop", func() {
	var manifest agent.Manifest

	BeforeEach(func() {
		manifest = agent.Manifest{
			ID:   "tool-time-backstop-agent",
			Name: "Tool Time Backstop Agent",
			Capabilities: agent.Capabilities{
				Tools: []string{"slowpoke", "delegate_like"},
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

	terminalStopReason := func(received []provider.StreamChunk) (string, bool) {
		var reason string
		var sawDone bool
		for _, c := range received {
			if c.Done {
				reason = c.StopReason
				sawDone = true
			}
		}
		return reason, sawDone
	}

	Context("when cumulative non-delegated tool execution time exceeds the duration cap", func() {
		It("terminates the turn with tool_loop_exceeded even though provider time stays under the cap", func() {
			slowpoke := &delayedExecutableMockTool{
				name:       "slowpoke",
				delay:      40 * time.Millisecond,
				execResult: tool.Result{Output: "slow"},
			}

			registry := tool.NewRegistry()
			registry.Register(slowpoke)
			registry.SetPermission(slowpoke.Name(), tool.Allow)

			prov := &repeatingToolProvider{
				name: "slow-tool-spin",
				call: &provider.ToolCall{
					ID:        "call_slowpoke",
					Name:      "slowpoke",
					Arguments: map[string]any{"x": 1},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{slowpoke},
				ToolRegistry: registry,
			})
			eng.SetMaxToolLoopIterationsForTest(0)
			eng.SetMaxIdenticalToolCallsForTest(0)
			eng.SetMaxSameToolPatternCallsForTest(0)
			eng.SetMaxToolLoopDurationForTest(120 * time.Millisecond)

			chunks, err := eng.Stream(context.Background(), "tool-time-backstop-agent", "Go")
			Expect(err).NotTo(HaveOccurred())

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue(),
				"cumulative tool time must terminate the turn; it hung instead")

			reason, sawDone := terminalStopReason(received)
			Expect(sawDone).To(BeTrue(), "expected a terminal Done chunk")
			Expect(reason).To(Equal(session.StopReasonToolLoopExceeded),
				"the total-tool-time backstop must stamp tool_loop_exceeded")
			Expect(prov.callCount()).To(BeNumerically(">=", 3),
				"the cap must only trip once the cumulative 120ms budget is consumed by 40ms calls")
			Expect(prov.callCount()).To(BeNumerically("<", 50),
				"the backstop must trip far below the production iteration ceiling")
		})
	})

	Context("when a TimeoutOverrider-zero tool runs past the duration cap", func() {
		It("does not count the delegated execution toward the backstop", func() {
			inherit := &sleepingToolWithOverride{
				sleepingTool: sleepingTool{name: "delegate_like", sleepFor: 250 * time.Millisecond},
			}

			registry := tool.NewRegistry()
			registry.Register(inherit)
			registry.SetPermission(inherit.Name(), tool.Allow)

			prov := &scriptedChunkProvider{
				name: "single-delegation",
				script: []scriptedBatch{
					{toolCalls: []*provider.ToolCall{{ID: "c1", Name: "delegate_like", Arguments: map[string]any{}}}},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{inherit},
				ToolRegistry: registry,
			})
			eng.SetMaxToolLoopIterationsForTest(0)
			eng.SetMaxIdenticalToolCallsForTest(0)
			eng.SetMaxSameToolPatternCallsForTest(0)
			eng.SetMaxToolLoopDurationForTest(100 * time.Millisecond)

			chunks, err := eng.Stream(context.Background(), "tool-time-backstop-agent", "Delegate")
			Expect(err).NotTo(HaveOccurred())

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue(),
				"the delegated turn must complete naturally")

			for _, c := range received {
				Expect(c.StopReason).NotTo(Equal(session.StopReasonToolLoopExceeded),
					"a delegation-shaped execution must be exempt from the parent tool-time cap")
			}
			Expect(inherit.observed).NotTo(HaveOccurred(),
				"the delegated execution must inherit the parent context and run to completion")
		})
	})
})

var _ = Describe("Engine background-task continuation budget", func() {
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
		case <-time.After(10 * time.Second):
			return received, false
		}
	}

	terminalStopReason := func(received []provider.StreamChunk) (string, bool) {
		var reason string
		var sawDone bool
		for _, c := range received {
			if c.Done {
				reason = c.StopReason
				sawDone = true
			}
		}
		return reason, sawDone
	}

	Context("when background tasks never complete and the loop keeps requesting continuations", func() {
		It("terminates the turn after twenty background-task continuations", func() {
			manifest := agent.Manifest{
				ID:   "bg-continuation-budget-agent",
				Name: "BG Continuation Budget Agent",
				Capabilities: agent.Capabilities{
					Tools: []string{"spinner", "delegate"},
				},
			}

			spinner := &delayedExecutableMockTool{
				name:       "spinner",
				execResult: tool.Result{Output: "spun"},
			}

			registry := tool.NewRegistry()
			registry.Register(spinner)
			registry.SetPermission(spinner.Name(), tool.Allow)

			prov := &repeatingToolProvider{
				name: "bg-continuation-spin",
				call: &provider.ToolCall{
					ID:        "call_spinner",
					Name:      "spinner",
					Arguments: map[string]any{"x": 1},
				},
			}

			const sessionID = "bg-continuation-budget-session"
			bgMgr := engine.NewBackgroundTaskManager()
			delegate := engine.NewDelegateToolWithBackground(
				nil, agent.Delegation{}, "bg-continuation-budget-agent", bgMgr, nil)

			taskCtx, cancel := context.WithCancel(
				context.WithValue(context.Background(), session.IDKey{}, sessionID))
			defer cancel()
			bgMgr.Launch(taskCtx, "stuck-bg", "bg-continuation-budget-agent", "never completes",
				func(ctx context.Context) (string, error) {
					<-ctx.Done()
					return "", ctx.Err()
				})

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{spinner, delegate},
				ToolRegistry: registry,
			})
			eng.SetMaxToolLoopIterationsForTest(2)
			eng.SetMaxIdenticalToolCallsForTest(0)
			eng.SetMaxSameToolPatternCallsForTest(0)
			eng.SetMaxToolLoopDurationForTest(30 * time.Second)

			streamCtx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
			chunks, err := eng.Stream(streamCtx, sessionID, "Go")
			Expect(err).NotTo(HaveOccurred())

			received, closed := drain(chunks)
			Expect(closed).To(BeTrue(),
				"the background continuation budget must terminate the turn instead of cycling forever")

			reason, sawDone := terminalStopReason(received)
			Expect(sawDone).To(BeTrue(), "expected a terminal Done chunk")
			Expect(reason).To(Equal(session.StopReasonToolLoopExceeded),
				"exhausting the background continuation budget must stamp tool_loop_exceeded")
			Expect(prov.callCount()).To(BeNumerically(">=", 40),
				"the budget must allow twenty continuations before tripping")
			Expect(prov.callCount()).To(BeNumerically("<=", 60),
				"the budget must stop the cycle far below unbounded spinning")
		})
	})
})
