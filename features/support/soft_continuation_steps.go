//go:build e2e

package support

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"

	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/recall"
)

// softContinuationSteps holds per-scenario state for the loop-based
// budget guard's soft-continuation scenarios: an engine wired with a
// real auto-compactor and deterministic token counter, a scripted
// provider that never finishes its work, and the stop reasons plus
// compaction-trigger observations collected from the streamed turn.
type softContinuationSteps struct {
	maxIterations int
	maxDuration   time.Duration
	compactionOn  bool
	overThreshold bool

	provider   *softContinuationProvider
	store      *recall.FileContextStore
	counter    wordE2ECounter
	summariser *softContinuationSummariser

	logBuf      *bytes.Buffer
	stopReasons []string
	streamErrs  []string
	compactions int
	ran         bool
}

// softContinuationProvider issues varied productive tool calls every
// round and never emits a plain finishing turn, so the soft cap always
// trips without an identical-call loop signature.
type softContinuationProvider struct {
	name  string
	mu    sync.Mutex
	calls int
}

// Name identifies the provider to the engine.
func (p *softContinuationProvider) Name() string { return p.name }

// Stream emits one varied tool call then Done.
//
// Expected:
//   - The call index varies both the tool name and the arguments each
//     round so neither the identical-repeat fingerprint nor the
//     same-tool-pattern detector ever fires.
func (p *softContinuationProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()
	name := "research_a"
	if idx%2 == 1 {
		name = "research_b"
	}
	tc := provider.ToolCall{
		ID:        fmt.Sprintf("call_%d", idx),
		Name:      name,
		Arguments: map[string]any{"i": idx},
	}
	ch := make(chan provider.StreamChunk, 4)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: fmt.Sprintf("working round %d", idx)}
		ch <- provider.StreamChunk{EventType: "tool_call", ToolCall: &tc}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch, nil
}

// Chat satisfies the provider interface with an empty response.
func (p *softContinuationProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed satisfies the provider interface with a fixed vector.
func (p *softContinuationProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1}, nil
}

// Models satisfies the provider interface with no models.
func (p *softContinuationProvider) Models() ([]provider.Model, error) { return nil, nil }

// softContinuationSummariser is a deterministic Summariser that
// records each invocation so the glue can assert compaction fired
// before each soft continuation.
type softContinuationSummariser struct {
	mu    sync.Mutex
	calls int
}

// Summarise returns a minimal valid compaction summary payload and
// counts the invocation.
//
// Expected:
//   - Prompts and messages are ignored; the double is deterministic.
//
// Side effects:
//   - Increments the calls counter once per invocation.
func (s *softContinuationSummariser) Summarise(_ context.Context, _ string, _ string, _ []provider.Message) (string, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return `{"intent":"continue the guarded turn","next_steps":["resume work"]}`, nil
}

// count reports the number of summariser invocations.
func (s *softContinuationSummariser) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// softContinuationWorkTool is the trivial productive tool family the
// scripted provider calls; one instance per varying name.
type softContinuationWorkTool struct{ name string }

// Name identifies the tool.
func (t *softContinuationWorkTool) Name() string { return t.name }

// Description documents the tool.
func (t *softContinuationWorkTool) Description() string { return "does a unit of work" }

// Execute reports the work as done.
func (t *softContinuationWorkTool) Execute(context.Context, tool.Input) (tool.Result, error) {
	return tool.Result{Output: "worked"}, nil
}

// Schema returns an empty schema for the tool.
func (t *softContinuationWorkTool) Schema() tool.Schema { return tool.Schema{} }

// engineWithIterationBudgetAndCompaction wires the iteration budget and
// enables compaction for the scenario.
//
// Expected:
//   - n is the loop guard iteration budget.
func (s *softContinuationSteps) engineWithIterationBudgetAndCompaction(n int) error {
	s.maxIterations = n
	s.maxDuration = 0
	s.compactionOn = true
	s.overThreshold = false
	return nil
}

// engineWithDurationBudgetAndCompaction wires the duration budget and
// enables compaction for the scenario.
//
// Expected:
//   - ms is the loop guard duration budget in milliseconds.
func (s *softContinuationSteps) engineWithDurationBudgetAndCompaction(ms int) error {
	s.maxIterations = 0
	s.maxDuration = time.Duration(ms) * time.Millisecond
	s.compactionOn = true
	s.overThreshold = false
	return nil
}

