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
	cap         time.Duration
	provider    *toolBudgetScriptedProvider
	tools       []tool.Tool
	capturer    *capTripCapturer
	stopReasons []string
	ran         bool
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

// capTripCapturer is an slog handler that records the trip reason of every
// "engine tool loop capped" warning the engine emits.
type capTripCapturer struct {
	mu    sync.Mutex
	trips []string
}

// Enabled reports all levels as capturable.
func (c *capTripCapturer) Enabled(context.Context, slog.Level) bool { return true }

// Handle records the trip attribute of tool-loop cap warnings.
func (c *capTripCapturer) Handle(_ context.Context, r slog.Record) error {
	if r.Message != "engine tool loop capped" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "trip" {
			c.trips = append(c.trips, attr.Value.String())
		}
		return true
	})
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
			Tools: []string{"slowpoke", "delegate"},
		},
	}
	eng := engine.New(engine.Config{
		ChatProvider:         s.provider,
		Manifest:             manifest,
		Tools:                s.tools,
		MaxToolLoopDuration:  s.cap,
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
	ctx.Step(`^an engine with a tool loop duration cap of 200ms$`, s.engineWithToolLoopDurationCap)
	ctx.Step(`^a tool that sleeps 50ms per call and never finishes the task$`, s.toolSleepsPerCallNeverFinishing)
	ctx.Step(`^a delegation tool whose child engine runs for 500ms$`, s.delegationToolRunningChildEngine)
	ctx.Step(`^the cumulative tool execution time exceeds the cap$`, s.cumulativeToolExecutionTimeExceedsCap)
	ctx.Step(`^the tool loop is capped with reason "total_tool_time_backstop"$`, s.toolLoopCappedWithReason)
	ctx.Step(`^the delegation completes$`, s.delegationCompletes)
	ctx.Step(`^the parent tool loop does not trip the tool-time backstop$`, s.parentLoopDoesNotTripToolTimeBackstop)
}

// engineWithToolLoopDurationCap resets scenario state and sets the duration
// cap every budget guard shares.
func (s *toolLoopBudgetSteps) engineWithToolLoopDurationCap() error {
	s.cap = 200 * time.Millisecond
	s.provider = &toolBudgetScriptedProvider{name: "tool-budget-provider"}
	s.tools = nil
	s.capturer = &capTripCapturer{}
	s.stopReasons = nil
	s.ran = false
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

// delegationToolRunningChildEngine registers a TimeoutOverrider-zero tool
// whose execution models a child engine run, then scripts one delegation
// followed by a clean completion.
func (s *toolLoopBudgetSteps) delegationToolRunningChildEngine() error {
	delegate := &budgetDelegationTool{budgetSleepTool{name: "delegate", delay: 500 * time.Millisecond}}
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
// total-tool-time backstop.
func (s *toolLoopBudgetSteps) toolLoopCappedWithReason() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if !s.capturer.hasTrip("total_tool_time_backstop") {
		return fmt.Errorf("expected the loop to be capped with reason total_tool_time_backstop")
	}
	if !s.observedStopReason(session.StopReasonToolLoopExceeded) {
		return fmt.Errorf("expected a terminal tool_loop_exceeded stop reason")
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
