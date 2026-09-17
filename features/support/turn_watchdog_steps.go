//go:build e2e

package support

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// turnWatchdogSteps holds per-scenario state for the turn watchdog
// feature. Each scenario wires a scripted provider plus mock tools into
// a fresh engine whose tool-time cap and desired watchdog window are
// recorded here, then asserts on the terminal stop reasons and the
// engine's captured log records.
type turnWatchdogSteps struct {
	watchdog    time.Duration
	cap         time.Duration
	provider    *watchdogScriptedProvider
	tools       []tool.Tool
	capturer    *watchdogLogCapturer
	ran         bool
	stalled     bool
	mu          sync.Mutex
	stopReasons []string
}

// watchdogTurn describes one scripted provider turn: assistant text and
// an optional tool call.
type watchdogTurn struct {
	content  string
	toolName string
}

// watchdogScriptedProvider replays a script of tool-carrying turns and
// records how many times the engine called it. Once the call index
// reaches blockFrom, Stream parks on the context and never opens the
// next stream, modelling a provider that stalls between rounds.
// Tool-call arguments vary by call index so the identical-batch
// fingerprint detector never fires.
type watchdogScriptedProvider struct {
	name      string
	turns     []watchdogTurn
	blockFrom int

	mu    sync.Mutex
	calls int
}

// Name identifies the provider to the engine.
func (p *watchdogScriptedProvider) Name() string { return p.name }

// Stream emits the scripted turn, varying tool-call arguments per call, or parks forever once the stall index is reached.
func (p *watchdogScriptedProvider) Stream(ctx context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()

	if idx >= p.blockFrom {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	turn := watchdogTurn{content: "All done."}
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
func (p *watchdogScriptedProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed satisfies the provider interface with a fixed vector.
func (p *watchdogScriptedProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

// Models satisfies the provider interface with no models.
func (p *watchdogScriptedProvider) Models() ([]provider.Model, error) { return nil, nil }

// watchdogSleepTool sleeps for a fixed delay per execution, modelling a
// slow tool.
type watchdogSleepTool struct {
	name  string
	delay time.Duration
}

// Name identifies the tool to the engine.
func (t *watchdogSleepTool) Name() string { return t.name }

// Description satisfies the tool interface.
func (t *watchdogSleepTool) Description() string { return "sleeps per call" }

// Execute sleeps for the configured delay.
func (t *watchdogSleepTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	time.Sleep(t.delay)
	return tool.Result{Output: "slept"}, nil
}

// Schema satisfies the tool interface with an empty schema.
func (t *watchdogSleepTool) Schema() tool.Schema { return tool.Schema{} }

// watchdogLogCapturer is an slog handler that records every message the
// engine emits plus the trip reason of each "engine tool loop capped"
// warning.
type watchdogLogCapturer struct {
	mu       sync.Mutex
	messages []string
	trips    []string
}

// Enabled reports all levels as capturable.
func (c *watchdogLogCapturer) Enabled(context.Context, slog.Level) bool { return true }

// Handle records the message text and, for tool-loop cap warnings, the trip attribute.
func (c *watchdogLogCapturer) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, r.Message)
	if r.Message != "engine tool loop capped" {
		return nil
	}
	r.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "trip" {
			c.trips = append(c.trips, attr.Value.String())
		}
		return true
	})
	return nil
}

// WithAttrs satisfies the slog.Handler interface.
func (c *watchdogLogCapturer) WithAttrs([]slog.Attr) slog.Handler { return c }

// WithGroup satisfies the slog.Handler interface.
func (c *watchdogLogCapturer) WithGroup(string) slog.Handler { return c }

// hasMessage reports whether any captured record contains the fragment.
func (c *watchdogLogCapturer) hasMessage(fragment string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, msg := range c.messages {
		if strings.Contains(msg, fragment) {
			return true
		}
	}
	return false
}

// hasTrip reports whether the given trip reason was captured.
func (c *watchdogLogCapturer) hasTrip(reason string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, trip := range c.trips {
		if trip == reason {
			return true
		}
	}
	return false
}

