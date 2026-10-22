//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/cucumber/godog"

	agentpkg "github.com/baphled/flowstate/internal/agent"
	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
)

// contextLossState carries per-scenario state for the context-loss
// regression guard scenarios. The Before hook zeroes it so state never
// leaks between scenarios.
type contextLossState struct {
	eng        *engine.Engine
	summariser *contextLossSummariser
	store      *recall.FileContextStore
	messages   []provider.Message
	rebuilt    []provider.Message
	result     string
	sessID     string
	betaFiles  []string
	fallback   string
}

// contextLossSummariser is a scripted Summariser double whose failure
// behaviour the scenario controls: failAlways drives the truncation
// fallback path, failFirst succeeds on the second call so the retry
// contract can be asserted.
type contextLossSummariser struct {
	response   string
	failAlways bool
	failFirst  bool
	calls      atomic.Int32
}

func (s *contextLossSummariser) Summarise(_ context.Context, _ string, _ string, _ []provider.Message) (string, error) {
	n := s.calls.Add(1)
	if s.failAlways || (s.failFirst && n == 1) {
		return "", errors.New("summariser chain failed")
	}
	return s.response, nil
}

func (s *contextLossState) wire(enabled bool, tokenLimit int) error {
	s.summariser = &contextLossSummariser{response: `{"intent":"ctx-loss intent","next_steps":["continue"]}`}
	dir, err := os.MkdirTemp("", "ctxloss-bdd-*")
	if err != nil {
		return err
	}
	store, err := recall.NewFileContextStore(filepath.Join(dir, "ctx.json"), "ctxloss-model")
	if err != nil {
		return err
	}
	s.store = store
	cfg := flowctx.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = enabled
	cfg.AutoCompaction.Threshold = 0.01
	cm := agentpkg.DefaultContextManagement()
	cm.CompactionThreshold = 0
	cm.SlidingWindowSize = 500
	s.eng = engine.New(engine.Config{
		ChatProvider: &usageReportingStubProvider{},
		Manifest: agentpkg.Manifest{
			ID:                "ctxloss-agent",
			Instructions:      agentpkg.Instructions{SystemPrompt: "sys"},
			ContextManagement: cm,
		},
		TokenCounter: wordE2ECounter{limit: tokenLimit},
		AutoCompactor: func() *flowctx.AutoCompactor {
			if !enabled {
				return nil
			}
			return flowctx.NewAutoCompactor(s.summariser)
		}(),
		Store:             store,
		CompressionConfig: cfg,
	})
	return nil
}