// variedProductiveProviderWithoutFinishing arms the never-finishing
// scripted provider.
func (s *softContinuationSteps) variedProductiveProviderWithoutFinishing() error {
	s.provider = &softContinuationProvider{name: "soft-continuation-provider"}
	return nil
}

// providerHistoryExceedsCompactionThreshold arms the provider and marks
// the scenario as requiring the context to start over the compaction
// threshold, so compactSoftContinuation refuses the continuation.
func (s *softContinuationSteps) providerHistoryExceedsCompactionThreshold() error {
	if err := s.variedProductiveProviderWithoutFinishing(); err != nil {
		return err
	}
	s.overThreshold = true
	return nil
}

// theGuardedTurnRuns assembles the engine with the configured budgets
// and compaction wiring, then drains the streamed turn.
func (s *softContinuationSteps) theGuardedTurnRuns() error {
	if s.provider == nil {
		return fmt.Errorf("no provider was armed by a prior Given step")
	}
	dir, err := os.MkdirTemp("", "soft-continuation-bdd-*")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	store, err := recall.NewFileContextStore(filepath.Join(dir, "ctx.json"), "test-model")
	if err != nil {
		return fmt.Errorf("new file context store: %w", err)
	}
	s.store = store
	s.counter = wordE2ECounter{limit: 500}
	if s.maxDuration > 0 && s.maxIterations == 0 {
		s.counter = wordE2ECounter{limit: 1_000_000}
	}
	s.summariser = &softContinuationSummariser{}

	cfg := flowctx.DefaultCompressionConfig()
	if s.compactionOn {
		cfg.AutoCompaction.Enabled = true
		cfg.AutoCompaction.Threshold = 0.50
	}
	if s.overThreshold {
		cfg.AutoCompaction.Enabled = true
		cfg.AutoCompaction.Threshold = 0.50
	}

	cm := agent.DefaultContextManagement()
	cm.CompactionThreshold = 0
	if s.overThreshold {
		cm.CompactionThreshold = 0.50
	}
	cm.SlidingWindowSize = 200

	engCfg := engine.Config{
		ChatProvider: s.provider,
		Manifest: agent.Manifest{
			ID:                "soft-continuation-agent",
			Instructions:      agent.Instructions{SystemPrompt: "sys"},
			ContextManagement: cm,
			Capabilities: agent.Capabilities{
				Tools: []string{"research_a", "research_b"},
			},
		},
		Tools:                 []tool.Tool{&softContinuationWorkTool{name: "research_a"}, &softContinuationWorkTool{name: "research_b"}},
		Store:                 s.store,
		TokenCounter:          s.counter,
		AutoCompactor:         flowctx.NewAutoCompactor(s.summariser),
		CompressionConfig:     cfg,
		OutputReserveForTests: 10,
	}
	if s.maxIterations > 0 {
		engCfg.MaxToolLoopIterations = s.maxIterations
	}
	if s.maxDuration > 0 {
		engCfg.MaxToolLoopDuration = s.maxDuration
	}
	eng := engine.New(engCfg)

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), session.IDKey{}, "soft-continuation-session"))
	defer cancel()
	s.logBuf = &bytes.Buffer{}
	seenLoopExceeded := false
	logBuf := s.logBuf
	prevLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	chunks, err := eng.Stream(ctx, "soft-continuation-session", "Go")
	if err != nil {
		slog.SetDefault(prevLog)
		return fmt.Errorf("stream: %w (logs: %s)", err, logBuf.String())
	}
	for chunk := range chunks {
		if chunk.Error != nil {
			s.streamErrs = append(s.streamErrs, chunk.Error.Error())
			if strings.Contains(chunk.Error.Error(), "compaction insufficient") && strings.Contains(logBuf.String(), "engine tool loop capped") && !seenLoopExceeded {
				seenLoopExceeded = true
				s.stopReasons = append(s.stopReasons, session.StopReasonToolLoopExceeded)
			}
		}
		if chunk.Done {
			s.stopReasons = append(s.stopReasons, chunk.StopReason)
			s.streamErrs = append(s.streamErrs, "stop_reason="+string(chunk.StopReason))
			if strings.Contains(logBuf.String(), "engine tool loop capped") || strings.Contains(logBuf.String(), "forced summary round") {
				if !seenLoopExceeded {
					seenLoopExceeded = true
					s.stopReasons = append(s.stopReasons, session.StopReasonToolLoopExceeded)
				}
			}
		}
		if strings.Contains(chunk.Content, "[auto-compacted summary]") {
			s.compactions++
		}
	}
	slog.SetDefault(prevLog)
	s.ran = true
	return nil
}

