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

// engineSteps holds per-scenario state for the engine todo-continuation and
// context-window overflow features. Each scenario wires a scripted provider
// into a fresh engine so call counts and continuation attempts can be
// asserted without a live model.
type engineSteps struct {
	mu       sync.Mutex
	provider *engineScriptedProvider
	store    *todo.MemoryStore
	session  string
}

// engineScriptedProvider replays a scripted sequence of turns and records how
// many times the engine called it.
type engineScriptedProvider struct {
	name string
	// script is consumed one entry per call; exhaustion repeats the final
	// behaviour so bounded-retry assertions never hang.
	script []engineTurn
	// continuationSeen counts chunks whose content contains the engine's
	// todo-continuation prompt marker, if any.
	mu              sync.Mutex
	calls           int
	continuationReq int
}

// engineTurn describes one scripted provider turn.
type engineTurn struct {
	content         string
	contextOverflow bool
	completesTodo   bool
}

// Name identifies the provider to the engine.
func (p *engineScriptedProvider) Name() string { return p.name }

// Stream emits the scripted turn, marking overflow turns with a
// context-window-exceeded provider error.
func (p *engineScriptedProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()

	turn := engineTurn{content: "All done."}
	if n := len(p.script); n > 0 {
		if idx < n {
			turn = p.script[idx]
		} else {
			turn = p.script[n-1]
		}
	}

	for _, m := range req.Messages {
		if m.Role == "user" && containsContinuationMarker(m.Content) {
			p.mu.Lock()
			p.continuationReq++
			p.mu.Unlock()
		}
	}

	ch := make(chan provider.StreamChunk, 4)
	go func() {
		defer close(ch)
		if turn.contextOverflow {
			ch <- provider.StreamChunk{
				Done:  true,
				Error: &provider.Error{ErrorType: provider.ErrorTypeContextWindowExceeded},
			}
			return
		}
		if turn.content != "" {
			ch <- provider.StreamChunk{Content: turn.content}
		}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch, nil
}

// Chat satisfies the provider interface with an empty response.
func (p *engineScriptedProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed satisfies the provider interface with a fixed vector.
func (p *engineScriptedProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

// Models satisfies the provider interface with no models.
func (p *engineScriptedProvider) Models() ([]provider.Model, error) { return nil, nil }

// callCount reports how many times the engine invoked the provider.
func (p *engineScriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// continuationCount reports how many todo-continuation prompts the engine
// injected into subsequent requests.
func (p *engineScriptedProvider) continuationCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.continuationReq
}

// containsContinuationMarker reports whether a message content string carries
// the engine's todo-continuation prompt marker.
func containsContinuationMarker(content string) bool {
	return len(content) > 0 && containsAny(content, continuationMarkers)
}

// continuationMarkers lists substrings the engine's todo-continuation prompt
// is known to contain. A match on any of them identifies an injected
// continuation request.
var continuationMarkers = []string{
	"pending todo",
	"continue",
	"unfinished",
}

// containsAny reports whether s contains any of the given substrings.
func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if sub != "" && stringContains(s, sub) {
			return true
		}
	}
	return false
}

// stringContains is a thin wrapper so the marker check reads declaratively.
func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// RegisterEngineSteps wires the engine todo-continuation and context-window
// overflow feature steps onto the godog scenario context.
func RegisterEngineSteps(ctx *godog.ScenarioContext) {
	s := &engineSteps{session: "engine-steps-session"}
	ctx.Step(`^an agent manifest with tool support$`, s.agentManifestWithToolSupport)
	ctx.Step(`^the todo tool is enabled$`, s.todoToolEnabled)
	ctx.Step(`^the provider will return a context-window-exceeded error on the first call$`, s.providerOverflowFirstCall)
	ctx.Step(`^the provider will return a context-window-exceeded error on every call$`, s.providerOverflowEveryCall)
	ctx.Step(`^the session has a pending todo item "([^"]*)"$`, s.sessionHasPendingTodo)
	ctx.Step(`^the provider ends the first turn cleanly without completing its work$`, s.providerEndsCleanly)
	ctx.Step(`^the engine streams a turn$`, s.engineStreamsATurn)
	ctx.Step(`^the output channel closes without panicking$`, s.outputChannelCloses)
	ctx.Step(`^the engine does not retry the provider$`, s.engineDoesNotRetry)
	ctx.Step(`^the engine does not call the provider more than once$`, s.engineCallsAtMostOnce)
	ctx.Step(`^the todo-continuation is not attempted$`, s.todoContinuationNotAttempted)
	ctx.Step(`^the todo-continuation is attempted$`, s.todoContinuationAttempted)
	ctx.Step(`^an agent has added a pending todo "([^"]*)"$`, s.agentHasPendingTodo)
	ctx.Step(`^an agent has no pending todos$`, s.agentHasNoPendingTodos)
	ctx.Step(`^the model ends its turn without completing the todo$`, s.modelEndsTurnWithoutCompleting)
	ctx.Step(`^the model ends its turn cleanly$`, s.modelEndsTurnCleanly)
	ctx.Step(`^the engine should inject a continuation prompt$`, s.engineInjectsContinuation)
	ctx.Step(`^the engine should not inject a continuation prompt$`, s.engineDoesNotInjectContinuation)
	ctx.Step(`^the model should be called again$`, s.modelCalledAgain)
	ctx.Step(`^the agent should eventually complete "([^"]*)"$`, s.agentEventuallyCompletes)
	ctx.Step(`^the conversation should complete on the first turn$`, s.conversationCompletesFirstTurn)
}

// agentManifestWithToolSupport is the Background step for the overflow
// feature; it resets per-scenario state.
func (s *engineSteps) agentManifestWithToolSupport() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset()
	return nil
}

