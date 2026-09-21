//go:build e2e

package support

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/todo"
)

// loopGuardSteps holds per-scenario state for the loop-based budget guard
// feature. It wires purpose-built scripted providers (varied-args and
// identical-args) into a fresh engine with a small iteration budget and
// asserts whether the tool-loop guard fired.
type loopGuardSteps struct {
	maxIterations int
	provider      provider.Provider
	tools         []tool.Tool
	todoStore     *todo.MemoryStore
	stopReasons   []string
	rejectedTodo  bool
	ran           bool
}

// loopGuardTurn is one scripted provider turn.
type loopGuardTurn struct {
	content  string
	toolName string
	args     map[string]any
}

// loopGuardScriptedProvider replays a fixed script of tool-carrying turns.
// When varyArgs is true, tool-call arguments embed the call index (modelling
// steady productive variation); when false, every call carries identical
// arguments. It also records tool results so todo-guard rejections can be
// detected.
type loopGuardScriptedProvider struct {
	name       string
	turns      []loopGuardTurn
	varyArgs   bool
	rejectText string

	mu        sync.Mutex
	calls     int
	sawReject bool
}

func (p *loopGuardScriptedProvider) Name() string { return p.name }

func (p *loopGuardScriptedProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()

	turn := loopGuardTurn{content: "All done."}
	if n := len(p.turns); n > 0 {
		if idx < n {
			turn = p.turns[idx]
		} else {
			turn = p.turns[n-1]
		}
	}

	args := turn.args
	if args == nil {
		args = map[string]any{}
	}
	if p.varyArgs {
		args["i"] = idx
	} else {
		args["i"] = 0
	}

	tc := provider.ToolCall{
		ID:        fmt.Sprintf("call_%d", idx),
		Name:      turn.toolName,
		Arguments: args,
	}
	ch := make(chan provider.StreamChunk, 4)
	go func() {
		defer close(ch)
		if turn.content != "" {
			ch <- provider.StreamChunk{Content: turn.content}
		}
		if turn.toolName != "" {
			ch <- provider.StreamChunk{EventType: "tool_call", ToolCall: &tc}
		}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch, nil
}

func (p *loopGuardScriptedProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *loopGuardScriptedProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

func (p *loopGuardScriptedProvider) Models() ([]provider.Model, error) { return nil, nil }

// loopGuardContains reports whether s contains sub.
func loopGuardContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// loopGuardWorkTool is a trivial productive tool.
type loopGuardWorkTool struct{ name string }

func (t *loopGuardWorkTool) Name() string        { return t.name }
func (t *loopGuardWorkTool) Description() string { return "does a unit of work" }
func (t *loopGuardWorkTool) Execute(context.Context, tool.Input) (tool.Result, error) {
	return tool.Result{Output: "worked"}, nil
}
func (t *loopGuardWorkTool) Schema() tool.Schema { return tool.Schema{} }

// callCount reports provider invocations.
func (p *loopGuardScriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// runLoopGuardTurn streams one turn and records terminal stop reasons plus
// any todo-guard rejection text seen in streamed content.
func (s *loopGuardSteps) runLoopGuardTurn(sessionID, prompt string) error {
	manifest := agent.Manifest{
		ID:   "loop-guard-agent",
		Name: "Loop Guard Agent",
		Capabilities: agent.Capabilities{
			Tools: toolNames(s.tools),
		},
	}
	cfg := engine.Config{
		ChatProvider:          s.provider,
		Manifest:              manifest,
		Tools:                 s.tools,
		MaxToolLoopIterations: s.maxIterations,
	}
	if s.todoStore != nil {
		cfg.TodoStore = s.todoStore
	}
	eng := engine.New(cfg)

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), session.IDKey{}, sessionID))
	defer cancel()
	chunks, err := eng.Stream(ctx, sessionID, prompt)
	if err != nil {
		return err
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for chunk := range chunks {
			if chunk.Done {
				s.stopReasons = append(s.stopReasons, chunk.StopReason)
			}
			if loopGuardContains(chunk.Content, "cannot complete this todo item") {
				s.rejectedTodo = true
			}
		}
	}()
	select {
	case <-drained:
		s.ran = true
		return nil
	case <-time.After(10 * time.Second):
		cancel()
		return fmt.Errorf("turn did not finish within 10s")
	}
}

func toolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name())
	}
	return names
}

func (s *loopGuardSteps) observedStop(reason string) bool {
	for _, got := range s.stopReasons {
		if got == reason {
			return true
		}
	}
	return false
}

// RegisterLoopGuardSteps wires the loop-based budget guard feature steps.
func RegisterLoopGuardSteps(ctx *godog.ScenarioContext) {
	s := &loopGuardSteps{}
	ctx.Step(`^an engine with a loop guard iteration budget of (\d+)$`, s.engineWithIterationBudget)
	ctx.Step(`^a provider that makes varied productive tool calls each round$`, s.variedProductiveProvider)
	ctx.Step(`^a provider that repeats one identical tool call with identical arguments$`, s.identicalArgsProvider)
	ctx.Step(`^a provider that makes 8 distinct tool calls$`, s.eightDistinctToolCallsProvider)
	ctx.Step(`^a provider that repeats arguments twice then changes them$`, s.repeatThenChangeArgsProvider)
	ctx.Step(`^the guarded turn runs$`, s.theTurnRuns)
	ctx.Step(`^the turn completes without StopReason "StopReasonToolLoopExceeded"$`, s.turnCompletesWithoutLoopExceeded)
	ctx.Step(`^the guarded turn terminates with StopReason "StopReasonToolLoopExceeded"$`, s.turnTerminatesWithLoopExceeded)
	ctx.Step(`^an engine with a todo store and a productive provider$`, s.engineWithTodoStoreAndProductiveProvider)
	ctx.Step(`^the provider did real work, then calls todo_clear followed by a final todo_update marking completion$`, s.providerDoesWorkThenShutdownBookkeeping)
	ctx.Step(`^the todo bookkeeping calls are not rejected as "no work done"$`, s.todoBookkeepingNotRejected)
}

func (s *loopGuardSteps) engineWithIterationBudget(n int) error {
	s.maxIterations = n
	s.provider = nil
	s.tools = nil
	s.todoStore = nil
	s.stopReasons = nil
	s.rejectedTodo = false
	s.ran = false
	return nil
}

// variedProductiveProvider scripts three different work tools with varied
// arguments, then finishes. Under a budget of 3 this is productive work that
// the loop guard must NOT kill.
func (s *loopGuardSteps) variedProductiveProvider() error {
	s.tools = []tool.Tool{
		&loopGuardWorkTool{name: "research"},
		&loopGuardWorkTool{name: "implement"},
		&loopGuardWorkTool{name: "verify"},
	}
	prov := &loopGuardScriptedProvider{name: "varied-provider", varyArgs: true}
	prov.turns = []loopGuardTurn{
		{content: "researching", toolName: "research"},
		{content: "implementing", toolName: "implement"},
		{content: "verifying", toolName: "verify"},
		{content: "All done."},
	}
	s.provider = prov
	return nil
}

// identicalArgsProvider scripts the same tool with identical arguments
// forever — the classic loop the guard MUST stop.
func (s *loopGuardSteps) identicalArgsProvider() error {
	s.tools = []tool.Tool{&loopGuardWorkTool{name: "research"}}
	prov := &loopGuardScriptedProvider{name: "identical-provider", varyArgs: false}
	prov.turns = []loopGuardTurn{
		{content: "working", toolName: "research"},
	}
	s.provider = prov
	return nil
}

// eightDistinctToolCallsProvider scripts 8 rounds of the same tool name but
// with distinct arguments each round (varyArgs), then finishes. Distinct
// fingerprints must keep identicalRun at 1 — no behavioural stop — while the
// sameToolPattern detector is exercised but the iteration budget of 20 in the
// feature is high enough that the soft-trip continuation keeps the turn alive.
func (s *loopGuardSteps) eightDistinctToolCallsProvider() error {
	s.tools = []tool.Tool{
		&loopGuardWorkTool{name: "work_0"}, &loopGuardWorkTool{name: "work_1"},
		&loopGuardWorkTool{name: "work_2"}, &loopGuardWorkTool{name: "work_3"},
		&loopGuardWorkTool{name: "work_4"}, &loopGuardWorkTool{name: "work_5"},
		&loopGuardWorkTool{name: "work_6"}, &loopGuardWorkTool{name: "work_7"},
	}
	prov := &loopGuardScriptedProvider{name: "distinct-provider", varyArgs: true}
	prov.turns = []loopGuardTurn{
		{content: "step 1", toolName: "work_0"},
		{content: "step 2", toolName: "work_1"},
		{content: "step 3", toolName: "work_2"},
		{content: "step 4", toolName: "work_3"},
		{content: "step 5", toolName: "work_4"},
		{content: "step 6", toolName: "work_5"},
		{content: "step 7", toolName: "work_6"},
		{content: "step 8", toolName: "work_7"},
		{content: "All done."},
	}
	s.provider = prov
	return nil
}

