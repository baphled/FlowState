//go:build e2e

package support

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// toolLoopBudgetSteps holds per-scenario state for the tool-loop budget
// features. Each scenario wires a scripted provider plus mock tools into a
// fresh engine configured with a tiny duration cap, then asserts which
// backstop (if any) terminated the turn by capturing the engine's
// "engine tool loop capped" log records.
type toolLoopBudgetSteps struct {
	cap           time.Duration
	maxIterations int
	provider      *toolBudgetScriptedProvider
	tools         []tool.Tool
	capturer      *capTripCapturer
	stopReasons   []string
	ran           bool
	bgCancel      context.CancelFunc
}

// toolBudgetTurn describes one scripted provider turn: assistant text and an
// optional tool call. Script exhaustion repeats the final turn.
type toolBudgetTurn struct {
	content  string
	toolName string
}

// toolBudgetScriptedProvider replays a script of tool-carrying turns and
// records how many times the engine called it. Tool-call arguments vary by
// call index so the identical-batch fingerprint detector never fires.
type toolBudgetScriptedProvider struct {
	name  string
	turns []toolBudgetTurn

	mu    sync.Mutex
	calls int
}

// Name identifies the provider to the engine.
func (p *toolBudgetScriptedProvider) Name() string { return p.name }

// Stream emits the scripted turn, varying tool-call arguments per call.
func (p *toolBudgetScriptedProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()

	turn := toolBudgetTurn{content: "All done."}
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
func (p *toolBudgetScriptedProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed satisfies the provider interface with a fixed vector.
func (p *toolBudgetScriptedProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

// Models satisfies the provider interface with no models.
func (p *toolBudgetScriptedProvider) Models() ([]provider.Model, error) { return nil, nil }

// callCount reports how many times the engine invoked the provider.
func (p *toolBudgetScriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// budgetSleepTool sleeps for a fixed delay per execution, modelling a slow
// non-delegated tool.
type budgetSleepTool struct {
	name  string
	delay time.Duration
}

// Name identifies the tool to the engine.
func (t *budgetSleepTool) Name() string { return t.name }

// Description satisfies the tool interface.
func (t *budgetSleepTool) Description() string { return "sleeps per call" }

// Execute sleeps for the configured delay.
func (t *budgetSleepTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	time.Sleep(t.delay)
	return tool.Result{Output: "slept"}, nil
}

// Schema satisfies the tool interface with an empty schema.
func (t *budgetSleepTool) Schema() tool.Schema { return tool.Schema{} }

// budgetDelegationTool models DelegateTool's timeout shape: it implements
// tool.TimeoutOverrider returning 0, so the engine injects no per-tool
// deadline and the child execution runs under the parent context alone.
type budgetDelegationTool struct {
	budgetSleepTool
}

// Timeout opts out of the engine's per-tool deadline, mirroring DelegateTool.
func (t *budgetDelegationTool) Timeout() time.Duration { return 0 }

// capTripCapturer is an slog handler that records the trip reason and
// duration attrs of every "engine tool loop capped" warning the engine
// emits, and counts how many background-task continuation messages it
// injects.
type capTripCapturer struct {
	mu              sync.Mutex
	trips           []string
	durations       map[string][]time.Duration
	bgContinuations int
}

// Enabled reports all levels as capturable.
func (c *capTripCapturer) Enabled(context.Context, slog.Level) bool { return true }

// Handle records the trip and duration attrs of tool-loop cap warnings
// and counts background-task continuation injections.
func (c *capTripCapturer) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch r.Message {
	case "engine tool loop capped":
		r.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "trip" {
				c.trips = append(c.trips, attr.Value.String())
			}
			if attr.Value.Kind() == slog.KindDuration {
				if c.durations == nil {
					c.durations = make(map[string][]time.Duration)
				}
				c.durations[attr.Key] = append(c.durations[attr.Key], attr.Value.Duration())
			}
			return true
		})
	case "background tasks still active, continuing":
		c.bgContinuations++
	}
	return nil
}

// WithAttrs satisfies the slog.Handler interface.
func (c *capTripCapturer) WithAttrs([]slog.Attr) slog.Handler { return c }

// WithGroup satisfies the slog.Handler interface.
func (c *capTripCapturer) WithGroup(string) slog.Handler { return c }

// hasTrip reports whether the given trip reason was captured.
func (c *capTripCapturer) hasTrip(reason string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, trip := range c.trips {
		if trip == reason {
			return true
		}
	}
	return false
}

// hasDurationField reports whether any cap warning carried the named
// duration attr.
func (c *capTripCapturer) hasDurationField(field string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.durations[field]) > 0
}

// backgroundContinuationCount reports how many background-task
// continuation injections were captured.
func (c *capTripCapturer) backgroundContinuationCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bgContinuations
}

// observedStopReason reports whether any terminal chunk carried the reason.
func (s *toolLoopBudgetSteps) observedStopReason(reason string) bool {
	for _, got := range s.stopReasons {
		if got == reason {
			return true
		}
	}
	return false
}

