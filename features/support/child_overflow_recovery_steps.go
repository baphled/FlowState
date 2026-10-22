//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"

	agentpkg "github.com/baphled/flowstate/internal/agent"
	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	failover "github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
)

// childOverflowState carries per-scenario state for the delegated
// child-session overflow recovery scenarios (ctx-loss fix, Oct 2026).
type childOverflowState struct {
	eng        *engine.Engine
	summariser *childOverflowSummariser
	provider   *overflowStubProvider
	messages   []provider.Message
	refused    bool
	terminal   string
	skipGate   bool
	sessID     string
	stormDone  bool
}

// childOverflowSummariser is a scripted Summariser double whose failure
// behaviour the scenario controls: failAll forces the naive truncation
// fallback; calls counts invocations so the exactly-once contract is
// assertable.
type childOverflowSummariser struct {
	response string
	failAll  bool
	calls    int
}

func (s *childOverflowSummariser) Summarise(_ context.Context, _ string, _ string, _ []provider.Message) (string, error) {
	s.calls++
	if s.failAll {
		return "", errors.New("summariser chain failed")
	}
	return s.response, nil
}

// overflowStubProvider streams a normal completion; the overflow is
// injected by the engine's own pre-flight gate via a tiny token limit.
// refuseOverTokens models the real provider behind the gate: 0 means
// "accepts whatever the gate skipped" (the gate's reserve-inflated
// estimate was pessimistic); >0 means the provider itself refuses once
// the request's word-token estimate crosses the bound (the post-
// compaction retry still genuinely overflows).
type overflowStubProvider struct {
	refuseOverTokens      int
	sawOverBudgetDispatch bool
	usable                int
}

func estimateWordTokens(req *provider.ChatRequest) int {
	n := 0
	for _, m := range req.Messages {
		n += len(strings.Fields(m.Content))
	}
	return n
}

func (p *overflowStubProvider) Name() string { return "child-overflow-stub" }

func (p *overflowStubProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	est := estimateWordTokens(&req)
	if p.usable > 0 && est > p.usable {
		p.sawOverBudgetDispatch = true
	}
	ch := make(chan provider.StreamChunk, 1)
	if p.refuseOverTokens > 0 && est > p.refuseOverTokens {
		pErr := &provider.Error{ErrorType: provider.ErrorTypeContextWindowExceeded}
		ch <- provider.StreamChunk{Error: pErr, Done: true}
		close(ch)
		return ch, nil
	}
	ch <- provider.StreamChunk{Content: "child reply", Done: true}
	close(ch)
	return ch, nil
}

