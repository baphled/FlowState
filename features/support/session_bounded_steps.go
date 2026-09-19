//go:build e2e

// Package support provides BDD test step definitions and helpers.
package support

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/todo"
)

// sessionBoundedSteps holds per-scenario state for the session-bounded
// lifecycle invariant feature. It mirrors the turn-completion harness
// (scripted provider, temp-dir context store, log capturer) and adds the
// compaction wiring, duration bookkeeping, and CompletionOrchestrator
// fakes the bounded-session scenarios need.
type sessionBoundedSteps struct {
	sessionID         string
	provider          *invariantScriptedProvider
	tools             []tool.Tool
	todoStore         *todo.MemoryStore
	store             *recall.FileContextStore
	storeDir          string
	maxIter           int
	maxDuration       time.Duration
	maxSessionTurns   int
	tokenCounter      *engineStepTokenCounter
	summariser        *boundedSliverSummariser
	compressionConfig *ctxstore.CompressionConfig
	capturer          *invariantLogCapturer
	ran               bool
	errorSeen         bool
	stopReasons       []string
	turnElapsed       time.Duration
	orchestrator      *engine.CompletionOrchestrator
	orchSender        *boundedOrchestratorSender
	orchBgMgr         *engine.BackgroundTaskManager
}

// boundedSliverSummariser is a scripted ctxstore.Summariser that counts
// its invocations and returns a summary whose payload is deliberately
// large relative to the compacted range, modelling a compaction that
// only ever trims a sliver of the window.
type boundedSliverSummariser struct {
	mu    sync.Mutex
	calls int
}