// repeatThenChangeArgsProvider alternates argument groups: same args twice
// (identicalRun reaches 2), then changed args (reset to 1), then same as the
// changed pair once more. identicalRun never reaches the threshold of 3, so
// the turn must NOT be stopped with StopReasonToolLoopExceeded.
func (s *loopGuardSteps) repeatThenChangeArgsProvider() error {
	s.tools = []tool.Tool{
		&loopGuardWorkTool{name: "work_a"}, &loopGuardWorkTool{name: "work_b"},
	}
	prov := &loopGuardScriptedProvider{name: "alternating-provider", varyArgs: false}
	prov.turns = []loopGuardTurn{
		{content: "first", toolName: "work_a", args: map[string]any{"k": 0}},
		{content: "repeat a", toolName: "work_a", args: map[string]any{"k": 0}},
		{content: "switch", toolName: "work_a", args: map[string]any{"k": 1}},
		{content: "repeat changed", toolName: "work_b", args: map[string]any{"k": 1}},
		{content: "All done."},
	}
	s.provider = prov
	return nil
}

func (s *loopGuardSteps) theTurnRuns() error {
	return s.runLoopGuardTurn("loop-guard-session", "Go")
}

func (s *loopGuardSteps) turnCompletesWithoutLoopExceeded() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if s.observedStop(session.StopReasonToolLoopExceeded) {
		return fmt.Errorf("productive varied turn was killed by the tool-loop guard (stop reason tool_loop_exceeded)")
	}
	return nil
}

func (s *loopGuardSteps) turnTerminatesWithLoopExceeded() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if !s.observedStop(session.StopReasonToolLoopExceeded) {
		return fmt.Errorf("expected terminal tool_loop_exceeded stop reason")
	}
	return nil
}

// engineWithTodoStoreAndProductiveProvider wires a real todo toolset plus a
// work tool; the todo guard's session state is seeded by the scripted turns.
func (s *loopGuardSteps) engineWithTodoStoreAndProductiveProvider() error {
	s.maxIterations = 100
	s.todoStore = todo.NewMemoryStore()
	s.tools = []tool.Tool{
		&loopGuardWorkTool{name: "research"},
		todo.New(s.todoStore),
		todo.NewUpdate(s.todoStore),
		todo.NewClear(s.todoStore),
	}
	prov := &loopGuardScriptedProvider{name: "todo-provider", varyArgs: true}
	s.provider = prov
	return nil
}

// providerDoesWorkThenShutdownBookkeeping scripts: create todo list, do one
// real work call, complete the item, clear the list (shutdown bookkeeping),
// then a final todo_update marking completion. The final completed mark
// arrives with no intervening work — the guard must not treat it as fatal.
func (s *loopGuardSteps) providerDoesWorkThenShutdownBookkeeping() error {
	prov, ok := s.provider.(*loopGuardScriptedProvider)
	if !ok {
		return fmt.Errorf("expected loopGuardScriptedProvider")
	}
	prov.turns = []loopGuardTurn{
		{content: "planning", toolName: "todowrite", args: map[string]any{
			"items": []any{map[string]any{"content": "do the thing", "status": "pending"}},
		}},
		{content: "working", toolName: "research"},
		{content: "completing", toolName: "todo_update", args: map[string]any{
			"index": 0, "status": "completed",
		}},
		{content: "shutting down", toolName: "todo_clear", args: map[string]any{}},
		{content: "final mark", toolName: "todo_update", args: map[string]any{
			"index": 0, "status": "completed",
		}},
		{content: "All done."},
	}
	return nil
}

func (s *loopGuardSteps) todoBookkeepingNotRejected() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if s.rejectedTodo {
		return fmt.Errorf("shutdown todo bookkeeping was rejected as no-work-done; guard must be non-fatal here")
	}
	return nil
}