// RegisterTurnWatchdogSteps wires the turn watchdog feature steps onto
// the godog scenario context.
func RegisterTurnWatchdogSteps(ctx *godog.ScenarioContext) {
	s := &turnWatchdogSteps{}
	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.watchdog = 0
		s.cap = 0
		s.provider = &watchdogScriptedProvider{name: "turn-watchdog-provider", blockFrom: 1 << 30}
		s.tools = nil
		s.capturer = &watchdogLogCapturer{}
		s.ran = false
		s.stalled = false
		s.mu.Lock()
		s.stopReasons = nil
		s.mu.Unlock()
		return bddCtx, nil
	})
	ctx.Step(`^an engine with a turn watchdog of (\d+)(ms|s)$`, s.engineWithTurnWatchdog)
	ctx.Step(`^a provider that completes one tool round then never opens the next stream$`, s.providerCompletesOneRoundThenStalls)
	ctx.Step(`^a tool that sleeps (\d+)ms per call without ever finishing the task$`, s.toolSleepsPerCallWithoutFinishing)
	ctx.Step(`^a tool that sleeps (\d+)ms once and then completes the task$`, s.toolSleepsOnceThenCompletes)
	ctx.Step(`^the tool batch completes and the loop stalls$`, s.toolBatchCompletesAndLoopStalls)
	ctx.Step(`^the cumulative tool execution time exceeds a (\d+)ms cap$`, s.cumulativeToolExecutionTimeExceedsCapMs)
	ctx.Step(`^the turn runs$`, s.theTurnRuns)
	ctx.Step(`^the turn ends with StopReason "([^"]+)"$`, s.turnEndsWithStopReason)
	ctx.Step(`^the turn completes naturally$`, s.turnCompletesNaturally)
	ctx.Step(`^the tool loop trips the cap with reason "([^"]+)"$`, s.toolLoopTripsCapWithReason)
	ctx.Step(`^the log (contains|does not contain) "([^"]+)"$`, s.logContainment)
}

// engineWithTurnWatchdog records the watchdog window the engine must be
// configured with once the production knob exists.
func (s *turnWatchdogSteps) engineWithTurnWatchdog(amount int, unit string) error {
	d := time.Duration(amount) * time.Millisecond
	if unit == "s" {
		d = time.Duration(amount) * time.Second
	}
	s.watchdog = d
	return nil
}

// providerCompletesOneRoundThenStalls scripts a single quick tool round
// and makes every later Stream call park without opening.
func (s *turnWatchdogSteps) providerCompletesOneRoundThenStalls() error {
	quick := &watchdogSleepTool{name: "quick", delay: 0}
	s.tools = []tool.Tool{quick}
	s.provider.turns = []watchdogTurn{{toolName: "quick"}}
	s.provider.blockFrom = 1
	return nil
}

// toolSleepsPerCallWithoutFinishing registers a sleeping tool and
// scripts the provider to re-request it on every continuation.
func (s *turnWatchdogSteps) toolSleepsPerCallWithoutFinishing(delayMs int) error {
	slowpoke := &watchdogSleepTool{name: "slowpoke", delay: time.Duration(delayMs) * time.Millisecond}
	s.tools = []tool.Tool{slowpoke}
	s.provider.turns = []watchdogTurn{
		{content: "working", toolName: "slowpoke"},
	}
	return nil
}

// toolSleepsOnceThenCompletes registers a sleeping tool the provider
// calls exactly once before answering with a final text turn, so the
// scenario exercises one long tool execution inside an otherwise
// healthy turn.
func (s *turnWatchdogSteps) toolSleepsOnceThenCompletes(delayMs int) error {
	sleepy := &watchdogSleepTool{name: "sleepy", delay: time.Duration(delayMs) * time.Millisecond}
	s.tools = []tool.Tool{sleepy}
	s.provider.turns = []watchdogTurn{
		{toolName: "sleepy"},
		{content: "All done."},
	}
	return nil
}

// theTurnRuns streams the assembled turn under a guard that leaves a
// future watchdog ample room to fire first.
func (s *turnWatchdogSteps) theTurnRuns() error {
	return s.runTurn("turn-watchdog-session", "Go", 8*time.Second)
}

// toolBatchCompletesAndLoopStalls runs the stalling turn under a guard
// that leaves a future watchdog ample room to fire first.
func (s *turnWatchdogSteps) toolBatchCompletesAndLoopStalls() error {
	return s.runTurn("turn-watchdog-session", "Go", 2*time.Second)
}