// Summarise returns a valid but bulky compaction summary and records the
// invocation.
func (s *boundedSliverSummariser) Summarise(_ context.Context, _, _ string, _ []provider.Message) (string, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	summary := ctxstore.CompactionSummary{
		Intent:    "continue the bounded session invariant scenario: " + strings.Repeat("detail ", 260),
		NextSteps: []string{"resume the bounded turn: " + strings.Repeat("step ", 60)},
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// callCount reports how many times the summariser ran.
func (s *boundedSliverSummariser) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// boundedOrchestratorSender is a fake engine.SessionMessageSender that
// records SendMessage calls and serves queued completion notifications.
type boundedOrchestratorSender struct {
	mu            sync.Mutex
	sendCalls     []string
	notifications map[string][]streaming.CompletionNotificationEvent
}

// SendMessage records the re-prompt and returns a short finished stream.
func (f *boundedOrchestratorSender) SendMessage(_ context.Context, sessionID string, _ string) (<-chan provider.StreamChunk, error) {
	f.mu.Lock()
	f.sendCalls = append(f.sendCalls, sessionID)
	f.mu.Unlock()
	ch := make(chan provider.StreamChunk, 2)
	ch <- provider.StreamChunk{Content: "re-prompt response"}
	ch <- provider.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

// GetNotifications retrieves and clears pending completion notifications.
func (f *boundedOrchestratorSender) GetNotifications(sessionID string) ([]streaming.CompletionNotificationEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	notifs := f.notifications[sessionID]
	delete(f.notifications, sessionID)
	return notifs, nil
}

// EnsureSession satisfies the sender interface.
func (f *boundedOrchestratorSender) EnsureSession(string, string) {}

// addNotification queues a completion notification for the session.
func (f *boundedOrchestratorSender) addNotification(sessionID string, n streaming.CompletionNotificationEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notifications == nil {
		f.notifications = make(map[string][]streaming.CompletionNotificationEvent)
	}
	f.notifications[sessionID] = append(f.notifications[sessionID], n)
}

// sendCount reports how many re-prompt sends were recorded.
func (f *boundedOrchestratorSender) sendCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sendCalls)
}

// boundedOrchestratorBroker is a fake engine.SessionBrokerPublisher that
// drains published streams.
type boundedOrchestratorBroker struct{}

// Publish drains the chunk channel.
func (b *boundedOrchestratorBroker) Publish(_ string, chunks <-chan provider.StreamChunk) {
	for range chunks {
	}
}

// RegisterSessionBoundedSteps wires the session-bounded lifecycle
// invariant feature steps onto the godog scenario context.
func RegisterSessionBoundedSteps(ctx *godog.ScenarioContext) {
	s := &sessionBoundedSteps{}
	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset("session-bounded-session")
		return bddCtx, nil
	})
	ctx.AfterScenario(func(_ *godog.Scenario, _ error) {
		s.teardown()
	})
	ctx.Step(`^a scripted model that narrates identical tool requests every round$`, s.modelNarratesIdenticalToolRequests)
	ctx.Step(`^a scripted model that keeps calling tools while its todo list never changes$`, s.modelKeepsCallingToolsTodoUnchanged)
	ctx.Step(`^a scripted model that ends every turn without finishing its todos$`, s.modelEndsTurnWithoutFinishingTodos)
	ctx.Step(`^a pending todo is seeded for the bounded-session scenarios$`, s.pendingTodoSeeded)
	ctx.Step(`^the engine is capped at a tool-loop duration of (\d+)ms$`, s.engineCappedAtToolLoopDuration)
	ctx.Step(`^each tool call stalls the engine for (\d+)ms$`, s.eachToolCallStallsEngine)
	ctx.Step(`^a token budget that sits at the compaction gate after a handful of tool batches$`, s.tokenBudgetAtCompactionGate)
	ctx.Step(`^the summariser only ever trims a sliver of the window$`, s.summariserTrimsSliver)
	ctx.Step(`^the bounded turn is streamed to completion$`, s.boundedTurnStreamedToCompletion)
	ctx.Step(`^six auto-continued turns are streamed on the same session$`, s.sixAutoContinuedTurnsStreamed)
	ctx.Step(`^the turn is stopped by the same-tool-pattern cap$`, s.turnStoppedBySameToolPatternCap)
	ctx.Step(`^the no-progress guard stops the turn within three continuations$`, s.noProgressGuardStopsWithinThree)
	ctx.Step(`^the whole turn finishes within twice its original duration cap$`, s.wholeTurnWithinTwiceDurationCap)
	ctx.Step(`^the mid-loop compactor fires at most once$`, s.midLoopCompactorFiresAtMostOnce)
	ctx.Step(`^the session is refused or flagged once its lifetime budget is spent$`, s.sessionRefusedOrFlaggedAfterLifetimeBudget)
	ctx.Step(`^a completion orchestrator is watching background tasks$`, s.completionOrchestratorWatching)
	ctx.Step(`^four background tasks complete one after another$`, s.fourBackgroundTasksComplete)
	ctx.Step(`^the re-prompt chain stops at three sends$`, s.rePromptChainStopsAtThree)
}

// reset rebuilds per-scenario state.
func (s *sessionBoundedSteps) reset(sessionID string) {
	s.sessionID = sessionID
	s.provider = &invariantScriptedProvider{name: "session-bounded-provider"}
	s.tools = []tool.Tool{&invariantEchoTool{name: "echoer", output: "ok"}}
	s.todoStore = nil
	dir, err := os.MkdirTemp("", "session-bounded-ctx-*")
	if err != nil {
		return
	}
	store, err := recall.NewFileContextStore(dir+"/ctx.json", "invariant-model")
	if err != nil {
		return
	}
	s.storeDir = dir
	s.store = store
	s.maxIter = 0
	s.maxDuration = 0
	s.maxSessionTurns = 0
	s.tokenCounter = nil
	s.summariser = nil
	s.compressionConfig = nil
	s.capturer = &invariantLogCapturer{}
	s.ran = false
	s.errorSeen = false
	s.stopReasons = nil
	s.turnElapsed = 0
	s.orchestrator = nil
	s.orchSender = nil
	s.orchBgMgr = nil
}