// runTurn streams one turn under the captured logger and a watchdog, then
// restores the previous default logger.
func (s *toolLoopBudgetSteps) runTurn(sessionID, prompt string, watchdog time.Duration) error {
	manifest := agent.Manifest{
		ID:   "tool-budget-agent",
		Name: "Tool Budget Agent",
		Capabilities: agent.Capabilities{
			Tools: []string{"slowpoke", "delegate", "spinner"},
		},
	}
	maxIterations := s.maxIterations
	if maxIterations == 0 {
		maxIterations = 10000
	}
	eng := engine.New(engine.Config{
		ChatProvider:          s.provider,
		Manifest:              manifest,
		Tools:                 s.tools,
		MaxToolLoopDuration:   s.cap,
		MaxToolLoopIterations: maxIterations,
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
				s.stopReasons = append(s.stopReasons, chunk.StopReason)
			}
		}
	}()

	select {
	case <-drained:
		s.ran = true
		return nil
	case <-time.After(watchdog):
		cancel()
		return fmt.Errorf("tool loop was not capped within %s", watchdog)
	}
}

// RegisterToolLoopBudgetSteps wires the tool-loop budget feature steps onto
// the godog scenario context.
func RegisterToolLoopBudgetSteps(ctx *godog.ScenarioContext) {
	s := &toolLoopBudgetSteps{}
	ctx.Step(`^a tool completes five successful calls with different arguments$`, func() error {
		s.tools = []tool.Tool{&budgetSleepTool{name: "spinner"}}
		s.provider.turns = []toolBudgetTurn{
			{toolName: "spinner"},
			{toolName: "spinner"},
			{toolName: "spinner"},
			{toolName: "spinner"},
			{toolName: "spinner"},
			{content: "All done."},
		}
		return nil
	})
	ctx.Step(`^the successful same-tool sequence completes$`, func() error {
		return s.runTurn("successful-tool-budget-session", "Go", 8*time.Second)
	})
	ctx.Step(`^all five tool calls reach the provider's final response$`, func() error {
		if s.provider.callCount() != 6 || s.capturer.hasTrip("same_tool_pattern") || s.observedStopReason(session.StopReasonToolLoopExceeded) {
			return fmt.Errorf("successful same-tool work was capped after %d provider calls", s.provider.callCount())
		}
		return nil
	})
	ctx.Step(`^an engine with a tool loop duration cap of 200ms$`, s.engineWithToolLoopDurationCap)
	ctx.Step(`^a tool that sleeps 50ms per call and never finishes the task$`, s.toolSleepsPerCallNeverFinishing)
	ctx.Step(`^a delegation tool whose child engine runs for (\d+)ms$`, s.delegationToolRunningChildEngine)
	ctx.Step(`^the cumulative tool execution time exceeds the cap$`, s.cumulativeToolExecutionTimeExceedsCap)
	ctx.Step(`^the tool loop is capped with reason "([^"]+)"$`, s.toolLoopCappedWithReason)
	ctx.Step(`^the cap warning includes a "([^"]+)" duration field$`, s.capWarningIncludesDurationField)
	ctx.Step(`^the delegation completes$`, s.delegationCompletes)
	ctx.Step(`^the parent tool loop does not trip the tool-time backstop$`, s.parentLoopDoesNotTripToolTimeBackstop)
	ctx.Step(`^a session whose background tasks never complete$`, s.sessionWhoseBackgroundTasksNeverComplete)
	ctx.Step(`^the tool loop requests more than 20 background-task continuations$`, s.toolLoopRequestsMoreThan20BackgroundTaskContinuations)
	ctx.Step(`^the turn terminates with StopReason "StopReasonToolLoopExceeded"$`, s.turnTerminatesWithToolLoopExceededStopReason)
	ctx.Step(`^no further continuation is injected$`, s.noFurtherContinuationIsInjected)
}

// engineWithToolLoopDurationCap resets scenario state and sets the duration
// cap every budget guard shares.
func (s *toolLoopBudgetSteps) engineWithToolLoopDurationCap() error {
	s.cap = 200 * time.Millisecond
	s.maxIterations = 0
	s.provider = &toolBudgetScriptedProvider{name: "tool-budget-provider"}
	s.tools = nil
	s.capturer = &capTripCapturer{}
	s.stopReasons = nil
	s.ran = false
	s.bgCancel = nil
	return nil
}

// toolSleepsPerCallNeverFinishing registers a plain sleeping tool and scripts
// the provider to re-request it on every continuation.
func (s *toolLoopBudgetSteps) toolSleepsPerCallNeverFinishing() error {
	slowpoke := &budgetSleepTool{name: "slowpoke", delay: 50 * time.Millisecond}
	s.tools = []tool.Tool{slowpoke}
	s.provider.turns = []toolBudgetTurn{
		{content: "working", toolName: "slowpoke"},
	}
	return nil
}

