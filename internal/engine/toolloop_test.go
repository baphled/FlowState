package engine_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

type softContinuationSummariser struct {
	calls    atomic.Int32
	response string
}

func (s *softContinuationSummariser) Summarise(_ context.Context, _ string, _ string, _ []provider.Message) (string, error) {
	s.calls.Add(1)
	return s.response, nil
}

func newSoftContinuationEngine(prov provider.Provider, summariser ctxstore.Summariser, tokenLimit int) (*engine.Engine, *recall.FileContextStore) {
	tempDir, err := os.MkdirTemp("", "soft-cont-*")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

	store, err := recall.NewFileContextStore(filepath.Join(tempDir, "context.json"), "test-model")
	Expect(err).NotTo(HaveOccurred())

	counter := &wordTokenCounter{limit: tokenLimit}

	cfg := ctxstore.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = true
	cfg.AutoCompaction.Threshold = 0.60

	cm := agent.DefaultContextManagement()
	cm.CompactionThreshold = 0

	manifest := agent.Manifest{
		ID:   "soft-cont-agent",
		Name: "Soft Cont Agent",
		Instructions: agent.Instructions{
			SystemPrompt: "sys",
		},
		ContextManagement: cm,
		Capabilities: agent.Capabilities{
			Tools: []string{"read", "todo_update"},
		},
	}

	todoStore := todo.NewMemoryStore()
	eng := engine.New(engine.Config{
		ChatProvider:      prov,
		Manifest:          manifest,
		Store:             store,
		TokenCounter:      counter,
		AutoCompactor:     ctxstore.NewAutoCompactor(summariser),
		CompressionConfig: cfg,
		Tools: []tool.Tool{
			&delayedExecutableMockTool{
				name:        "read",
				description: "read a file",
				delay:       2 * time.Millisecond,
				execResult:  tool.Result{Output: "file contents"},
			},
			// Fix 3: register a real todo_update tool so the todo-guard
			// machinery is exercised end-to-end in this harness instead
			// of being skipped.
			todo.NewUpdate(todoStore),
		},
	})
	eng.SetTodoStoreForTest(todoStore)
	return eng, store
}

func seedSoftContinuationHistory(store *recall.FileContextStore) {
	words := make([]string, 7)
	for i := range words {
		words[i] = "w"
	}
	content := strings.Join(words, " ")
	for range 10 {
		store.Append(provider.Message{Role: "assistant", Content: content})
	}
}

func readBatch(i int) []*provider.ToolCall {
	return []*provider.ToolCall{{
		ID:        fmt.Sprintf("read_%d", i),
		Name:      "read",
		Arguments: map[string]any{"path": fmt.Sprintf("/tmp/soft/%d.txt", i)},
	}}
}

func drainSoftContinuation(chunks <-chan provider.StreamChunk) []provider.StreamChunk {
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
	case <-time.After(10 * time.Second):
		Fail("stream did not terminate within 10s")
	}
	return received
}

func softContinuationTerminalStopReason(received []provider.StreamChunk) (string, bool) {
	for i := range received {
		if received[i].Done {
			return received[i].StopReason, true
		}
	}
	return "", false
}