// teardown stops the orchestrator, closes the scenario's context store,
// and removes its temp dir.
func (s *sessionBoundedSteps) teardown() {
	if s.orchestrator != nil {
		func() {
			defer func() { _ = recover() }()
			s.orchestrator.Stop()
		}()
		s.orchestrator = nil
	}
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
func (s *sessionBoundedSteps) buildEngine() *engine.Engine {
	manifest := agent.Manifest{
		ID:   "session-bounded-agent",
		Name: "Session Bounded Agent",
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
		MaxToolLoopIterations: maxIter,
		MaxToolLoopDuration:   s.maxDuration,
		MaxSessionTurns:       s.maxSessionTurns,
		ToolOutputRetention:   -1,
	}
	if s.tokenCounter != nil {
		cfg.TokenCounter = s.tokenCounter
	}
	if s.summariser != nil {
		cfg.AutoCompactor = ctxstore.NewAutoCompactor(s.summariser)
	}
	if s.todoStore != nil {
		cfg.TodoStore = s.todoStore
	}
	if s.compressionConfig != nil {
		cfg.CompressionConfig = *s.compressionConfig
	}
	return engine.New(cfg)
}

// drainTurn streams one turn under the captured logger and a guard,
// recording terminal stop reasons, error chunks, and wall duration.
func (s *sessionBoundedSteps) drainTurn(eng *engine.Engine, prompt string, guard time.Duration) error {
	previous := slog.Default()
	slog.SetDefault(slog.New(s.capturer))
	defer slog.SetDefault(previous)

	streamCtx, cancel := context.WithCancel(context.WithValue(context.Background(), session.IDKey{}, s.sessionID))
	defer cancel()

	start := time.Now()
	chunks, err := eng.Stream(streamCtx, s.sessionID, prompt)
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
			if chunk.Error != nil {
				s.errorSeen = true
			}
		}
	}()

	select {
	case <-drained:
		s.ran = true
		s.turnElapsed = time.Since(start)
		return nil
	case <-time.After(guard):
		cancel()
		return fmt.Errorf("turn did not terminate within %s", guard)
	}
}

// modelNarratesIdenticalToolRequests scripts narration alongside the
// same tool call every round and arms a modest iteration backstop so
// narrating rounds still terminate for inspection.
func (s *sessionBoundedSteps) modelNarratesIdenticalToolRequests() error {
	s.provider.turns = []invariantTurn{{content: "Let me check that again.", toolName: "echoer"}}
	s.maxIter = 12
	return nil
}

// modelKeepsCallingToolsTodoUnchanged scripts narration plus a tool call
// every round; the todo store is seeded separately and never changes, so
// only the engine's guards can stop the turn.
func (s *sessionBoundedSteps) modelKeepsCallingToolsTodoUnchanged() error {
	s.provider.turns = []invariantTurn{{content: "Still working on it.", toolName: "echoer"}}
	s.maxIter = 3
	return nil
}

// modelEndsTurnWithoutFinishingTodos scripts a text-only answer that
// never addresses the pending todo, so every turn is auto-continued by
// the todo machinery until a guard stops it. The lifetime bound is
// armed tight (four turns) so the scenario's six streamed turns
// observably trip it.
func (s *sessionBoundedSteps) modelEndsTurnWithoutFinishingTodos() error {
	s.provider.turns = []invariantTurn{{content: "Done for now."}}
	s.maxSessionTurns = 4
	return nil
}

// pendingTodoSeeded arms the todo store with one pending item.
func (s *sessionBoundedSteps) pendingTodoSeeded() error {
	s.todoStore = todo.NewMemoryStore()
	return s.todoStore.Set(s.sessionID, []todo.Item{
		{Content: "finish the bounded-session work", Status: "pending", Priority: "high"},
	})
}

// engineCappedAtToolLoopDuration arms the wall/tool-time duration cap.
func (s *sessionBoundedSteps) engineCappedAtToolLoopDuration(ms int) error {
	s.maxDuration = time.Duration(ms) * time.Millisecond
	return nil
}

