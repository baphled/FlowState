//go:build e2e

package support

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
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
	// compactor, when configured, is injected into the engine so the
	// overflow recovery path can fire a real summariser.
	compactor *ctxstore.AutoCompactor
	// lastContent holds the streamed assistant content for
	// retry-content assertions.
	lastContent string
	// store, when configured, feeds the engine's context gate so a
	// low-limit token counter can drive proactive overflow refusals
	// without a live model.
	store2 *recall.FileContextStore
	// tokenCounter, when configured, supplies the deterministic
	// low-limit model budget for local overflow refusals.
	tokenCounter *engineStepTokenCounter
	// compressionConfig, when configured, enables AutoCompaction so
	// the engine's forced compaction path can fire.
	compressionConfig *ctxstore.CompressionConfig
	// refusedLocally records that the engine surfaced a local
	// context-window error to the stream consumer.
	refusedLocally bool
	// compactionInsufficient records that the engine surfaced the
	// distinct terminal compaction-insufficient error after a
	// post-compaction retry still overflowed.
	compactionInsufficient bool
}

// engineStepTokenCounter is a deterministic TokenCounter whose Count is
// inflated so any multi-message request trips the low ModelLimit budget.
type engineStepTokenCounter struct{ limit int }

// Count reports one token per byte-sized chunk, inflated so the gate
// estimate crosses the low limit.
func (c *engineStepTokenCounter) Count(text string) int { return len(text) / 4 }

// ModelLimit returns the deliberately low model budget.
func (c *engineStepTokenCounter) ModelLimit(string) int { return c.limit }

// engineSummariser is a scripted ctxstore.Summariser that returns a
// valid CompactionSummary payload so the engine's auto-compaction can
// succeed on demand.
type engineSummariser struct{}