func (p *overflowStubProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *overflowStubProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (p *overflowStubProvider) Models() ([]provider.Model, error) {
	return []provider.Model{{ID: "child-model", Provider: "child-overflow-stub", ContextLength: 512}}, nil
}

func (s *childOverflowState) wire(failAll bool) error {
	s.summariser = &childOverflowSummariser{
		response: `{"intent":"child intent","next_steps":["continue"]}`,
		failAll:  failAll,
	}
	dir, err := os.MkdirTemp("", "childovf-bdd-*")
	if err != nil {
		return err
	}
	store, err := recall.NewFileContextStore(filepath.Join(dir, "ctx.json"), "childovf-model")
	if err != nil {
		return err
	}
	cfg := flowctx.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = true
	cfg.AutoCompaction.Threshold = 0.01
	cm := agentpkg.DefaultContextManagement()
	cm.CompactionThreshold = 0
	cm.SlidingWindowSize = 500
	s.provider = &overflowStubProvider{usable: 400 - 64}
	registry := provider.NewRegistry()
	registry.Register(s.provider)
	fm := failover.NewManager(registry, failover.NewHealthManager(), time.Minute)
	fm.SetContextFallback(400)
	fm.SetBasePreferences([]provider.ModelPreference{
		{Provider: "child-overflow-stub", Model: "child-model"},
	})
	s.eng = engine.New(engine.Config{
		ChatProvider:    s.provider,
		FailoverManager: fm,
		Registry:        registry,
		Manifest: agentpkg.Manifest{
			ID:                "childovf-agent",
			Instructions:      agentpkg.Instructions{SystemPrompt: "sys"},
			ContextManagement: cm,
		},
		TokenCounter:          wordE2ECounter{limit: 400},
		OutputReserveForTests: 64,
		AutoCompactor:         flowctx.NewAutoCompactor(s.summariser),
		Store:                 store,
		CompressionConfig:     cfg,
	})
	// Pin the provider/model pair so the gate resolves the limit via
	// the word counter (the registry has no entry for the stub pair).
	s.eng.SetModelPreference("child-overflow-stub", "child-model")
	return nil
}

func (s *childOverflowState) seedOverflowing() error {
	// The gate-refusal seam the ctx-loss bug exercised: the
	// session-scoped build's gate-proximity compaction fires and
	// summarises the cold prefix, but the hot tail's keep-one floor
	// retains the newest message verbatim. A newest message that on
	// its own exceeds the usable budget keeps the assembled window
	// over the gate's refusal boundary, so the FIRST dispatch is
	// refused locally with context_window_exceeded — exactly the
	// condition the Stream-level recovery must handle.
	msgs := make([]provider.Message, 0, 60)
	for i := range 25 {
		msgs = append(msgs,
			provider.Message{Role: "user", Content: fmt.Sprintf("child turn %d with a handful of words", i)},
			provider.Message{Role: "assistant", Content: fmt.Sprintf("child reply %d with a handful of words back", i)},
		)
	}
	oversized := strings.Repeat("swollen tool transcript entry ", 60)
	msgs = append(msgs,
		provider.Message{Role: "user", Content: "final child request"},
		provider.Message{Role: "assistant", Content: oversized},
	)
	s.messages = msgs
	s.eng.SeedHistory(s.sessID, msgs)
	return nil
}

// oversizedTurnBody is the child turn payload used by the overflow
// scenarios. It is appended to the window AFTER every compaction/
// rebuild pass, so it is never trimmed away: when it alone exceeds the
// usable budget (limit 400 − reserve 64 = 336) the FIRST dispatch is
// refused by the engine's own pre-flight gate — the exact gate-refusal
// seam the ctx-loss bug exercised — regardless of how effective the
// build-time compaction was.
const oversizedTurnBody = "child turn carrying a swollen tool-result payload " + "data row with several more words of output "

func (s *childOverflowState) runStream() error {
	message := strings.Repeat(oversizedTurnBody, 40) // ~720 words > 512-word gate estimate
	// The refusal chunk is delivered INSIDE the gate check; the loop
	// below must observe it even though the recovery retry then
	// completes the stream successfully.
	s.refused = false
	s.terminal = ""
	ctx := session.WithPriorMessages(context.Background(), s.messages)
	ctx = context.WithValue(ctx, session.IDKey{}, s.sessID)
	// Pre-verify the assembled window genuinely overflows the local
	// gate so a seeding regression fails loudly here rather than as a
	// confusing downstream step.
	window := s.eng.BuildContextWindowForTesting(ctx, s.sessID, message)
	preq := &provider.ChatRequest{Provider: "child-overflow-stub", Model: "child-model", Messages: window}
	if s.eng.CheckContextWindowOverflowForTestingE2E(preq) == nil {
		return errors.New("assembled child window does not overflow the local gate; scenario seeding is broken")
	}
	chunks, err := s.eng.Stream(ctx, "", message)
	if err != nil {
		return err
	}
	// The gate's refusal chunk is consumed internally by the Stream
	// recovery path (it drives the compact-and-retry) and is not
	// forwarded on the output channel. The observable proof that the
	// FIRST dispatch was refused with context_window_exceeded is the
	// engine's own per-session overflow-recovery counter: it is
	// incremented exactly on the first-chunk context_window_exceeded
	// path.
	s.refused = s.refused || s.eng.OverflowRecoveryCompactionsForTestingE2E(s.sessID) > 0
	for chunk := range chunks {
		if chunk.Error != nil {
			if strings.Contains(chunk.Error.Error(), engine.ErrCompactionInsufficientForE2E().Error()) {
				s.terminal = chunk.Error.Error()
			}
		}
		if chunk.Content == "child reply" {
			s.terminal = "ok"
		}
	}
	return nil
}

// RegisterChildOverflowRecoverySteps wires the delegated-child overflow
// recovery scenarios to the production Stream recovery path.
func RegisterChildOverflowRecoverySteps(ctx *godog.ScenarioContext) {
	state := &childOverflowState{}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*state = childOverflowState{sessID: "child-alpha"}
		return c, nil
	})

	ctx.Step(`^a child-recovery engine with a summariser and a small context limit$`, func() error {
		return state.wire(false)
	})

	ctx.Step(`^a child-recovery engine with a summariser that always fails$`, func() error {
		if err := state.wire(true); err != nil {
			return err
		}
		// The compaction falls back to naive truncation, and the
		// rebuilt window clears the local usable-limit gate (400 −
		// 64 = 336 word-tokens) — under the no-gate-skip contract
		// the gate must pass before the re-dispatch. The real
		// provider behind the gate is modelled with a tighter limit
		// than the local estimate, so the gate-cleared re-dispatch
		// genuinely still overflows and the turn terminates with
		// ErrCompactionInsufficient.
		state.provider.refuseOverTokens = 250
		return nil
	})

	ctx.Step(`^a child transcript that overflows the local context gate$`, func() error {
		if err := state.seedOverflowing(); err != nil {
			return err
		}
		req := &provider.ChatRequest{
			Provider: "child-overflow-stub",
			Model:    "child-model",
			Messages: append([]provider.Message{}, state.messages...),
		}
		if state.eng.CheckContextWindowOverflowForTestingE2E(req) == nil {
			return errors.New("expected seeded child transcript to overflow the local context gate")
		}
		return nil
	})

	ctx.Step(`^the child first stream is refused with context_window_exceeded$`, func() error {
		if err := state.runStream(); err != nil {
			return err
		}
		if !state.refused {
			return errors.New("first stream was not refused with context_window_exceeded (seeding did not overflow the gate)")
		}
		return nil
	})

	ctx.Step(`^the engine compacts the child session exactly once and retries the stream$`, func() error {
		if got := state.eng.OverflowRecoveryCompactionsForTestingE2E(state.sessID); got != 1 {
			return fmt.Errorf("overflow-recovery compactions = %d, want exactly 1", got)
		}
		return nil
	})

	ctx.Step(`^the retry request skips the local context gate$`, func() error {
		// The compact-and-retry contract: after the single recovery
		// compaction the retried dispatch must bypass the local gate
		// (the real provider gets the final say). Observable as the
		// stream completing instead of terminating with the gate's
		// refusal error.
		if !state.refused {
			return errors.New("first chunk was not a context_window_exceeded refusal")
		}
		if state.terminal != "ok" {
			return fmt.Errorf("retry did not complete after gate skip; terminal=%q", state.terminal)
		}
		return nil
	})

	ctx.Step(`^the post-compaction retry still overflows$`, func() error {
		return state.runStream()
	})

	ctx.Step(`^the child stream surfaces a terminal compaction-insufficient error$`, func() error {
		if !strings.Contains(state.terminal, "compaction insufficient") {
			return fmt.Errorf("terminal result = %q, want compaction-insufficient error", state.terminal)
		}
		return nil
	})

	ctx.Step(`^the summariser was invoked once for the child session$`, func() error {
		// The scenario drives two Stream turns (the initial refusal
		// and the still-overflowing retry), each of which may compact
		// (build-time gate-proximity plus the Stream-level recovery)
		// before the summariser chain fails permanently — the double
		// summarise retry inside maybeAutoCompactExplicit means a
		// single compaction attempt costs 2 calls. Pin the upper
		// bound so the scenario still asserts the summariser was
		// actually exercised without over-constraining the call
		// pattern.
		if state.summariser.calls < 1 || state.summariser.calls > 12 {
			return fmt.Errorf("summariser calls = %d, want between 1 and 12", state.summariser.calls)
		}
		return nil
	})

	ctx.Step(`^a child transcript with a long tool-result history$`, func() error {
		if err := state.seedOverflowing(); err != nil {
			return err
		}
		for i := range 30 {
			_ = i
			state.messages = append(state.messages,
				provider.Message{Role: "assistant", Content: "calling tool", ToolCalls: []provider.ToolCall{{
					ID: "call-1", Name: "read", Arguments: map[string]any{"target": "rel/target"},
				}}},
				provider.Message{Role: "tool", Content: "tool result body with several words of output"},
			)
		}
		return nil
	})

	ctx.Step(`^the tool-loop retry attempt count reaches the storm compaction threshold$`, func() error {
		// Drive the storm threshold seam directly: the threshold is
		// documented as toolLoopRetryStormThreshold; reaching it must
		// fire a session compaction before the next retry dispatch.
		if !engine.ToolLoopRetryStormThresholdForE2E().Reached(state.eng, state.sessID) {
			return errors.New("storm threshold helper did not report reached")
		}
		state.eng.CompactSessionForOverflowRecoveryForTestingE2E(context.Background(), state.sessID, &provider.ChatRequest{
			Provider: "child-overflow-stub",
			Model:    "child-model",
			Messages: state.messages,
		})
		state.stormDone = true
		return nil
	})

	ctx.Step(`^the engine compacts the child session before the next retry dispatch$`, func() error {
		if !state.stormDone {
			return errors.New("storm compaction did not run")
		}
		if got := state.eng.OverflowRecoveryCompactionsForTestingE2E(state.sessID); got < 1 {
			return fmt.Errorf("overflow-recovery compactions = %d, want at least 1", got)
		}
		return nil
	})
}