// eachToolCallStallsEngine swaps the echo tool for a sleeping tool so
// non-delegated tool time accumulates against the duration cap, and
// lifts the iteration backstop so the duration budget is the only
// intended trip.
func (s *sessionBoundedSteps) eachToolCallStallsEngine(ms int) error {
	s.tools = []tool.Tool{&budgetSleepTool{name: "echoer", delay: time.Duration(ms) * time.Millisecond}}
	s.maxIter = 50
	return nil
}

// tokenBudgetAtCompactionGate wires the deterministic token counter,
// bulky-output echo tool, and iteration ceiling the mid-loop compaction
// scenario needs so the live tool-loop window crosses the gate-proximity
// boundary after a handful of batches.
func (s *sessionBoundedSteps) tokenBudgetAtCompactionGate() error {
	s.tokenCounter = &engineStepTokenCounter{limit: 12000}
	s.tools = []tool.Tool{&invariantEchoTool{name: "echoer", output: strings.Repeat("gate-window-payload ", 90)}}
	s.maxIter = 40
	return nil
}

// summariserTrimsSliver arms the counting summariser and enables
// auto-compaction so mid-loop gate fires invoke the summariser.
func (s *sessionBoundedSteps) summariserTrimsSliver() error {
	s.summariser = &boundedSliverSummariser{}
	cfg := ctxstore.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = true
	cfg.AutoCompaction.Threshold = 0.75
	s.compressionConfig = &cfg
	return nil
}

// boundedTurnStreamedToCompletion runs the assembled turn under a
// generous guard.
func (s *sessionBoundedSteps) boundedTurnStreamedToCompletion() error {
	if s.store == nil {
		return fmt.Errorf("scenario store was not initialised")
	}
	return s.drainTurn(s.buildEngine(), "Go", 30*time.Second)
}

// sixAutoContinuedTurnsStreamed drives six sequential turns through one
// engine, store, and todo store so session-lifetime behaviour is
// observable across turns.
func (s *sessionBoundedSteps) sixAutoContinuedTurnsStreamed() error {
	if s.store == nil {
		return fmt.Errorf("scenario store was not initialised")
	}
	eng := s.buildEngine()
	for range 6 {
		if err := s.drainTurn(eng, "Go", 30*time.Second); err != nil {
			return err
		}
	}
	return nil
}

// turnStoppedBySameToolPatternCap asserts the same-tool-pattern detector
// terminated the turn.
func (s *sessionBoundedSteps) turnStoppedBySameToolPatternCap() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if !s.capturer.hasTrip("same_tool_pattern") {
		return fmt.Errorf("expected a same_tool_pattern cap trip, saw trips %v (stop reasons %v)",
			s.capturer.tripSnapshot(), s.stopReasons)
	}
	return nil
}

// noProgressGuardStopsWithinThree asserts the todo no-progress guard
// halted the turn within three continuation injections.
func (s *sessionBoundedSteps) noProgressGuardStopsWithinThree() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	injected := s.capturer.continuationInjectionCount()
	if injected > 3 {
		return fmt.Errorf("expected the no-progress guard to stop the turn within 3 continuations, saw %d continuation injections (cap trips %v)",
			injected, s.capturer.tripSnapshot())
	}
	return nil
}

// wholeTurnWithinTwiceDurationCap asserts the whole turn's wall duration
// stayed within twice the original duration cap, i.e. continuation
// injection did not reset the budget.
func (s *sessionBoundedSteps) wholeTurnWithinTwiceDurationCap() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if s.maxDuration <= 0 {
		return fmt.Errorf("no duration cap was armed")
	}
	bound := 2 * s.maxDuration
	if s.turnElapsed > bound {
		return fmt.Errorf("turn ran for %s, more than twice the original %s cap (cap trips %v, continuation injections %d)",
			s.turnElapsed, s.maxDuration, s.capturer.tripSnapshot(), s.capturer.continuationInjectionCount())
	}
	return nil
}

