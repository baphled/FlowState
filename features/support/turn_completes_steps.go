//go:build e2e

// Package support provides BDD test step definitions and helpers.
package support

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/todo"
)

// turnCompletesSteps holds per-scenario state for the turn-completion
// invariant feature. Each scenario wires a scripted provider plus mock
// tools into a fresh engine with a real context store, streams one turn
// under a guard, then asserts on the persisted transcript and the
// terminal chunks observed on the wire.
type turnCompletesSteps struct {
	sessionID   string
	provider    *invariantScriptedProvider
	tools       []tool.Tool
	todoStore   *todo.MemoryStore
	store       *recall.FileContextStore
	storeDir    string
	registry    *tool.Registry
	maxIter     int
	maxDuration time.Duration
	capturer    *invariantLogCapturer
	doneChunks  int
	stopReasons []string
	errorSeen   bool
	ran         bool
}

// invariantTurn describes one scripted provider turn: assistant text and
// an optional tool call. Script exhaustion repeats the final turn.
type invariantTurn struct {
	content  string
	toolName string
}

// invariantScriptedProvider replays a script of tool-carrying turns,
// records how many times the engine called it, and keeps a copy of every
// ChatRequest so scenarios can assert on what the provider actually
// received. Tool-call arguments vary by call index so the identical-batch
// fingerprint detector never fires unless a scenario asks for it.
type invariantScriptedProvider struct {
	name  string
	turns []invariantTurn

	mu       sync.Mutex
	calls    int
	requests []provider.ChatRequest
}

// Name identifies the provider to the engine.
func (p *invariantScriptedProvider) Name() string { return p.name }

// Stream emits the scripted turn, varying tool-call arguments per call
// and recording the request the engine assembled.
func (p *invariantScriptedProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	copied := provider.ChatRequest{Provider: req.Provider, Model: req.Model}
	copied.Messages = append([]provider.Message(nil), req.Messages...)
	p.requests = append(p.requests, copied)
	p.mu.Unlock()

	turn := invariantTurn{content: "All done."}
	if n := len(p.turns); n > 0 {
		if idx < n {
			turn = p.turns[idx]
		} else {
			turn = p.turns[n-1]
		}
	}

	tc := provider.ToolCall{
		ID:        fmt.Sprintf("call_%d", idx),
		Name:      turn.toolName,
		Arguments: map[string]any{"i": idx},
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

// Chat satisfies the provider interface with an empty response.
func (p *invariantScriptedProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed satisfies the provider interface with a fixed vector.
func (p *invariantScriptedProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

// Models satisfies the provider interface with no models.
func (p *invariantScriptedProvider) Models() ([]provider.Model, error) { return nil, nil }

// callCount reports how many times the engine invoked the provider.
func (p *invariantScriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// recordedRequests returns the requests observed so far.
func (p *invariantScriptedProvider) recordedRequests() []provider.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.ChatRequest(nil), p.requests...)
}

// continuationRequestCount reports how many observed requests carried the
// engine's todo-continuation user message.
func (p *invariantScriptedProvider) continuationRequestCount() int {
	count := 0
	for _, req := range p.recordedRequests() {
		for _, m := range req.Messages {
			if m.Role != "user" {
				continue
			}
			if strings.Contains(m.Content, "incomplete tasks that still need to be completed") ||
				strings.Contains(m.Content, "must be completed before you can proceed") {
				count++
				break
			}
		}
	}
	return count
}

// invariantEchoTool returns a fixed output per execution, modelling a
// fast read-only tool.
type invariantEchoTool struct {
	name   string
	output string
}

// Name identifies the tool to the engine.
func (t *invariantEchoTool) Name() string { return t.name }

// Description satisfies the tool interface.
func (t *invariantEchoTool) Description() string { return "returns a fixed payload" }

// Execute returns the configured output.
func (t *invariantEchoTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	return tool.Result{Output: t.output}, nil
}

// Schema satisfies the tool interface with an empty schema.
func (t *invariantEchoTool) Schema() tool.Schema { return tool.Schema{} }

// invariantLogCapturer is an slog handler that counts every message the
// engine emits and records the trip reasons of "engine tool loop capped"
// warnings.
type invariantLogCapturer struct {
	mu       sync.Mutex
	messages map[string]int
	trips    []string
}

// Enabled reports all levels as capturable.
func (c *invariantLogCapturer) Enabled(context.Context, slog.Level) bool { return true }

// Handle counts the record's message and, for tool-loop cap warnings,
// records the trip attribute.
func (c *invariantLogCapturer) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.messages == nil {
		c.messages = make(map[string]int)
	}
	c.messages[r.Message]++
	if r.Message == "engine tool loop capped" {
		r.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "trip" {
				c.trips = append(c.trips, attr.Value.String())
			}
			return true
		})
	}
	return nil
}