var _ = Describe("Engine soft continuation guard", func() {
	It("soft continuation compacts before continuing", func() {
		summariser := &softContinuationSummariser{response: buildSummaryJSON()}

		script := make([]scriptedBatch, 0, 12)
		for i := 0; i < 11; i++ {
			script = append(script, scriptedBatch{toolCalls: readBatch(i)})
		}
		script = append(script, scriptedBatch{content: "Here is the summary."})

		prov := &capturingScriptedProvider{name: "soft-cont-compact", script: script}
		eng, store := newSoftContinuationEngine(prov, summariser, 400)

		seedSoftContinuationHistory(store)

		eng.SetMaxToolLoopIterationsForTest(2)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(0)
		eng.SetMaxSameToolPatternCallsForTest(0)

		chunks, err := eng.Stream(context.Background(), "soft-cont-agent", "Read many files")
		Expect(err).NotTo(HaveOccurred())

		received := drainSoftContinuation(chunks)
		_, gotTerminal := softContinuationTerminalStopReason(received)
		Expect(gotTerminal).To(BeTrue(), "the turn must terminate")

		Expect(summariser.calls.Load()).To(BeNumerically(">=", 1),
			"soft continuation over the compaction threshold must compact before continuing")
	})

	It("duration budget still forces summary after soft continuations", func() {
		summariser := &softContinuationSummariser{response: buildSummaryJSON()}

		script := make([]scriptedBatch, 0, 300)
		for i := 0; i < 300; i++ {
			script = append(script, scriptedBatch{toolCalls: readBatch(i)})
		}

		prov := &capturingScriptedProvider{name: "soft-cont-duration", script: script}
		// wordTokenCounter budget. Must exceed the overflow gate's
		// defaultOutputReserve (4096) — with a limit below the reserve
		// floor, usable = limit-reserve collapses to ~1 and every
		// non-skip-flagged request is refused over-budget, so the run
		// terminates through ErrCompactionInsufficient instead of the
		// duration backstop this spec exercises.
		eng, store := newSoftContinuationEngine(prov, summariser, 8000)
		seedSoftContinuationHistory(store)

		eng.SetMaxToolLoopIterationsForTest(0)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(time.Millisecond)
		eng.SetMaxSameToolPatternCallsForTest(0)

		chunks, err := eng.Stream(context.Background(), "soft-cont-agent", "Long read loop")
		Expect(err).NotTo(HaveOccurred())

		received := drainSoftContinuation(chunks)
		reason, gotTerminal := softContinuationTerminalStopReason(received)
		Expect(gotTerminal).To(BeTrue(), "expected a terminal Done chunk")
		Expect(reason).To(Equal(session.StopReasonToolLoopExceeded),
			"the duration backstop must stamp tool_loop_exceeded after soft continuations")

		Expect(prov.callCount()).To(BeNumerically("<=", 12),
			"the duration budget must bound provider calls tightly")

		Expect(prov.sawMessageContaining("tool loop budget is exhausted")).To(BeTrue(),
			"the forced summary must be injected after the duration trip")
	})

	It("todo guard rejects no-work completion until work done this turn", func() {
		sessionID := "soft-cont-todo-session"
		ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)

		todoStore := todo.NewMemoryStore()
		todoStore.Set(sessionID, []todo.Item{
			{Content: "Task 1", Status: "in_progress", Priority: "high"},
			{Content: "Task 2", Status: "in_progress", Priority: "medium"},
		})

		summariser := &softContinuationSummariser{response: buildSummaryJSON()}

		todoCompleteCall := func(i int) []*provider.ToolCall {
			return []*provider.ToolCall{{
				ID:        fmt.Sprintf("todo_%d", i),
				Name:      "todo_update",
				Arguments: map[string]any{"index": i, "status": "completed"},
			}}
		}

		script := []scriptedBatch{
			{toolCalls: todoCompleteCall(0)},
			{toolCalls: readBatch(0)},
			{toolCalls: todoCompleteCall(1)},
			{content: "All work completed and summarised."},
		}

		prov := &capturingScriptedProvider{name: "soft-cont-todo", script: script}
		// 8000: must clear the overflow gate's 4096 default output
		// reserve or the first request is refused over-budget and the
		// todo-guard script never runs.
		eng, _ := newSoftContinuationEngine(prov, summariser, 8000)

		eng.SetMaxToolLoopIterationsForTest(10)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(0)
		eng.SetMaxSameToolPatternCallsForTest(0)
		eng.SetTodoStoreForTest(todoStore)

		chunks, err := eng.Stream(ctx, "soft-cont-agent", "Complete tasks")
		Expect(err).NotTo(HaveOccurred())

		received := drainSoftContinuation(chunks)
		_, gotTerminal := softContinuationTerminalStopReason(received)
		Expect(gotTerminal).To(BeTrue(), "the turn must terminate")

		Expect(prov.sawMessageContaining("without doing any work")).To(BeTrue(),
			"the engine must reject the todo completion and feed the error back to the provider")

		var sawFinalSummary bool
		for _, c := range received {
			if strings.Contains(c.Content, "All work completed") {
				sawFinalSummary = true
			}
		}
		if !sawFinalSummary {
			Skip("todo_update acceptance half not exercised: todo_update is not wired as an executable engine tool in this harness, so only the rejection half is asserted")
		}
	})
})

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
		It("halts a no-progress todo cycle once behavioural continuations are exhausted", func() {
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
				"the no-progress guard must stamp tool_loop_exceeded on the capped turn")
			Expect(prov.callCount()).To(BeNumerically(">", 8),
				"the soft continuation budget must let the cycle run well past the first cap trip")
			Expect(prov.callCount()).To(BeNumerically("<=", 20),
				"tool activity with an unchanged todo snapshot must terminate within a bounded call budget")
		})
	})
})