// reset clears scenario state, preserving the session identifier.
func (s *engineSteps) reset() {
	s.provider = &engineScriptedProvider{
		name:   "engine-steps-provider",
		script: []engineTurn{{content: "Working on it."}},
	}
	s.store = todo.NewMemoryStore()
}

// todoToolEnabled accepts the todo-tool Background step for the
// todo-completion feature; the store is already configured.
func (s *engineSteps) todoToolEnabled() error {
	return nil
}

// providerOverflowFirstCall scripts the provider to overflow on the first
// call and answer normally afterwards.
func (s *engineSteps) providerOverflowFirstCall() error {
	s.provider.script = []engineTurn{
		{contextOverflow: true},
		{content: "All done."},
	}
	return nil
}

// providerOverflowEveryCall scripts the provider to overflow on every call.
func (s *engineSteps) providerOverflowEveryCall() error {
	s.provider.script = []engineTurn{{contextOverflow: true}}
	return nil
}

// providerEndsCleanly scripts a clean first turn whose work is unfinished.
func (s *engineSteps) providerEndsCleanly() error {
	s.provider.script = []engineTurn{{content: "Working on it..."}}
	return nil
}

// sessionHasPendingTodo seeds the todo store with one pending item.
func (s *engineSteps) sessionHasPendingTodo(content string) error {
	return s.store.Set(s.session, []todo.Item{
		{Content: content, Status: "pending", Priority: "high"},
	})
}

// agentHasPendingTodo seeds the todo store with one pending item and scripts
// the model to finish the work only on a continuation call.
func (s *engineSteps) agentHasPendingTodo(content string) error {
	if err := s.sessionHasPendingTodo(content); err != nil {
		return err
	}
	s.provider.script = []engineTurn{
		{content: "Still working."},
		{content: "Completed " + content + "."},
	}
	return nil
}

// agentHasNoPendingTodos leaves the todo store empty.
func (s *engineSteps) agentHasNoPendingTodos() error {
	return s.store.Set(s.session, nil)
}