// delegationToolRunningChildEngine registers a TimeoutOverrider-zero tool modelling a child engine run for the scripted duration.
func (s *toolLoopBudgetSteps) delegationToolRunningChildEngine(durationMs int) error {
	delegate := &budgetDelegationTool{budgetSleepTool{name: "delegate", delay: time.Duration(durationMs) * time.Millisecond}}
	s.tools = []tool.Tool{delegate}
	s.provider.turns = []toolBudgetTurn{
		{toolName: "delegate"},
		{content: "All done."},
	}
	return nil
}

// cumulativeToolExecutionTimeExceedsCap runs the never-finishing turn under
// a watchdog.
func (s *toolLoopBudgetSteps) cumulativeToolExecutionTimeExceedsCap() error {
	return s.runTurn("tool-budget-session", "Go", 8*time.Second)
}

// delegationCompletes runs the delegated turn to its natural completion.
func (s *toolLoopBudgetSteps) delegationCompletes() error {
	return s.runTurn("tool-budget-session", "Delegate", 10*time.Second)
}

// toolLoopCappedWithReason asserts the turn was terminated by the
// quoted cap trip reason.
func (s *toolLoopBudgetSteps) toolLoopCappedWithReason(reason string) error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if !s.capturer.hasTrip(reason) {
		return fmt.Errorf("expected the loop to be capped with reason %s", reason)
	}
	return nil
}

// capWarningIncludesDurationField asserts at least one cap warning
// carried the quoted duration attr.
func (s *toolLoopBudgetSteps) capWarningIncludesDurationField(field string) error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if !s.capturer.hasDurationField(field) {
		return fmt.Errorf("expected the cap warning to include a %q duration field", field)
	}
	return nil
}

// parentLoopDoesNotTripToolTimeBackstop asserts the delegated turn finished
// without the total-tool-time backstop firing.
func (s *toolLoopBudgetSteps) parentLoopDoesNotTripToolTimeBackstop() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if s.capturer.hasTrip("total_tool_time_backstop") {
		return fmt.Errorf("the parent loop must not trip the tool-time backstop for delegated execution")
	}
	if s.observedStopReason(session.StopReasonToolLoopExceeded) {
		return fmt.Errorf("the delegated turn must complete naturally, not with tool_loop_exceeded")
	}
	return nil
}

// sessionWhoseBackgroundTasksNeverComplete wires a delegate tool whose
// background manager holds one task that blocks until the scenario context
// is cancelled, so activeBackgroundTaskCount stays above zero for the whole
// turn.
func (s *toolLoopBudgetSteps) sessionWhoseBackgroundTasksNeverComplete() error {
	s.cap = 0
	s.maxIterations = 3
	s.provider = &toolBudgetScriptedProvider{name: "tool-budget-provider"}
	s.capturer = &capTripCapturer{}
	s.stopReasons = nil
	s.ran = false

	spinner := &budgetSleepTool{name: "spinner", delay: 0}
	s.tools = []tool.Tool{spinner}
	s.provider.turns = []toolBudgetTurn{
		{content: "working", toolName: "spinner"},
	}

	bgMgr := engine.NewBackgroundTaskManager()
	delegate := engine.NewDelegateToolWithBackground(nil, agent.Delegation{}, "tool-budget-agent", bgMgr, nil)
	s.tools = append(s.tools, delegate)

	const bgSessionID = "tool-budget-bg-session"
	taskCtx, cancel := context.WithCancel(context.WithValue(context.Background(), session.IDKey{}, bgSessionID))
	s.bgCancel = cancel
	bgMgr.Launch(taskCtx, "stuck-bg-task", "tool-budget-agent", "never completes",
		func(ctx context.Context) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		})
	return nil
}

// toolLoopRequestsMoreThan20BackgroundTaskContinuations runs the turn whose
// background tasks never settle, forcing the loop past the continuation
// budget under a watchdog.
func (s *toolLoopBudgetSteps) toolLoopRequestsMoreThan20BackgroundTaskContinuations() error {
	return s.runTurn("tool-budget-bg-session", "Go", 15*time.Second)
}

// turnTerminatesWithToolLoopExceededStopReason asserts the terminal chunk
// carried the tool-loop-exceeded stop reason.
func (s *toolLoopBudgetSteps) turnTerminatesWithToolLoopExceededStopReason() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if !s.observedStopReason(session.StopReasonToolLoopExceeded) {
		return fmt.Errorf("expected a terminal tool_loop_exceeded stop reason")
	}
	return nil
}

// noFurtherContinuationIsInjected asserts exactly twenty background-task
// continuations were injected before the turn terminated.
func (s *toolLoopBudgetSteps) noFurtherContinuationIsInjected() error {
	if s.bgCancel != nil {
		defer s.bgCancel()
	}
	injected := s.capturer.backgroundContinuationCount()
	if injected != 20 {
		return fmt.Errorf("expected exactly 20 background-task continuations, got %d", injected)
	}
	return nil
}
