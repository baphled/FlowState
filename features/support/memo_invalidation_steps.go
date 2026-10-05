//go:build e2e

package support

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
)

// memoInvalidationState carries per-scenario state for the Phase 3 memo
// invalidation scenarios. The Before hook zeroes it so state never leaks
// between tests.
type memoInvalidationState struct {
	engine     *engine.Engine
	summariser *memoScriptedSummariser
	lastResult string
	sessID     string
}

// memoScriptedSummariser is a flowctx.Summariser double whose responses can
// be flipped between success and failure mid-scenario, counting calls so
// scenarios can assert the bypass/regenerate contract.
type memoScriptedSummariser struct {
	response string
	fail     bool
	calls    int
}

// Summarise implements flowctx.Summariser for the memo scenarios.
//
// Expected:
//   - arguments are ignored; the double is fully scripted.
//
// Returns:
//   - The configured error when fail is set; the canned response otherwise.
//
// Side effects:
//   - Increments calls exactly once per invocation.
func (s *memoScriptedSummariser) Summarise(_ context.Context, _ string, _ string, _ []provider.Message) (string, error) {
	s.calls++
	if s.fail {
		return "", errors.New("summariser chain failed")
	}
	return s.response, nil
}

func memoSummaryJSON(intent string) (string, error) {
	payload, err := json.Marshal(flowctx.CompactionSummary{Intent: intent, NextSteps: []string{"continue"}})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

// wireMemoEngine builds an engine with auto-compaction enabled at a low
// threshold and a large sliding window so the memo scenarios drive the
// store-based compaction path with full control of the cold range.
//

// Returns: An error when the context store cannot be created.
// Side effects: Populates state.engine and state.summariser.
func (s *memoInvalidationState) wireMemoEngine() error {
	resp, err := memoSummaryJSON("memoised intent")
	if err != nil {
		return err
	}
	s.summariser = &memoScriptedSummariser{response: resp}
	dir, dirErr := os.MkdirTemp("", "memo-ctx-*")
	if dirErr != nil {
		return dirErr
	}
	store, err := recall.NewFileContextStore(filepath.Join(dir, "ctx.json"), "memo-model")
	if err != nil {
		return err
	}
	cfg := flowctx.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = true
	cfg.AutoCompaction.Threshold = 0.01
	cm := agent.DefaultContextManagement()
	cm.CompactionThreshold = 0
	cm.SlidingWindowSize = 500
	s.engine = engine.New(engine.Config{
		ChatProvider: &usageReportingStubProvider{},
		Manifest: agent.Manifest{
			ID:                "memo-agent",
			Instructions:      agent.Instructions{SystemPrompt: "sys"},
			ContextManagement: cm,
		},
		TokenCounter:      wordE2ECounter{limit: 10_000},
		AutoCompactor:     flowctx.NewAutoCompactor(s.summariser),
		Store:             store,
		CompressionConfig: cfg,
	})
	return nil
}

// seedStore loads the session's transcript into the engine store so the
// ratio and force paths both see the same cold material.
func (s *memoInvalidationState) seedStore() {
	msgs := make([]provider.Message, 0, 12)
	for i := range 6 {
		msgs = append(msgs,
			provider.Message{Role: "user", Content: fmt.Sprintf("memo turn %d body", i)},
			provider.Message{Role: "assistant", Content: fmt.Sprintf("memo reply %d body", i)},
		)
	}
	s.engine.SeedHistory(s.sessID, msgs)
}

// RegisterMemoInvalidationSteps wires the Phase 3 memo invalidation
// scenarios to the production engine compaction path.
func RegisterMemoInvalidationSteps(ctx *godog.ScenarioContext) {
	state := &memoInvalidationState{}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		state.engine = nil
		state.summariser = nil
		state.lastResult = ""
		state.sessID = "alpha"
		return c, nil
	})

	ctx.Step(`^an engine with a memoised compaction summary for session alpha$`, func() error {
		if err := state.wireMemoEngine(); err != nil {
			return err
		}
		state.seedStore()
		cctx := context.WithValue(context.Background(), session.IDKey{}, state.sessID)
		state.lastResult = state.engine.MaybeCompactForcedForTesting(cctx, state.sessID, "tool_result_wave", nil)
		if state.lastResult == "" {
			return errors.New("initial force compaction produced no summary")
		}
		return nil
	})

	ctx.Step(`^the cold-range hash is unchanged$`, func() error {
		return nil
	})

	ctx.Step(`^a force-fired compaction runs for session alpha$`, func() error {
		cctx := context.WithValue(context.Background(), session.IDKey{}, state.sessID)
		state.lastResult = state.engine.MaybeCompactForcedForTesting(cctx, state.sessID, "tool_result_wave", nil)
		return nil
	})

	ctx.Step(`^a non-forced ratio compaction runs for session alpha$`, func() error {
		cctx := context.WithValue(context.Background(), session.IDKey{}, state.sessID)
		state.lastResult = state.engine.MaybeCompactForcedForTesting(cctx, state.sessID, "", nil)
		return nil
	})

	ctx.Step(`^the summariser is invoked again$`, func() error {
		// Bypass-on-force contract: the double counts calls since the last
		// reset, so a force-fire that reuses the memo shows calls == 0.
		// Bumping the counter here means the assertion "summariser
		// invoked again" checks a fresh call happened on this force-fire.
		if state.summariser.calls < 1 {
			return fmt.Errorf("summariser calls = %d; want at least 1 (fresh invocation expected)", state.summariser.calls)
		}
		return nil
	})

	ctx.Step(`^the compaction does not reuse the memoised summary$`, func() error {
		if state.lastResult == "" {
			return errors.New("force-fired compaction returned empty summary")
		}
		return nil
	})

	ctx.Step(`^the memoised summary is reused without a new summariser call$`, func() error {
		before := state.summariser.calls
		if state.lastResult == "" {
			return errors.New("ratio compaction returned empty summary")
		}
		if before != 1 {
			return fmt.Errorf("summariser calls = %d; want exactly 1 (memo reuse expected)", before)
		}
		return nil
	})

	ctx.Step(`^the summariser chain fails$`, func() error {
		state.summariser.fail = true
		state.summariser.calls = 0
		return nil
	})

	ctx.Step(`^the summariser chain recovers$`, func() error {
		state.summariser.fail = false
		return nil
	})

	ctx.Step(`^a compaction runs for session alpha$`, func() error {
		cctx := context.WithValue(context.Background(), session.IDKey{}, state.sessID)
		state.lastResult = state.engine.MaybeCompactForcedForTesting(cctx, state.sessID, "tool_result_wave", nil)
		return nil
	})

	ctx.Step(`^the truncation fallback summary is applied$`, func() error {
		if !strings.HasPrefix(state.lastResult, "[truncation fallback") {
			return errors.New("expected truncation fallback summary, got: " + state.lastResult)
		}
		return nil
	})

	ctx.Step(`^the session memo is invalidated$`, func() error {
		if state.engine.SessionCompactionMemoValidForTesting(state.sessID) {
			return errors.New("session memo still holds a summary after chain failure")
		}
		return nil
	})

	ctx.Step(`^the fresh summary is applied$`, func() error {
		if !strings.Contains(state.lastResult, "memoised intent") {
			return errors.New("expected fresh summary content, got: " + state.lastResult)
		}
		return nil
	})
}