// WithAttrs satisfies the slog.Handler interface.
func (c *invariantLogCapturer) WithAttrs([]slog.Attr) slog.Handler { return c }

// WithGroup satisfies the slog.Handler interface.
func (c *invariantLogCapturer) WithGroup(string) slog.Handler { return c }

// messageCount reports how many records carried the exact message.
func (c *invariantLogCapturer) messageCount(message string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.messages[message]
}

// continuationInjectionCount reports how many records were continuation
// injections, matched on the shared "injecting ... continuation" phrasing
// of every injection log site.
func (c *invariantLogCapturer) continuationInjectionCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for msg, n := range c.messages {
		if strings.Contains(msg, "injecting") && strings.Contains(msg, "continuation") {
			count += n
		}
	}
	return count
}

// hasTrip reports whether the given trip reason was captured.
func (c *invariantLogCapturer) hasTrip(reason string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, trip := range c.trips {
		if trip == reason {
			return true
		}
	}
	return false
}

// tripSnapshot copies the captured cap-trip reasons so assertion
// messages can quote the trips the engine actually took, giving the
// invariants ledger observed evidence rather than inferred causes.
func (c *invariantLogCapturer) tripSnapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.trips...)
}

// RegisterTurnCompletesSteps wires the turn-completion invariant feature
// steps onto the godog scenario context.
func RegisterTurnCompletesSteps(ctx *godog.ScenarioContext) {
	s := &turnCompletesSteps{}
	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset("turn-completes-session")
		return bddCtx, nil
	})
	ctx.AfterScenario(func(_ *godog.Scenario, err error) {
		s.teardown()
	})
	ctx.Step(`^a scripted model that answers the prompt directly$`, s.modelAnswersPromptDirectly)
	ctx.Step(`^a scripted model that re-requests one tool forever without narration$`, s.modelReRequestsToolWithoutNarration)
	ctx.Step(`^a scripted model that narrates while re-requesting one tool$`, s.modelNarratesWhileReRequestingTool)
	ctx.Step(`^a scripted model that requests a permission-denied tool once$`, s.modelRequestsPermissionDeniedTool)
	ctx.Step(`^a pending todo is seeded for the turn-completion session$`, s.pendingTodoSeeded)
	ctx.Step(`^the turn is streamed to completion$`, s.turnIsStreamedToCompletion)
	ctx.Step(`^the persisted transcript ends with an assistant message$`, s.transcriptEndsWithAssistantMessage)
	ctx.Step(`^a persisted assistant message carries stop reason "([^"]+)"$`, s.persistedAssistantCarriesStopReason)
	ctx.Step(`^exactly one terminal chunk closes the stream$`, s.exactlyOneTerminalChunk)
	ctx.Step(`^every tool call in the persisted transcript has a matching tool result$`, s.everyToolCallHasMatchingResult)
}