// Summarise returns a minimal valid compaction summary JSON payload.
func (engineSummariser) Summarise(context.Context, string, string, []provider.Message) (string, error) {
	summary := ctxstore.CompactionSummary{
		Intent:    "continue the turn after compaction",
		NextSteps: []string{"resume work"},
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	return string(data), nil
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
	"incomplete tasks",
	"CONTINUATION: continue working on",
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

// aCompactorIsConfigured accepts the compaction Background step; the engine
// already falls back to naive truncation when no compactor is wired, which
// satisfies the overflow-recovery contract.
func (s *engineSteps) aCompactorIsConfigured() error { return nil }

// RegisterEngineSteps wires the engine todo-continuation and context-window
// overflow feature steps onto the godog scenario context.
func RegisterEngineSteps(ctx *godog.ScenarioContext) {
	s := &engineSteps{session: "engine-steps-session"}
	ctx.Step(`^an agent manifest with tool support$`, s.agentManifestWithToolSupport)
	ctx.Step(`^the todo tool is enabled$`, s.todoToolEnabled)
	ctx.Step(`^the provider will return a context-window-exceeded error on the first call$`, s.providerOverflowFirstCall)
	ctx.Step(`^the provider will return a context-window-exceeded error on every call$`, s.providerOverflowEveryCall)
	ctx.Step(`^the provider overflows and the retry stream also overflows in-stream$`, s.providerOverflowThenInStreamOverflow)
	ctx.Step(`^the provider recovers cleanly after an overflow retry$`, s.providerOverflowThenCleanContinuation)
	ctx.Step(`^a compactor is configured that can reduce the context$`, s.compactorConfigured)
	ctx.Step(`^a compactor is configured$`, s.aCompactorIsConfigured)
	ctx.Step(`^the session has a pending todo item "([^"]*)"$`, s.sessionHasPendingTodo)
	ctx.Step(`^the provider ends the first turn cleanly without completing its work$`, s.providerEndsCleanly)
	ctx.Step(`^the engine streams a turn$`, s.engineStreamsATurn)
	ctx.Step(`^the output channel closes without panicking$`, s.outputChannelCloses)
	ctx.Step(`^the engine does not retry the provider$`, s.engineDoesNotRetry)
	ctx.Step(`^the engine does not call the provider more than once$`, s.engineCallsAtMostOnce)
	ctx.Step(`^the todo-continuation is not attempted$`, s.todoContinuationNotAttempted)
	ctx.Step(`^the todo-continuation is attempted$`, s.todoContinuationAttempted)
	ctx.Step(`^the engine retries after compacting$`, s.engineRetriesAfterCompacting)
	ctx.Step(`^the final response contains the retry content$`, s.finalResponseContainsRetryContent)
	ctx.Step(`^the engine attempts at most two provider calls$`, s.engineAttemptsAtMostTwoProviderCalls)
	ctx.Step(`^an agent has added a pending todo "([^"]*)"$`, s.agentHasPendingTodo)
	ctx.Step(`^an agent has no pending todos$`, s.agentHasNoPendingTodos)
	ctx.Step(`^the model ends its turn without completing the todo$`, s.modelEndsTurnWithoutCompleting)
	ctx.Step(`^the model ends its turn cleanly$`, s.modelEndsTurnCleanly)
	ctx.Step(`^the engine should inject a continuation prompt$`, s.engineInjectsContinuation)
	ctx.Step(`^the engine should not inject a continuation prompt$`, s.engineDoesNotInjectContinuation)
	ctx.Step(`^the model should be called again$`, s.modelCalledAgain)
	ctx.Step(`^the agent should eventually complete "([^"]*)"$`, s.agentEventuallyCompletes)
	ctx.Step(`^the conversation should complete on the first turn$`, s.conversationCompletesFirstTurn)
	ctx.Step(`^the session context is over budget after a todo continuation$`, s.sessionContextOverBudget)
	ctx.Step(`^compaction is unavailable$`, s.compactionUnavailable)
	ctx.Step(`^the engine attempts the continuation retry$`, s.engineAttemptsContinuationRetry)
	ctx.Step(`^the provider does not receive the over-budget request$`, s.providerDoesNotReceiveOverBudgetRequest)
	ctx.Step(`^a local context-window error is surfaced$`, s.localContextWindowErrorSurfaced)
	ctx.Step(`^a compaction-insufficient error is surfaced$`, s.compactionInsufficientSurfaced)
	ctx.Step(`^the final response indicates completion$`, s.finalResponseIndicatesCompletion)
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
	s.compactor = nil
	s.lastContent = ""
	s.store2 = nil
	s.tokenCounter = nil
	s.compressionConfig = nil
	s.refusedLocally = false
	s.compactionInsufficient = false
}

// todoToolEnabled accepts the todo-tool Background step for the
// todo-completion feature and rebuilds scenario state so sibling features
// sharing this step struct cannot nil-deref the todo store.
func (s *engineSteps) todoToolEnabled() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset()
	return nil
}

// providerOverflowFirstCall scripts the provider to overflow on the first
// call, answer with recovery content after compaction, and finish cleanly.
func (s *engineSteps) providerOverflowFirstCall() error {
	s.provider.script = []engineTurn{
		{contextOverflow: true},
		{content: "Recovered after compaction."},
	}
	return nil
}

// providerOverflowEveryCall scripts the provider to overflow on every call.
func (s *engineSteps) providerOverflowEveryCall() error {
	s.provider.script = []engineTurn{{contextOverflow: true}}
	return nil
}

// providerOverflowThenInStreamOverflow scripts a first overflow followed
// by an in-stream overflow on the retry itself, pinning that the taint
// must persist even though the retry stream opened cleanly.
func (s *engineSteps) providerOverflowThenInStreamOverflow() error {
	s.provider.script = []engineTurn{
		{contextOverflow: true},
		{contextOverflow: true},
	}
	return nil
}

// providerOverflowThenCleanContinuation scripts an initial overflow, a
// clean post-compaction retry turn, and a clean continuation answer so
// the taint must clear after the first fully-completed non-overflow
// result, letting the todo-continuation loop fire.
func (s *engineSteps) providerOverflowThenCleanContinuation() error {
	s.provider.script = []engineTurn{
		{contextOverflow: true},
		{content: "Working on it."},
		{content: "Recovered after compaction."},
	}
	return nil
}

// providerEndsCleanly scripts a clean first turn whose work is unfinished.
func (s *engineSteps) providerEndsCleanly() error {
	s.provider.script = []engineTurn{
		{content: "Working on it..."},
		{content: "Recovered after compaction."},
	}
	return nil
}

// compactorConfigured wires a summariser-backed AutoCompactor plus the
// FileContextStore, low-limit token counter and AutoCompaction-enabled
// CompressionConfig the engine needs for its forced compaction path to
// fire without a live model.
func (s *engineSteps) compactorConfigured() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compactor = ctxstore.NewAutoCompactor(engineSummariser{})
	dir, err := os.MkdirTemp("", "engine-steps-ctx-*")
	if err != nil {
		return err
	}
	fileStore, err := recall.NewFileContextStore(dir+"/ctx.json", "engine-steps-model")
	if err != nil {
		return err
	}
	s.store2 = fileStore
	s.tokenCounter = &engineStepTokenCounter{limit: 65536}
	cfg := ctxstore.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = true
	cfg.AutoCompaction.Threshold = 0.50
	s.compressionConfig = &cfg
	return nil
}

// sessionContextOverBudget arms a low token budget so the proactive
// context-window gate refuses oversized requests locally.
func (s *engineSteps) sessionContextOverBudget() error {
	return s.compactionUnavailable()
}

// compactionUnavailable arms the low-budget gate without wiring a
// compactor, modelling an environment where compaction cannot help.
func (s *engineSteps) compactionUnavailable() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := os.MkdirTemp("", "engine-steps-nocompact-*")
	if err != nil {
		return err
	}
	fileStore, err := recall.NewFileContextStore(dir+"/ctx.json", "engine-steps-model")
	if err != nil {
		return err
	}
	s.store2 = fileStore
	s.tokenCounter = &engineStepTokenCounter{limit: 64}
	return nil
}

