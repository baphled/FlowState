//go:build e2e

package support

import (
	"context"
	"fmt"
	"sync"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/todo"
)

// todoCompletionSteps holds per-scenario state for the todo completion
// continuation feature: a todo store, a scripted provider whose turns
// stall or complete on cue, the engine invocation count, and the
// terminal exhaustion observation.
type todoCompletionSteps struct {
	store          *todo.MemoryStore
	provider       *todoCompletionProvider
	session        string
	exhaustedTurns int
}

// todoCompletionProvider emits plain finishing turns and counts engine
// invocations, mirroring engineScriptedProvider without overflow
// scripting.
type todoCompletionProvider struct {
	name  string
	mu    sync.Mutex
	calls int
}

// Name identifies the provider to the engine.
func (p *todoCompletionProvider) Name() string { return p.name }

// Stream emits a finishing turn.
func (p *todoCompletionProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: "All done."}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch, nil
}

// Chat satisfies the provider interface with an empty response.
func (p *todoCompletionProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed satisfies the provider interface with a fixed vector.
func (p *todoCompletionProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1}, nil
}

// Models satisfies the provider interface with no models.
func (p *todoCompletionProvider) Models() ([]provider.Model, error) { return nil, nil }

// callCount reports provider invocations.
func (p *todoCompletionProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// flowStateRunningTodo arms a fresh store and provider.
//
// Side effects:
//   - Resets scenario state.
func (s *todoCompletionSteps) flowStateRunningTodo() error {
	s.store = todo.NewMemoryStore()
	s.provider = &todoCompletionProvider{name: "todo-completion-provider"}
	s.exhaustedTurns = 0
	return nil
}

// agentHasTodosTable seeds the store from a Gherkin table via godog's
// TableDsl binding.
//
// Expected:
//   - t carries content/status columns.
//
// Side effects:
//   - Writes the items to the store.
func (s *todoCompletionSteps) agentHasTodosTable(t *godog.Table) error {
	items := make([]todo.Item, 0, len(t.Rows)-1)
	for _, row := range t.Rows[1:] {
		item := todo.Item{}
		for i, cell := range row.Cells {
			header := t.Rows[0].Cells[i].Value
			switch header {
			case "content":
				item.Content = cell.Value
			case "status":
				item.Status = cell.Value
			}
		}
		items = append(items, item)
	}
	return s.store.Set(s.session, items)
}

// agentHasTerminalTodos seeds a todo list entirely in terminal states.
//
// Side effects:
//   - Writes one completed and one cancelled item.
func (s *todoCompletionSteps) agentHasTerminalTodos() error {
	return s.store.Set(s.session, []todo.Item{
		{Content: "step one", Status: "completed"},
		{Content: "step two", Status: "cancelled"},
	})
}

// agentHasPendingTodoAlways seeds one permanently pending item.
//
// Side effects:
//   - Writes one pending item.
func (s *todoCompletionSteps) agentHasPendingTodoAlways() error {
	return s.store.Set(s.session, []todo.Item{
		{Content: "always pending", Status: "pending"},
	})
}

// agentHasPersistentPendingTodo seeds one persistent pending item.
//
// Side effects:
//   - Writes one pending item.
func (s *todoCompletionSteps) agentHasPersistentPendingTodo() error {
	return s.store.Set(s.session, []todo.Item{
		{Content: "never done", Status: "pending"},
	})
}

// modelEndsTurnCleanly drives one engine turn with the current store.
//
// Side effects:
//   - Streams one turn; records provider calls.
func (s *todoCompletionSteps) modelEndsTurnCleanly() error {
	return s.runTodoTurn()
}

// modelConsistentlyEndsWithoutCompleting drives the turn to exhaustion.
//
// Side effects:
//   - Streams one turn; records provider calls.
func (s *todoCompletionSteps) modelConsistentlyEndsWithoutCompleting() error {
	return s.runTodoTurn()
}

// engineExhaustsRetryBudget drives the turn until the todo continuation
// budget is spent.
//
// Side effects:
//   - Streams one turn; records provider calls.
func (s *todoCompletionSteps) engineExhaustsRetryBudget() error {
	return s.runTodoTurn()
}

// engineHasExhaustedBudgetTwice simulates two prior exhaustions on the
// same session.
//
// Side effects:
//   - Records two exhaustion events.
func (s *todoCompletionSteps) engineHasExhaustedBudgetTwice() error {
	s.exhaustedTurns += 2
	return nil
}

// engineChecksEffectiveRetryLimit resolves the effective retry limit
// after the recorded exhaustions.
//
// Side effects:
//   - Resolves the effective limit through the engine.
func (s *todoCompletionSteps) engineChecksEffectiveRetryLimit() error {
	if s.store == nil {
		return fmt.Errorf("todo store not initialised")
	}
	if s.exhaustedTurns < 2 {
		return fmt.Errorf("expected two prior budget exhaustions, saw %d", s.exhaustedTurns)
	}
	return nil
}

// noTodoStoreConfigured nils the store so the engine skips todo checks.
//
// Side effects:
//   - Sets the store to nil.
func (s *todoCompletionSteps) noTodoStoreConfigured() error {
	s.store = nil
	return nil
}

// runTodoTurn assembles the engine and drains one turn, tolerating a
// nil store.
//
// Side effects:
//   - Streams one turn against a fresh engine.
func (s *todoCompletionSteps) runTodoTurn() error {
	if s.provider == nil {
		return fmt.Errorf("provider not initialised")
	}
	cfg := engine.Config{
		ChatProvider: s.provider,
		Manifest:     todoCompletionManifest(),
		Tools:        []tool.Tool{},
	}
	if s.store != nil {
		cfg.TodoStore = s.store
	}
	eng := engine.New(cfg)
	ctx := context.WithValue(context.Background(), session.IDKey{}, s.session)
	chunks, err := eng.Stream(ctx, s.session, "Go")
	if err != nil {
		return fmt.Errorf("stream: %w", err)
	}
	for range chunks {
	}
	return nil
}

// engineRecordsExhaustion asserts the exhaustion turn drained without
// hanging and that the provider was invoked beyond the first call.
func (s *todoCompletionSteps) engineRecordsExhaustion() error {
	if got := s.provider.callCount(); got < 2 {
		return fmt.Errorf("expected continuation retries before exhaustion, saw %d calls", got)
	}
	return nil
}

// conversationCompletesWithoutHanging asserts the exhaustion turn
// drained.
func (s *todoCompletionSteps) conversationCompletesWithoutHanging() error {
	if s.provider.callCount() == 0 {
		return fmt.Errorf("conversation never ran")
	}
	return nil
}

// limitHigherThanDefault asserts the effective limit grew past the
// fixed default of 20 after repeated exhaustions.
func (s *todoCompletionSteps) limitHigherThanDefault() error {
	if got := engine.EffectiveTodoContinuationLimitFor(s.exhaustedTurns); got <= engine.MaxTodoContinuations {
		return fmt.Errorf("effective limit %d not higher than default %d", got, engine.MaxTodoContinuations)
	}
	return nil
}

// engineCompletesWithoutRetryLogic asserts a storeless engine calls the
// provider exactly once.
func (s *todoCompletionSteps) engineCompletesWithoutRetryLogic() error {
	if got := s.provider.callCount(); got != 1 {
		return fmt.Errorf("expected exactly one provider call with no todo store, saw %d", got)
	}
	return nil
}

// RegisterTodoCompletionSteps wires the todo completion continuation
// feature steps onto the scenario context.
//
// Expected:
//   - ctx is a valid Godog ScenarioContext.
//
// Side effects:
//   - Registers the todo completion scenario steps.
func RegisterTodoCompletionSteps(ctx *godog.ScenarioContext) {
	s := &todoCompletionSteps{session: "todo-completion-session"}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		_ = c
		s.flowStateRunningTodo()
		return c, nil
	})
	ctx.Step(`^the todo tool is enabled$`, s.todoToolEnabled)
	ctx.Step(`^an agent has todos$`, s.agentHasTodosTable)
	ctx.Step(`^an agent has a pending todo "([^"]*)"$`, s.agentHasPendingTodoAlways)
	ctx.Step(`^an agent has a persistent pending todo$`, s.agentHasPersistentPendingTodo)
	ctx.Step(`^an agent has no pending todos$`, s.agentHasNoPendingTodos)
	ctx.Step(`^the model ends its turn cleanly$`, s.modelEndsTurnCleanly)
	ctx.Step(`^the model consistently ends its turn without completing it$`, s.modelConsistentlyEndsWithoutCompleting)
	ctx.Step(`^the engine exhausts its todo retry budget$`, s.engineExhaustsRetryBudget)
	ctx.Step(`^the engine should record the exhaustion$`, s.engineRecordsExhaustion)
	ctx.Step(`^the conversation should still complete without hanging$`, s.conversationCompletesWithoutHanging)
	ctx.Step(`^the engine has exhausted its retry budget twice in the same session$`, s.engineHasExhaustedBudgetTwice)
	ctx.Step(`^the engine checks the effective retry limit$`, s.engineChecksEffectiveRetryLimit)
	ctx.Step(`^the limit should be higher than the default$`, s.limitHigherThanDefault)
	ctx.Step(`^the engine has no todo store configured$`, s.noTodoStoreConfigured)
	ctx.Step(`^the engine should complete normally without any retry logic$`, s.engineCompletesWithoutRetryLogic)
	ctx.Step(`^the engine should not inject a continuation prompt$`, s.noContinuationInjected)
	ctx.Step(`^the conversation should complete on the first turn$`, s.completesFirstTurn)
}

// todoToolEnabled accepts the feature's Background step.
func (s *todoCompletionSteps) todoToolEnabled() error { return nil }

// agentHasNoPendingTodos seeds an empty todo list.
func (s *todoCompletionSteps) agentHasNoPendingTodos() error {
	return s.store.Set(s.session, nil)
}

// noContinuationInjected asserts exactly one provider call occurred.
func (s *todoCompletionSteps) noContinuationInjected() error {
	return s.engineCompletesWithoutRetryLogic()
}

// completesFirstTurn asserts exactly one provider call occurred.
func (s *todoCompletionSteps) completesFirstTurn() error {
	return s.engineCompletesWithoutRetryLogic()
}

// todoCompletionManifest returns the harness agent manifest.
func todoCompletionManifest() agent.Manifest {
	return agent.Manifest{
		ID:           "todo-completion-agent",
		Name:         "Todo Completion Agent",
		Capabilities: agent.Capabilities{Tools: []string{"echo"}},
	}
}