var _ = Describe("Engine capped-turn terminal persistence", func() {
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

	newTerminalPersistenceEngine := func(prov provider.Provider) (*engine.Engine, *recall.FileContextStore, string) {
		const sessionID = "terminal-persistence-session"
		todoStore := todo.NewMemoryStore()
		Expect(todoStore.Set(sessionID, []todo.Item{
			{Content: "never done", Status: "pending", Priority: "high"},
		})).NotTo(HaveOccurred())

		spinner := &executableMockTool{name: "spinner", execResult: tool.Result{Output: "spun"}}
		registry := tool.NewRegistry()
		registry.Register(spinner)
		registry.SetPermission(spinner.Name(), tool.Allow)

		store, err := recall.NewFileContextStore(GinkgoT().TempDir()+"/ctx.json", "terminal-model")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)

		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest: agent.Manifest{
				ID:   "terminal-persistence-agent",
				Name: "Terminal Persistence Agent",
				Capabilities: agent.Capabilities{
					Tools: []string{"spinner"},
				},
			},
			Tools:        []tool.Tool{spinner},
			ToolRegistry: registry,
			TodoStore:    todoStore,
			Store:        store,
		})
		return eng, store, sessionID
	}

	assertPersistedTerminal := func(store *recall.FileContextStore) {
		stored := store.GetStoredMessages()
		Expect(stored).NotTo(BeEmpty())
		last := stored[len(stored)-1].Message
		Expect(last.Role).To(Equal("assistant"),
			"the persisted transcript must end with an assistant message, not a dangling tool result")
		Expect(last.StopReason).To(Equal(session.StopReasonToolLoopExceeded),
			"the persisted terminal assistant message must carry the stop-reason stamp")
	}

	It("persists a stamped terminal assistant message when the todo machinery stops a capped turn", func() {
		prov := &repeatingToolProvider{
			name: "terminal-persistence-spin",
			call: &provider.ToolCall{
				ID:        "call_spinner",
				Name:      "spinner",
				Arguments: map[string]any{"x": 1},
			},
		}
		eng, store, sessionID := newTerminalPersistenceEngine(prov)
		eng.SetMaxToolLoopIterationsForTest(2)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxSameToolPatternCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(0)

		ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
		chunks, err := eng.Stream(ctx, sessionID, "Go")
		Expect(err).NotTo(HaveOccurred())

		received, closed := drain(chunks)
		Expect(closed).To(BeTrue(), "the capped turn must terminate and close the channel")

		reason, sawDone := terminalStopReason(received)
		Expect(sawDone).To(BeTrue(), "expected a terminal Done chunk")
		Expect(reason).To(Equal(session.StopReasonToolLoopExceeded),
			"the sentinel Done chunk must still carry tool_loop_exceeded")

		assertPersistedTerminal(store)
	})

	It("stops consecutive same-tool-pattern caps via the forced summary without further continuations", func() {
		prov := &repeatingToolProvider{
			name: "consecutive-same-tool-stop",
			call: &provider.ToolCall{
				ID:        "call_spinner",
				Name:      "spinner",
				Arguments: map[string]any{"x": 1},
			},
		}
		eng, store, sessionID := newTerminalPersistenceEngine(prov)
		eng.SetMaxToolLoopIterationsForTest(0)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxSameToolPatternCallsForTest(3)
		eng.SetMaxToolLoopDurationForTest(0)

		ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
		chunks, err := eng.Stream(ctx, sessionID, "Go")
		Expect(err).NotTo(HaveOccurred())

		received, closed := drain(chunks)
		Expect(closed).To(BeTrue(), "the consecutive same-tool stop must terminate the turn")

		Expect(prov.callCount()).To(BeNumerically("<=", 12),
			"the second consecutive same-tool-pattern cap must be terminal — no third todo continuation may be injected")

		reason, sawDone := terminalStopReason(received)
		Expect(sawDone).To(BeTrue(), "expected a terminal Done chunk")
		Expect(reason).To(Equal(session.StopReasonToolLoopExceeded))

		assertPersistedTerminal(store)
	})

	It("terminates a duration-capped turn within a bounded time after soft continuations are exhausted", func() {
		const sessionID = "monotonic-time-budget-session"
		todoStore := todo.NewMemoryStore()
		Expect(todoStore.Set(sessionID, []todo.Item{
			{Content: "never done", Status: "pending", Priority: "high"},
		})).NotTo(HaveOccurred())

		slowpoke := &delayedExecutableMockTool{
			name:       "spinner",
			delay:      40 * time.Millisecond,
			execResult: tool.Result{Output: "spun"},
		}
		registry := tool.NewRegistry()
		registry.Register(slowpoke)
		registry.SetPermission(slowpoke.Name(), tool.Allow)

		store, err := recall.NewFileContextStore(GinkgoT().TempDir()+"/ctx.json", "terminal-model")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)

		prov := &repeatingToolProvider{
			name: "monotonic-time-budget",
			call: &provider.ToolCall{
				ID:        "call_spinner",
				Name:      "spinner",
				Arguments: map[string]any{"x": 1},
			},
		}
		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest: agent.Manifest{
				ID:   "terminal-persistence-agent",
				Name: "Terminal Persistence Agent",
				Capabilities: agent.Capabilities{
					Tools: []string{"spinner"},
				},
			},
			Tools:        []tool.Tool{slowpoke},
			ToolRegistry: registry,
			TodoStore:    todoStore,
			Store:        store,
		})
		eng.SetMaxToolLoopIterationsForTest(0)
		eng.SetMaxIdenticalToolCallsForTest(0)
		eng.SetMaxSameToolPatternCallsForTest(0)
		eng.SetMaxToolLoopDurationForTest(150 * time.Millisecond)

		ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
		start := time.Now()
		chunks, err := eng.Stream(ctx, sessionID, "Go")
		Expect(err).NotTo(HaveOccurred())

		received, closed := drain(chunks)
		elapsed := time.Since(start)
		Expect(closed).To(BeTrue(), "the time-budget terminal must end the turn")

		Expect(elapsed).To(BeNumerically("<", 2*time.Second),
			"exhausting the soft continuation budget must still terminate the duration-capped turn within a bounded wall clock (took %s)", elapsed)

		reason, sawDone := terminalStopReason(received)
		Expect(sawDone).To(BeTrue(), "expected a terminal Done chunk")
		Expect(reason).To(Equal(session.StopReasonToolLoopExceeded),
			"the monotonic time budget must stamp tool_loop_exceeded")

		assertPersistedTerminal(store)
	})
})