// reset rebuilds per-scenario state: a fresh scripted provider, echo
// tool, todo store, temp-dir context store, log capturer, and default
// loop budgets.
func (s *turnCompletesSteps) reset(sessionID string) {
	s.sessionID = sessionID
	s.provider = &invariantScriptedProvider{name: "invariant-provider"}
	s.tools = []tool.Tool{&invariantEchoTool{name: "echoer", output: "ok"}}
	s.todoStore = nil
	dir, err := os.MkdirTemp("", "turn-completes-ctx-*")
	if err != nil {
		return
	}
	store, err := recall.NewFileContextStore(dir+"/ctx.json", "invariant-model")
	if err != nil {
		return
	}
	s.storeDir = dir
	s.store = store
	s.registry = nil
	s.maxIter = 0
	s.maxDuration = 0
	s.capturer = &invariantLogCapturer{}
	s.doneChunks = 0
	s.stopReasons = nil
	s.errorSeen = false
	s.ran = false
}

// teardown closes the scenario's context store and removes its temp dir.
func (s *turnCompletesSteps) teardown() {
	if s.store != nil {
		s.store.Close()
		s.store = nil
	}
	if s.storeDir != "" {
		os.RemoveAll(s.storeDir)
		s.storeDir = ""
	}
}

// buildEngine assembles the engine from the current fixture state.
func (s *turnCompletesSteps) buildEngine() *engine.Engine {
	manifest := agent.Manifest{
		ID:   "turn-completes-agent",
		Name: "Turn Completes Agent",
		Capabilities: agent.Capabilities{
			Tools: []string{"echoer"},
		},
	}
	maxIter := s.maxIter
	if maxIter == 0 {
		maxIter = 10000
	}
	cfg := engine.Config{
		ChatProvider:          s.provider,
		Manifest:              manifest,
		Tools:                 s.tools,
		Store:                 s.store,
		ToolRegistry:          s.registry,
		MaxToolLoopIterations: maxIter,
		MaxToolLoopDuration:   s.maxDuration,
		ToolOutputRetention:   -1,
	}
	if s.todoStore != nil {
		cfg.TodoStore = s.todoStore
	}
	return engine.New(cfg)
}

// drainTurn streams the turn under the captured logger and a guard,
// counting terminal chunks and stop reasons.
func (s *turnCompletesSteps) drainTurn(eng *engine.Engine, guard time.Duration) error {
	previous := slog.Default()
	slog.SetDefault(slog.New(s.capturer))
	defer slog.SetDefault(previous)

	streamCtx, cancel := context.WithCancel(context.WithValue(context.Background(), session.IDKey{}, s.sessionID))
	defer cancel()

	chunks, err := eng.Stream(streamCtx, s.sessionID, "Go")
	if err != nil {
		return err
	}

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for chunk := range chunks {
			if chunk.Done {
				s.doneChunks++
				s.stopReasons = append(s.stopReasons, chunk.StopReason)
			}
			if chunk.Error != nil {
				s.errorSeen = true
			}
		}
	}()

	select {
	case <-drained:
		s.ran = true
		return nil
	case <-time.After(guard):
		cancel()
		return fmt.Errorf("turn did not terminate within %s", guard)
	}
}

// modelAnswersPromptDirectly scripts a single text-only turn.
func (s *turnCompletesSteps) modelAnswersPromptDirectly() error {
	s.provider.turns = []invariantTurn{{content: "All done."}}
	return nil
}

// modelReRequestsToolWithoutNarration scripts an empty-text tool call on
// every round so the same-tool-pattern detector is the intended cap.
func (s *turnCompletesSteps) modelReRequestsToolWithoutNarration() error {
	s.provider.turns = []invariantTurn{{toolName: "echoer"}}
	s.maxIter = 50
	return nil
}

// modelNarratesWhileReRequestingTool scripts narration text alongside a
// tool call on every round so the iteration backstop is the intended cap.
func (s *turnCompletesSteps) modelNarratesWhileReRequestingTool() error {
	s.provider.turns = []invariantTurn{{content: "Still working.", toolName: "echoer"}}
	s.maxIter = 3
	return nil
}