// RegisterContextLossGuardSteps wires the context-loss regression guard
// scenarios to the production compaction paths.
func RegisterContextLossGuardSteps(ctx *godog.ScenarioContext) {
	state := &contextLossState{}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*state = contextLossState{sessID: "alpha"}
		return c, nil
	})

	ctx.Step(`^an engine with a summariser that is unavailable for compaction$`, func() error {
		if err := state.wire(true, 100_000); err != nil {
			return err
		}
		state.summariser.failAlways = true
		return nil
	})

	ctx.Step(`^a session transcript of (\d+) short messages$`, func(n int) error {
		state.messages = make([]provider.Message, 0, n)
		for i := range n {
			state.messages = append(state.messages, provider.Message{
				Role:    "user",
				Content: fmt.Sprintf("turn %d body words here", i),
			})
		}
		state.eng.SeedHistory(state.sessID, state.messages)
		return nil
	})

	ctx.Step(`^the naive truncation fallback rebuild runs for the session$`, func() error {
		cctx := context.WithValue(context.Background(), session.IDKey{}, state.sessID)
		state.fallback = state.eng.MaybeCompactForcedForTesting(cctx, state.sessID, "gate_proximity", state.messages)
		if !strings.HasPrefix(state.fallback, "[truncation fallback") {
			return errors.New("expected truncation fallback summary, got: " + state.fallback)
		}
		state.rebuilt = state.eng.RebuildExplicitPathForTesting(cctx, state.sessID, state.messages, state.fallback)
		return nil
	})

	ctx.Step(`^the rebuilt window retains a token-proportional tail of at least (\d+) percent of the session transcript$`, func(pct int) error {
		retained := 0
		for _, m := range state.rebuilt {
			retained += len(strings.Fields(m.Content))
		}
		total := 0
		for _, m := range state.messages {
			total += len(strings.Fields(m.Content))
		}
		if total == 0 || retained*100 < pct*total {
			return fmt.Errorf("retained %d of %d words; want at least %d%%", retained, total, pct)
		}
		return nil
	})

	ctx.Step(`^the fallback emission is logged loudly$`, func() error {
		if state.eng.FallbackEmissionsForTesting(state.sessID) < 1 {
			return errors.New("no loud fallback emission recorded for the session")
		}
		return nil
	})

	ctx.Step(`^the session memo is still valid$`, func() error {
		if !state.eng.SessionCompactionMemoValidForTesting(state.sessID) {
			return errors.New("session memo was destroyed by the fallback")
		}
		return nil
	})

	ctx.Step(`^an engine with a flaky summariser that succeeds on the second call$`, func() error {
		if err := state.wire(true, 100_000); err != nil {
			return err
		}
		state.summariser.failFirst = true
		return nil
	})

	// Locally-owned twins of the memo suite's engine steps: the
	// context-loss scenarios wire their own engine state so a nil
	// engine (memo suite never ran) cannot panic these steps.
	ctx.Step(`^a context-loss engine with a memoised compaction summary for session alpha$`, func() error {
		if err := state.wire(true, 100_000); err != nil {
			return err
		}
		state.messages = make([]provider.Message, 0, 12)
		for i := range 6 {
			state.messages = append(state.messages,
				provider.Message{Role: "user", Content: fmt.Sprintf("memo turn %d body", i)},
				provider.Message{Role: "assistant", Content: fmt.Sprintf("memo reply %d body", i)},
			)
		}
		state.eng.SeedHistory(state.sessID, state.messages)
		cctx := context.WithValue(context.Background(), session.IDKey{}, state.sessID)
		if res := state.eng.MaybeCompactForcedForTesting(cctx, state.sessID, "tool_result_wave", nil); res == "" {
			return errors.New("initial memoised compaction produced no summary")
		}
		return nil
	})

	ctx.Step(`^the context-loss compaction preconditions degrade with the summariser unresolvable$`, func() error {
		// Environmental precondition failure: summariser cannot even
		// reach a provider, so the summarise call fails and the
		// engine degrades to the truncation fallback.
		state.summariser.failAlways = true
		cctx := context.WithValue(context.Background(), session.IDKey{}, state.sessID)
		state.result = state.eng.MaybeCompactForcedForTesting(cctx, state.sessID, "gate_proximity", nil)
		return nil
	})

	ctx.Step(`^the context-loss fresh summary is applied$`, func() error {
		if !strings.HasPrefix(state.result, "[auto-compacted summary]") {
			return errors.New("expected fresh summary content, got: " + state.result)
		}
		return nil
	})

	ctx.Step(`^the context-loss truncation fallback summary is applied$`, func() error {
		if !strings.HasPrefix(state.result, "[truncation fallback") {
			return errors.New("expected truncation fallback summary, got: " + state.result)
		}
		return nil
	})

	ctx.Step(`^the context-loss session memo is invalidated$`, func() error {
		if state.eng.SessionCompactionMemoValidForTesting(state.sessID) {
			return errors.New("session memo still holds a summary after chain failure")
		}
		return nil
	})

	ctx.Step(`^the summariser was called exactly (\d+) times$`, func(n int) error {
		if got := state.summariser.calls.Load(); got != int32(n) {
			return fmt.Errorf("summariser calls = %d; want %d", got, n)
		}
		return nil
	})

	// A compaction now runs for the context-loss scenarios: drive the
	// forced compaction and record the result. Implemented locally (as
	// `a compaction now runs for session alpha`) because the memo
	// suite already owns the plain `a compaction runs for session
	// alpha` regex and godog rejects duplicate step definitions.
	ctx.Step(`^a compaction now runs for session alpha$`, func() error {
		state.eng.SeedHistory(state.sessID, state.messages)
		cctx := context.WithValue(context.Background(), session.IDKey{}, state.sessID)
		state.result = state.eng.MaybeCompactForcedForTesting(cctx, state.sessID, "tool_result_wave", nil)
		return nil
	})

	ctx.Step(`^an engine holding compaction summaries for two sessions$`, func() error {
		if err := state.wire(true, 100_000); err != nil {
			return err
		}
		return nil
	})

	ctx.Step(`^rehydration is attempted for session beta$`, func() error {
		alphaSummary := flowctx.CompactionSummary{Intent: "alpha intent", FilesToRestore: []string{"/tmp/alpha-only.md"}}
		betaSummary := flowctx.CompactionSummary{Intent: "beta intent", FilesToRestore: []string{"/nonexistent-beta-file.md"}}
		state.eng.SetLastCompactionSummaryForTesting("alpha-session", alphaSummary)
		state.eng.SetLastCompactionSummaryForTesting("beta-session", betaSummary)
		state.betaFiles = betaSummary.FilesToRestore
		state.rebuilt = state.eng.MaybeRehydrateForTesting("beta-session", []provider.Message{
			{Role: "user", Content: "beta turn"},
		})
		return nil
	})

	ctx.Step(`^the injected files are only those listed by session beta's summary$`, func() error {
		alphaLeaked := false
		for _, m := range state.rebuilt {
			if strings.Contains(m.Content, "/tmp/alpha-only.md") {
				alphaLeaked = true
			}
		}
		if alphaLeaked {
			return errors.New("session alpha's summary leaked into beta's rehydration")
		}
		return nil
	})

	ctx.Step(`^an engine with a large context budget$`, func() error {
		return state.wire(true, 100_000)
	})

	ctx.Step(`^an engine with a small context budget$`, func() error {
		// Small enough that the ~2.4k-word transcript tool result
		// cannot be retained verbatim and must be head-capped.
		return state.wire(true, 2_000)
	})

	ctx.Step(`^a transcript with an old compactable tool call and a long result$`, func() error {
		state.messages = []provider.Message{{Role: "user", Content: "do the thing"},
			{Role: "assistant", Content: "calling read on target", ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "read", Arguments: map[string]any{"target": "ctxloss/target"}}}},
			{Role: "tool", Content: strings.Repeat("head ", 1200) + "\n" + strings.Repeat("tail ", 1200)},
			{Role: "assistant", Content: "done with the tool"}}
		return nil
	})

	// Locally-owned twin of the phase3 suite's stubbing steps: the
	// context-loss scenarios wire their own engine, so the rebuild
	// must read contextLossState rather than the phase3 state.
	ctx.Step(`^a context-loss transcript with an old compactable tool call and result$`, func() error {
		state.messages = []provider.Message{{Role: "user", Content: "do the thing"},
			{Role: "assistant", Content: "calling read on target", ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "read", Arguments: map[string]any{"target": "ctxloss/target"}}}},
			{Role: "tool", Content: "tool result body with several words"},
			{Role: "assistant", Content: "done with the tool"}}
		return nil
	})

	ctx.Step(`^the context-loss window is rebuilt with tool stubbing$`, func() error {
		state.rebuilt = state.eng.StubToolCallsForTesting(state.messages)
		return nil
	})

	ctx.Step(`^the old tool result is retained verbatim$`, func() error {
		for _, m := range state.rebuilt {
			if m.Role != "tool" {
				continue
			}
			if strings.Contains(m.Content, "[tool stub:") {
				return errors.New("tool result was stubbed although the budget allowed verbatim retention")
			}
			return nil
		}
		return errors.New("no tool result found in rebuilt window")
	})

	ctx.Step(`^the old tool result is retained up to a 2 KiB head$`, func() error {
		for _, m := range state.rebuilt {
			if m.Role != "tool" {
				continue
			}
			if !strings.HasPrefix(m.Content, "head ") {
				head := m.Content
				if len(head) > 80 {
					head = head[:80]
				}
				return errors.New("capped head not retained: " + head)
			}
			if len(m.Content) > 3000 {
				return fmt.Errorf("retained head %d bytes exceeds the 2 KiB cap", len(m.Content))
			}
			return nil
		}
		return errors.New("no tool result found in rebuilt window")
	})

	ctx.Step(`^the retained stub does not reference the coordination store$`, func() error {
		for _, m := range state.rebuilt {
			if m.Role == "tool" && strings.Contains(m.Content, "coordination_store") {
				return errors.New("false coordination-store promise retained in stub")
			}
		}
		return nil
	})
}
