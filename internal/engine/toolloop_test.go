package engine_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/todo"
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

var _ = Describe("Engine todo continuation budget", func() {
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

	Context("when incomplete todos remain and the model keeps working without finishing them", func() {
		It("terminates the turn within a single Stream invocation after twenty todo continuations", func() {
			manifest := agent.Manifest{
				ID:   "todo-continuation-budget-agent",
				Name: "Todo Continuation Budget Agent",
				Capabilities: agent.Capabilities{
					Tools: []string{"spinner"},
				},
			}

			const sessionID = "todo-continuation-budget-session"
			todoStore := todo.NewMemoryStore()
			err := todoStore.Set(sessionID, []todo.Item{
				{Content: "never done", Status: "pending"},
			})
			Expect(err).NotTo(HaveOccurred())

			spinner := &delayedExecutableMockTool{
				name:       "spinner",
				execResult: tool.Result{Output: "spun"},
			}

			registry := tool.NewRegistry()
			registry.Register(spinner)
			registry.SetPermission(spinner.Name(), tool.Allow)

			prov := &repeatingToolProvider{
				name: "todo-continuation-spin",
				call: &provider.ToolCall{
					ID:        "call_spinner",
					Name:      "spinner",
					Arguments: map[string]any{"x": 1},
				},
			}

			eng := engine.New(engine.Config{
				ChatProvider: prov,
				Manifest:     manifest,
				Tools:        []tool.Tool{spinner},
				ToolRegistry: registry,
				TodoStore:    todoStore,
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
				"the in-turn todo continuation guards must terminate the turn instead of hanging")

			reason, sawDone := terminalStopReason(received)
			Expect(sawDone).To(BeTrue(), "expected a terminal Done chunk")
			Expect(reason).To(Equal(session.StopReasonToolLoopExceeded),
				"exhausting the todo continuation budget must stamp tool_loop_exceeded")
			Expect(prov.callCount()).To(BeNumerically(">=", 40),
				"the turn-local guards must sustain twenty todo continuations before tripping")
			Expect(prov.callCount()).To(BeNumerically("<=", 60),
				"the turn-local guards must stop the cycle far below unbounded spinning")
		})
	})
})

// drainChunks collects every chunk from the channel and reports whether
// the channel closed. Bounded by the passed duration so a stuck stream
// fails the spec rather than hanging the suite.
func drainCooldownChunks(chunks <-chan provider.StreamChunk, within time.Duration) ([]provider.StreamChunk, bool) {
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
	case <-time.After(within):
		return received, false
	}
}

func hasCooldownEventType(chunks []provider.StreamChunk, eventType string) bool {
	for _, c := range chunks {
		if c.EventType == eventType {
			return true
		}
	}
	return false
}

var _ = Describe("tool-loop cooldown retry hardening", func() {
	var (
		manifest  agent.Manifest
		todoStore *todo.MemoryStore
	)

	BeforeEach(func() {
		manifest = agent.Manifest{
			ID:   "cooldown-agent",
			Name: "Cooldown Agent",
		}
		todoStore = todo.NewMemoryStore()
	})

	It("does not reset guard counters after a cooldown retry", func() {
		health := failover.NewHealthManager()
		failoverMgr := failover.NewManager(provider.NewRegistry(), health, time.Second)
		failoverMgr.SetBasePreferences([]provider.ModelPreference{{Provider: "rl-provider", Model: "rl-model"}})
		prov := &overflowScriptedProvider{
			name: "rl-prov",
			script: []overflowProviderTurn{
				{content: "Working..."},
				{content: "Still working..."},
			},
			onCall: func(call int) {
				if call == 4 {
					health.MarkRateLimited("rl-provider", "rl-model", time.Now().Add(300*time.Millisecond))
				}
				if call > 4 {
					health.MarkRateLimited("rl-provider", "rl-model", time.Now().Add(time.Hour))
				}
			},
		}

		todoStore.Set("cooldown-session", []todo.Item{
			{Content: "write the report", Status: "pending", Priority: "high"},
		})

		eng := engine.New(engine.Config{
			ChatProvider:    prov,
			Manifest:        manifest,
			Tools:           []tool.Tool{},
			FailoverManager: failoverMgr,
		})
		eng.SetTodoStoreForTest(todoStore)
		eng.SetMaxToolLoopDurationForTest(30 * time.Second)

		ctx := context.WithValue(context.Background(), session.IDKey{}, "cooldown-session")
		chunks, err := eng.Stream(ctx, "cooldown-session", "Go")
		Expect(err).NotTo(HaveOccurred())

		received, closed := drainCooldownChunks(chunks, 10*time.Second)
		Expect(closed).To(BeTrue(), "channel must close once the no-progress guard holds across the cooldown retry")
		Expect(prov.callCount()).To(BeNumerically("<=", 6), "engine must stop after the cooldown retry instead of resetting no-progress forever")
		Expect(hasCooldownEventType(received, "provider_retry_scheduled")).To(BeTrue())
	})

	It("stops the loop when the cumulative cooldown wait budget is exhausted", func() {
		health := failover.NewHealthManager()
		failoverMgr := failover.NewManager(provider.NewRegistry(), health, time.Second)
		failoverMgr.SetBasePreferences([]provider.ModelPreference{{Provider: "budget-provider", Model: "budget-model"}})
		prov := &overflowScriptedProvider{
			name: "budget-prov",
			script: []overflowProviderTurn{
				{content: "Working..."},
				{content: "Still working..."},
			},
			onCall: func(call int) {
				if call == 4 {
					health.MarkRateLimited("budget-provider", "budget-model", time.Now().Add(4*time.Minute))
				}
			},
		}

		todoStore.Set("budget-session", []todo.Item{
			{Content: "write the report", Status: "pending", Priority: "high"},
		})

		eng := engine.New(engine.Config{
			ChatProvider:    prov,
			Manifest:        manifest,
			Tools:           []tool.Tool{},
			FailoverManager: failoverMgr,
		})
		eng.SetTodoStoreForTest(todoStore)
		// A 1s max loop duration makes the cumulative budget 2s; the
		// 4-minute wait exceeds it on the FIRST cooldown, so the loop
		// must break without sleeping at all.
		eng.SetMaxToolLoopDurationForTest(1 * time.Second)

		ctx := context.WithValue(context.Background(), session.IDKey{}, "budget-session")
		start := time.Now()
		chunks, err := eng.Stream(ctx, "budget-session", "Go")
		Expect(err).NotTo(HaveOccurred())

		received, closed := drainCooldownChunks(chunks, 5*time.Second)
		Expect(closed).To(BeTrue())
		Expect(time.Since(start)).To(BeNumerically("<", 30*time.Second))
		Expect(hasCooldownEventType(received, "provider_retry_budget_exhausted")).To(BeTrue(),
			"cumulative cooldown budget exhaustion must break the loop")
	})
})

var _ = Describe("delegate tool fallback child deadline", func() {
	It("applies the fallback timeout when no swarm member timeout governs", func() {
		dt := engine.NewDelegateTool(nil, agent.Delegation{}, "lead")
		Expect(dt.DelegateTimeoutForTest()).To(BeZero())

		dt.WithDelegateTimeout(5 * time.Minute)
		Expect(dt.DelegateTimeoutForTest()).To(Equal(5 * time.Minute))
	})
})