// modelRequestsPermissionDeniedTool scripts one tool call against a tool
// the registry denies outright.
func (s *turnCompletesSteps) modelRequestsPermissionDeniedTool() error {
	guarded := &invariantEchoTool{name: "echoer", output: "must not run"}
	registry := tool.NewRegistry()
	registry.Register(guarded)
	registry.SetPermission("echoer", tool.Deny)
	s.registry = registry
	s.provider.turns = []invariantTurn{{toolName: "echoer"}}
	return nil
}

// pendingTodoSeeded arms the todo store with one pending item so the
// todo-continuation machinery is active for the session.
func (s *turnCompletesSteps) pendingTodoSeeded() error {
	s.todoStore = todo.NewMemoryStore()
	return s.todoStore.Set(s.sessionID, []todo.Item{
		{Content: "finish the invariant work", Status: "pending", Priority: "high"},
	})
}

// turnIsStreamedToCompletion runs the assembled turn under a generous
// guard.
func (s *turnCompletesSteps) turnIsStreamedToCompletion() error {
	if s.store == nil {
		return fmt.Errorf("scenario store was not initialised")
	}
	return s.drainTurn(s.buildEngine(), 30*time.Second)
}

// transcriptEndsWithAssistantMessage asserts the persisted transcript's
// final message is an assistant message.
func (s *turnCompletesSteps) transcriptEndsWithAssistantMessage() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	messages := s.storedMessages()
	if len(messages) == 0 {
		return fmt.Errorf("persisted transcript is empty")
	}
	last := messages[len(messages)-1]
	if last.Role != "assistant" {
		return fmt.Errorf("persisted transcript ends with role %q (content %q); want an assistant message (cap trips %v, todo-continuation requests %d)",
			last.Role, truncateForLedger(last.Content, 80), s.capturer.tripSnapshot(), s.provider.continuationRequestCount())
	}
	return nil
}

// persistedAssistantCarriesStopReason asserts at least one stored
// assistant message was persisted with the quoted stop reason.
func (s *turnCompletesSteps) persistedAssistantCarriesStopReason(reason string) error {
	if reason != "StopReasonToolLoopExceeded" {
		return fmt.Errorf("unsupported stop-reason constant: %s", reason)
	}
	for _, m := range s.storedMessages() {
		if m.Role == "assistant" && m.StopReason == session.StopReasonToolLoopExceeded {
			return nil
		}
	}
	return fmt.Errorf("no persisted assistant message carries stop reason %s; terminal wire stop reasons were %v",
		session.StopReasonToolLoopExceeded, s.stopReasons)
}

// exactlyOneTerminalChunk asserts a single Done chunk closed the stream.
func (s *turnCompletesSteps) exactlyOneTerminalChunk() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if s.doneChunks != 1 {
		return fmt.Errorf("expected exactly one terminal chunk, saw %d (stop reasons %v)", s.doneChunks, s.stopReasons)
	}
	return nil
}

// everyToolCallHasMatchingResult asserts every tool_use block persisted
// in the transcript has a corresponding tool-result message.
func (s *turnCompletesSteps) everyToolCallHasMatchingResult() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	messages := s.storedMessages()
	results := make(map[string]bool)
	for _, m := range messages {
		if m.Role != "tool" {
			continue
		}
		for _, tc := range m.ToolCalls {
			results[tc.ID] = true
		}
	}
	var dangling []string
	for _, m := range messages {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if !results[tc.ID] {
				dangling = append(dangling, fmt.Sprintf("%s(%s)", tc.ID, tc.Name))
			}
		}
	}
	if len(dangling) > 0 {
		return fmt.Errorf("dangling tool calls without a tool result: %v", dangling)
	}
	return nil
}

// storedMessages returns the persisted transcript's provider messages.
func (s *turnCompletesSteps) storedMessages() []provider.Message {
	if s.store == nil {
		return nil
	}
	stored := s.store.GetStoredMessages()
	messages := make([]provider.Message, 0, len(stored))
	for _, sm := range stored {
		messages = append(messages, sm.Message)
	}
	return messages
}

// truncateForLedger shortens content for assertion messages.
func truncateForLedger(content string, max int) string {
	if len(content) <= max {
		return content
	}
	return content[:max] + "..."
}