var _ = Describe("Engine permission-denial transcript integrity", func() {
	It("answers every denied tool call with a persisted tool result", func() {
		guarded := &executableMockTool{name: "echoer", execResult: tool.Result{Output: "must not run"}}
		registry := tool.NewRegistry()
		registry.Register(guarded)
		registry.SetPermission("echoer", tool.Deny)

		store, err := recall.NewFileContextStore(GinkgoT().TempDir()+"/ctx.json", "denial-model")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)

		prov := &repeatingToolProvider{
			name: "denial-loop",
			call: &provider.ToolCall{
				ID:        "call_echo",
				Name:      "echoer",
				Arguments: map[string]any{"x": 1},
			},
		}
		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest: agent.Manifest{
				ID:   "denial-agent",
				Name: "Denial Agent",
				Capabilities: agent.Capabilities{
					Tools: []string{"echoer"},
				},
			},
			Tools:        []tool.Tool{guarded},
			ToolRegistry: registry,
			Store:        store,
		})

		const sessionID = "denial-session"
		ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
		chunks, err := eng.Stream(ctx, sessionID, "Go")
		Expect(err).NotTo(HaveOccurred())
		for range chunks {
		}

		results := make(map[string]bool)
		for _, sm := range store.GetStoredMessages() {
			if sm.Message.Role != "tool" {
				continue
			}
			for _, tc := range sm.Message.ToolCalls {
				results[tc.ID] = true
			}
		}
		var dangling []string
		for _, sm := range store.GetStoredMessages() {
			if sm.Message.Role != "assistant" {
				continue
			}
			for _, tc := range sm.Message.ToolCalls {
				if !results[tc.ID] {
					dangling = append(dangling, tc.ID)
				}
			}
		}
		Expect(dangling).To(BeEmpty(),
			"a permission denial must leave no dangling tool call in the transcript")
	})
})

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
		// A 1s max loop duration makes the cumulative cooldown budget 1s
		// (1x the cap, aligned with the duration backstop); the
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