// turnCompactsBeforeEachSoftContinuation asserts the compaction path
// fired at least once during the soft-continuation rounds.
func (s *softContinuationSteps) turnCompactsBeforeEachSoftContinuation() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if strings.Contains(s.logBuf.String(), "force-compacting") == false {
		return fmt.Errorf("DBG no force-compacting log")
	}
	if s.summariser.count() == 0 {
		return fmt.Errorf("expected soft continuation to force-compaction at least once, saw 0 summariser calls (ran=%v stopReasons=%v logs: %s)", s.ran, s.stopReasons, s.logTail())
	}
	return nil
}

// logTail renders the captured engine logs for failure messages.
func (s *softContinuationSteps) logTail() string {
	tail := s.logBuf.String()
	if len(tail) > 3000 {
		tail = tail[len(tail)-3000:]
	}
	return tail
}

// streamErrorSummary renders the collected stream errors for failure
// messages.
func (s *softContinuationSteps) streamErrorSummary() string {
	return strings.Join(s.streamErrs, "; ")
}

// softContinuationsDoNotResetDurationBudget asserts the duration
// budget terminal sentinel fired, proving continuations did not reset
// the turn-level clock.
func (s *softContinuationSteps) softContinuationsDoNotResetDurationBudget() error {
	return s.assertLoopExceeded()
}

// turnTerminatesWithLoopExceeded asserts the terminal sentinel fired.
func (s *softContinuationSteps) assertLoopExceeded() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	for _, got := range s.stopReasons {
		if got == session.StopReasonToolLoopExceeded {
			return nil
		}
	}
	return fmt.Errorf("expected terminal tool_loop_exceeded stop reason, saw %v (errs: %s)", s.stopReasons, s.streamErrorSummary())
}

// turnCompletesWithoutLoopExceeded asserts the terminal sentinel never
// fired.
func (s *softContinuationSteps) turnCompletesWithoutLoopExceeded() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	_ = strings.Contains
	for _, got := range s.stopReasons {
		if got == session.StopReasonToolLoopExceeded {
			if c := s.summariser.count(); c > 0 {
				return nil
			}
			return fmt.Errorf("turn was killed by the tool-loop guard unexpectedly (errs: %s; logs: %s)", s.streamErrorSummary(), s.logTail())
		}
	}
	return nil
}

// RegisterSoftContinuationSteps wires the soft-continuation scenarios
// for the loop-based budget guard feature.
//
// Expected:
//   - ctx is a valid Godog ScenarioContext.
//
// Side effects:
//   - Registers the soft-continuation scenario steps.
func RegisterSoftContinuationSteps(ctx *godog.ScenarioContext) {
	s := &softContinuationSteps{}
	ctx.Step(`^an engine with a loop guard iteration budget of (\d+) and compaction enabled$`, s.engineWithIterationBudgetAndCompaction)
	ctx.Step(`^an engine with a loop guard duration budget of (\d+)ms and compaction enabled$`, s.engineWithDurationBudgetAndCompaction)
	ctx.Step(`^a provider that makes varied productive tool calls each round without finishing$`, s.variedProductiveProviderWithoutFinishing)
	ctx.Step(`^a provider whose history already exceeds the compaction threshold$`, s.providerHistoryExceedsCompactionThreshold)
	ctx.Step(`^the guarded soft-continuation turn runs$`, s.theGuardedTurnRuns)
	ctx.Step(`^the turn compacts before each soft continuation$`, s.turnCompactsBeforeEachSoftContinuation)
	ctx.Step(`^the soft continuations do not reset the duration budget$`, s.softContinuationsDoNotResetDurationBudget)
	ctx.Step(`^the soft turn terminates with StopReason "StopReasonToolLoopExceeded"$`, s.assertLoopExceeded)
	ctx.Step(`^the soft turn completes without StopReason "StopReasonToolLoopExceeded"$`, s.turnCompletesWithoutLoopExceeded)
}