// midLoopCompactorFiresAtMostOnce asserts the summariser was invoked at
// most once: after a marginal trim the gate must not re-fire on
// subsequent batches.
func (s *sessionBoundedSteps) midLoopCompactorFiresAtMostOnce() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if s.summariser == nil {
		return fmt.Errorf("no summariser was armed")
	}
	if calls := s.summariser.callCount(); calls > 1 {
		return fmt.Errorf("expected the mid-loop compactor to fire at most once on a marginal trim, summariser ran %d times (cap trips %v)",
			calls, s.capturer.tripSnapshot())
	}
	return nil
}

// sessionRefusedOrFlaggedAfterLifetimeBudget asserts that across the six
// auto-continued turns something refused or flagged the session once a
// lifetime budget was spent.
func (s *sessionBoundedSteps) sessionRefusedOrFlaggedAfterLifetimeBudget() error {
	if !s.ran {
		return fmt.Errorf("no turns were streamed")
	}
	if s.errorSeen {
		return nil
	}
	for _, reason := range s.stopReasons {
		if reason != "" {
			return nil
		}
	}
	messages := 0
	if s.store != nil {
		messages = len(s.store.GetStoredMessages())
	}
	return fmt.Errorf("no session-lifetime bound tripped: 6 auto-continued turns were accepted with %d provider calls and %d persisted messages, every turn completed naturally",
		s.provider.callCount(), messages)
}

// completionOrchestratorWatching wires a completion orchestrator over a
// background-task manager with recording fakes.
func (s *sessionBoundedSteps) completionOrchestratorWatching() error {
	bus := eventbus.NewEventBus()
	bgMgr := engine.NewBackgroundTaskManager()
	bgMgr.SetEventBus(bus)
	s.orchBgMgr = bgMgr
	sender := &boundedOrchestratorSender{}
	s.orchSender = sender
	s.orchestrator = engine.NewCompletionOrchestrator(bgMgr, sender, bus, &boundedOrchestratorBroker{})
	s.orchestrator.Start()
	return nil
}

// fourBackgroundTasksComplete launches four instantly-completing
// background tasks for one session, each carrying a completion
// notification.
func (s *sessionBoundedSteps) fourBackgroundTasksComplete() error {
	if s.orchestrator == nil || s.orchBgMgr == nil {
		return fmt.Errorf("no completion orchestrator was wired")
	}
	const bgSessionID = "session-bounded-bg-session"
	taskCtx := context.WithValue(context.Background(), session.IDKey{}, bgSessionID)
	for _, suffix := range []string{"a", "b", "c", "d"} {
		taskID := "bounded-bg-" + suffix
		s.orchSender.addNotification(bgSessionID, streaming.CompletionNotificationEvent{
			TaskID:   taskID,
			Agent:    "session-bounded-agent",
			Duration: time.Second,
		})
		s.orchBgMgr.Launch(taskCtx, taskID, "session-bounded-agent", "bounded task", func(context.Context) (string, error) {
			return "done", nil
		})
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if task, ok := s.orchBgMgr.Get(taskID); ok && task.Status.Load() == "completed" {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

// rePromptChainStopsAtThree asserts the orchestrator's re-prompt depth
// limit capped the send chain at three.
func (s *sessionBoundedSteps) rePromptChainStopsAtThree() error {
	if s.orchSender == nil {
		return fmt.Errorf("no completion orchestrator was wired")
	}
	deadline := time.Now().Add(3 * time.Second)
	for s.orchSender.sendCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	sends := s.orchSender.sendCount()
	if sends < 1 {
		return fmt.Errorf("expected at least one re-prompt send, saw none")
	}
	if sends > 3 {
		return fmt.Errorf("expected the re-prompt chain to stop at three sends, saw %d", sends)
	}
	return nil
}