// cumulativeToolExecutionTimeExceedsCapMs applies the quoted tool-time
// cap and runs the never-finishing turn under a guard.
func (s *turnWatchdogSteps) cumulativeToolExecutionTimeExceedsCapMs(capMs int) error {
	s.cap = time.Duration(capMs) * time.Millisecond
	return s.runTurn("turn-watchdog-session", "Go", 8*time.Second)
}

// turnEndsWithStopReason asserts the quoted session stop-reason constant
// named the terminal chunk. The quoted literal names the Go constant, so
// only constants this feature pins are accepted.
func (s *turnWatchdogSteps) turnEndsWithStopReason(reason string) error {
	if s.stalled {
		return fmt.Errorf("turn stalled without terminating; the engine turn watchdog did not fire")
	}
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if reason != "StopReasonToolLoopExceeded" {
		return fmt.Errorf("unsupported stop-reason constant: %s", reason)
	}
	s.mu.Lock()
	observed := append([]string(nil), s.stopReasons...)
	s.mu.Unlock()
	for _, got := range observed {
		if got == session.StopReasonToolLoopExceeded {
			return nil
		}
	}
	return fmt.Errorf("expected terminal stop reason %s, got %v", reason, observed)
}

// turnCompletesNaturally asserts the streamed turn terminated on its
// own terms: it ran inside the guard and no terminal chunk carried the
// tool-loop-exceeded sentinel.
func (s *turnWatchdogSteps) turnCompletesNaturally() error {
	if s.stalled {
		return fmt.Errorf("turn stalled without terminating")
	}
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	s.mu.Lock()
	observed := append([]string(nil), s.stopReasons...)
	s.mu.Unlock()
	for _, got := range observed {
		if got == session.StopReasonToolLoopExceeded {
			return fmt.Errorf("expected a natural completion, saw terminal stop reason %v", observed)
		}
	}
	return nil
}

// toolLoopTripsCapWithReason asserts the tool-loop cap warning carried
// the quoted trip reason and the turn terminated with the
// tool-loop-exceeded stop reason.
func (s *turnWatchdogSteps) toolLoopTripsCapWithReason(reason string) error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if !s.capturer.hasTrip(reason) {
		return fmt.Errorf("expected the loop to be capped with reason %s", reason)
	}
	s.mu.Lock()
	observed := append([]string(nil), s.stopReasons...)
	s.mu.Unlock()
	for _, got := range observed {
		if got == session.StopReasonToolLoopExceeded {
			return nil
		}
	}
	return fmt.Errorf("expected a terminal tool_loop_exceeded stop reason")
}

// logContainment asserts the quoted fragment was or was not captured
// from the engine logs.
func (s *turnWatchdogSteps) logContainment(mode, fragment string) error {
	contains := s.capturer.hasMessage(fragment)
	if mode == "contains" && !contains {
		return fmt.Errorf("expected the log to contain %q", fragment)
	}
	if mode == "does not contain" && contains {
		return fmt.Errorf("expected the log not to contain %q", fragment)
	}
	return nil
}

// runTurn streams one turn under the captured logger, bounding the wait
// by the guard. A turn that outlives the guard is cancelled and
// recorded as stalled so the assertions can pin the missing watchdog.
func (s *turnWatchdogSteps) runTurn(sessionID, prompt string, guard time.Duration) error {
	manifest := agent.Manifest{
		ID:   "turn-watchdog-agent",
		Name: "Turn Watchdog Agent",
		Capabilities: agent.Capabilities{
			Tools: []string{"quick", "slowpoke", "sleepy"},
		},
	}
	eng := engine.New(engine.Config{
		ChatProvider:          s.provider,
		Manifest:              manifest,
		Tools:                 s.tools,
		MaxToolLoopDuration:   s.cap,
		ToolLoopWatchdog:      s.watchdog,
		MaxToolLoopIterations: 10000,
	})

	previous := slog.Default()
	slog.SetDefault(slog.New(s.capturer))
	defer slog.SetDefault(previous)

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
				s.mu.Lock()
				s.stopReasons = append(s.stopReasons, chunk.StopReason)
				s.mu.Unlock()
			}
		}
	}()

	select {
	case <-drained:
		s.ran = true
		return nil
	case <-time.After(guard):
		cancel()
		s.stalled = true
		select {
		case <-drained:
		case <-time.After(2 * time.Second):
		}
		return nil
	}
}