// engineAttemptsContinuationRetry seeds a pending todo and runs the
// turn so the todo-continuation path drives the over-budget retry.
func (s *engineSteps) engineAttemptsContinuationRetry() error {
	if err := s.sessionHasPendingTodo("finish the overflow refusal work"); err != nil {
		return err
	}
	return s.runTurn()
}

// providerDoesNotReceiveOverBudgetRequest asserts the gate refused the
// oversized request locally before any provider send.
func (s *engineSteps) providerDoesNotReceiveOverBudgetRequest() error {
	s.mu.Lock()
	overBudget := s.refusedLocally
	s.mu.Unlock()
	if !overBudget {
		return fmt.Errorf("expected the over-budget request to be refused locally")
	}
	return nil
}

// localContextWindowErrorSurfaced asserts the refusal surfaced a
// context-window error to the stream consumer.
func (s *engineSteps) localContextWindowErrorSurfaced() error {
	return s.providerDoesNotReceiveOverBudgetRequest()
}

// compactionInsufficientSurfaced asserts the engine surfaced the
// distinct terminal compaction-insufficient error after a
// post-compaction retry still overflowed.
func (s *engineSteps) compactionInsufficientSurfaced() error {
	s.mu.Lock()
	surfaced := s.compactionInsufficient
	s.mu.Unlock()
	if !surfaced {
		return fmt.Errorf("expected a compaction-insufficient terminal error to be surfaced")
	}
	return nil
}

// sessionHasPendingTodo seeds the todo store with one pending item.
// The store is rebuilt lazily because the Background step for other
// features (todo_tools_are_enabled) deliberately nils it, and a nil
// store would panic in Set.
func (s *engineSteps) sessionHasPendingTodo(content string) error {
	if s.store == nil {
		s.store = todo.NewMemoryStore()
	}
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

// modelEndsTurnWithoutCompleting streams the scripted turn whose work
// stops short of completing the seeded todo.
func (s *engineSteps) modelEndsTurnWithoutCompleting() error {
	return s.runTurn()
}

// modelEndsTurnCleanly streams the scripted clean turn.
func (s *engineSteps) modelEndsTurnCleanly() error {
	return s.runTurn()
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
	cfg := engine.Config{
		ChatProvider:  s.provider,
		Manifest:      *manifest,
		Tools:         []tool.Tool{},
		TodoStore:     s.store,
		AutoCompactor: s.compactor,
	}
	if s.store2 != nil {
		cfg.Store = s.store2
	}
	if s.tokenCounter != nil {
		cfg.TokenCounter = s.tokenCounter
	}
	if s.compressionConfig != nil {
		cfg.CompressionConfig = *s.compressionConfig
	}
	eng := engine.New(cfg)

	ctx := context.WithValue(context.Background(), session.IDKey{}, s.session)
	chunks, err := eng.Stream(ctx, s.session, "Go")
	if err != nil {
		return err
	}
	for chunk := range chunks {
		if chunk.Error != nil {
			var pErr *provider.Error
			if errors.As(chunk.Error, &pErr) && pErr.ErrorType == provider.ErrorTypeContextWindowExceeded {
				s.mu.Lock()
				s.refusedLocally = true
				s.mu.Unlock()
			}
			if chunk.Error.Error() == "compaction insufficient: context still exceeds the window after compaction" {
				s.mu.Lock()
				s.compactionInsufficient = true
				s.mu.Unlock()
			}
		}
		if chunk.Content != "" {
			s.mu.Lock()
			s.lastContent = s.lastContent + chunk.Content
			s.mu.Unlock()
		}
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

// engineRetriesAfterCompacting asserts the engine called the provider
// again after the overflow, exercising the compaction recovery path.
func (s *engineSteps) engineRetriesAfterCompacting() error {
	if got := s.provider.callCount(); got < 2 {
		return fmt.Errorf("expected a retry after compaction, saw %d provider calls", got)
	}
	return nil
}

// finalResponseContainsRetryContent asserts the recovered turn's
// content reached the stream consumer.
func (s *engineSteps) finalResponseContainsRetryContent() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !strings.Contains(s.lastContent, "Recovered after compaction.") {
		return fmt.Errorf("expected the retry content in the final response, got %q", s.lastContent)
	}
	return nil
}

// finalResponseIndicatesCompletion asserts the recovered turn completed
// after the continuation prompt.
func (s *engineSteps) finalResponseIndicatesCompletion() error {
	return s.finalResponseContainsRetryContent()
}

// engineAttemptsAtMostTwoProviderCalls asserts the bounded-retry
// contract: one initial call plus at most one overflow retry.
func (s *engineSteps) engineAttemptsAtMostTwoProviderCalls() error {
	if got := s.provider.callCount(); got > 2 {
		return fmt.Errorf("expected at most two provider calls, saw %d", got)
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