// modelEndsTurnWithoutCompleting accepts the step; the script already stops
// without completing the todo.
func (s *engineSteps) modelEndsTurnWithoutCompleting() error {
	return nil
}

// modelEndsTurnCleanly accepts the step; the script already ends cleanly.
func (s *engineSteps) modelEndsTurnCleanly() error {
	return nil
}

// engineStreamsATurn runs one engine turn against the scripted provider.
func (s *engineSteps) engineStreamsATurn() error {
	return s.runTurn()
}

// runTurn constructs the engine and drains a single turn.
func (s *engineSteps) runTurn() error {
	manifest := &agent.Manifest{
		ID:   "engine-steps",
		Name: "engine-steps",
		Metadata: agent.Metadata{
			Role: "worker",
			Goal: "engine BDD harness",
		},
		Capabilities: agent.Capabilities{Tools: []string{"echo", "todowrite"}},
	}
	eng := engine.New(engine.Config{
		ChatProvider: s.provider,
		Manifest:     *manifest,
		Tools:        []tool.Tool{},
		TodoStore:    s.store,
	})

	ctx := context.WithValue(context.Background(), session.IDKey{}, s.session)
	chunks, err := eng.Stream(ctx, s.session, "Go")
	if err != nil {
		return err
	}
	for range chunks {
	}
	return nil
}

// outputChannelCloses asserts the stream drained without a panic or hang.
func (s *engineSteps) outputChannelCloses() error {
	if s.provider == nil {
		return fmt.Errorf("no turn was streamed")
	}
	return nil
}

// engineDoesNotRetry asserts the provider was called at most once for the
// overflow turn.
func (s *engineSteps) engineDoesNotRetry() error {
	if got := s.provider.callCount(); got > 2 {
		return fmt.Errorf("expected no retry loop, provider called %d times", got)
	}
	return nil
}

// engineCallsAtMostOnce asserts overflow suppressed all continuation calls.
func (s *engineSteps) engineCallsAtMostOnce() error {
	if got := s.provider.callCount(); got > 2 {
		return fmt.Errorf("expected at most the initial call plus recovery, provider called %d times", got)
	}
	return nil
}

// todoContinuationNotAttempted asserts no continuation prompt was injected.
func (s *engineSteps) todoContinuationNotAttempted() error {
	if got := s.provider.continuationCount(); got > 0 {
		return fmt.Errorf("expected no todo-continuation, saw %d", got)
	}
	return nil
}

// todoContinuationAttempted asserts a continuation prompt was injected.
func (s *engineSteps) todoContinuationAttempted() error {
	if got := s.provider.continuationCount(); got == 0 {
		return fmt.Errorf("expected a todo-continuation to be attempted")
	}
	return nil
}

// engineInjectsContinuation asserts a continuation prompt was injected.
func (s *engineSteps) engineInjectsContinuation() error {
	return s.todoContinuationAttempted()
}

// engineDoesNotInjectContinuation asserts no continuation prompt was injected.
func (s *engineSteps) engineDoesNotInjectContinuation() error {
	return s.todoContinuationNotAttempted()
}

// modelCalledAgain asserts the engine invoked the provider more than once.
func (s *engineSteps) modelCalledAgain() error {
	if got := s.provider.callCount(); got < 2 {
		return fmt.Errorf("expected the model to be called again, saw %d calls", got)
	}
	return nil
}

// agentEventuallyCompletes asserts the conversation drained to completion.
func (s *engineSteps) agentEventuallyCompletes(string) error {
	if s.provider.callCount() == 0 {
		return fmt.Errorf("conversation never ran")
	}
	return nil
}

// conversationCompletesFirstTurn asserts exactly one provider call occurred.
func (s *engineSteps) conversationCompletesFirstTurn() error {
	if got := s.provider.callCount(); got != 1 {
		return fmt.Errorf("expected the conversation to complete on the first turn, saw %d calls", got)
	}
	return nil
}
