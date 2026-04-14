package support

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/cucumber/godog"

	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// autoCompactionState holds per-scenario state for @auto-compaction
// scenarios. A single instance is reused across scenarios; the Before
// hook zeroes it on scenario entry so state never leaks between tests.
type autoCompactionState struct {
	summariser *scriptedSummariser
	compactor  *flowctx.AutoCompactor

	coldMessages []provider.Message

	summary    flowctx.CompactionSummary
	compactErr error

	rehydrateSummary  flowctx.CompactionSummary
	rehydrateMessages []provider.Message
	rehydrateErr      error

	tempDir string
}

// scriptedSummariser is a flowctx.Summariser double for BDD scenarios.
// It returns a canned response (or error) and counts invocations so the
// "called exactly once" assertions can verify the no-retry contract.
type scriptedSummariser struct {
	response string
	err      error
	calls    atomic.Int32
}

// Summarise implements flowctx.Summariser for BDD scenarios.
//
// Expected:
//   - ctx, systemPrompt, userPrompt, msgs are ignored; the double
//     is fully scripted.
//
// Returns:
//   - The preconfigured response string on success.
//   - The preconfigured error when err is non-nil.
//
// Side effects:
//   - Increments the calls counter exactly once per invocation.
func (s *scriptedSummariser) Summarise(_ context.Context, _ string, _ string, _ []provider.Message) (string, error) {
	s.calls.Add(1)
	if s.err != nil {
		return "", s.err
	}
	return s.response, nil
}

// RegisterAutoCompactionSteps wires the @auto-compaction scenarios to
// the real AutoCompactor. Step definitions keep the scenarios readable
// and free of wiring noise: each Given sets a single piece of state,
// each When calls the production API, and each Then asserts an
// observable outcome.
//
// Expected:
//   - ctx is a non-nil godog.ScenarioContext.
//
// Returns:
//   - None.
//
// Side effects:
//   - Registers Given/When/Then steps and Before/After hooks that
//     allocate and release a temp directory used by the rehydration
//     scenarios.
func RegisterAutoCompactionSteps(ctx *godog.ScenarioContext) {
	state := &autoCompactionState{}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		state.summariser = nil
		state.compactor = nil
		state.coldMessages = nil
		state.summary = flowctx.CompactionSummary{}
		state.compactErr = nil
		state.rehydrateSummary = flowctx.CompactionSummary{}
		state.rehydrateMessages = nil
		state.rehydrateErr = nil

		dir, err := os.MkdirTemp("", "auto-compaction-bdd-*")
		if err != nil {
			return c, fmt.Errorf("temp dir: %w", err)
		}
		state.tempDir = dir
		return c, nil
	})

	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if state.tempDir != "" {
			_ = os.RemoveAll(state.tempDir)
			state.tempDir = ""
		}
		return c, nil
	})

	ctx.Step(`^the L2 summariser returns a valid compaction summary$`, func() error {
		payload, err := bddSummaryJSON(func(s *flowctx.CompactionSummary) {
			s.Intent = "continue the Phase 2 BDD slice"
			s.NextSteps = []string{"commit scenarios"}
		})
		if err != nil {
			return err
		}
		state.summariser = &scriptedSummariser{response: payload}
		state.compactor = flowctx.NewAutoCompactor(state.summariser)
		return nil
	})

	ctx.Step(`^the L2 summariser returns a summary with empty next_steps$`, func() error {
		payload, err := bddSummaryJSON(func(s *flowctx.CompactionSummary) {
			s.Intent = "continue the Phase 2 BDD slice"
			s.NextSteps = nil
		})
		if err != nil {
			return err
		}
		state.summariser = &scriptedSummariser{response: payload}
		state.compactor = flowctx.NewAutoCompactor(state.summariser)
		return nil
	})

	ctx.Step(`^I have a cold slice of (\d+) messages to summarise$`, func(n int) error {
		state.coldMessages = make([]provider.Message, 0, n)
		for i := range n {
			state.coldMessages = append(state.coldMessages, provider.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("message %d body", i),
			})
		}
		return nil
	})

	ctx.Step(`^the auto-compactor runs$`, func() error {
		if state.compactor == nil {
			return errors.New("compactor not configured by a prior Given step")
		}
		state.summary, state.compactErr = state.compactor.Compact(context.Background(), state.coldMessages)
		return nil
	})

	ctx.Step(`^the resulting summary has a non-empty intent$`, func() error {
		if state.compactErr != nil {
			return fmt.Errorf("unexpected compact error: %w", state.compactErr)
		}
		if strings.TrimSpace(state.summary.Intent) == "" {
			return errors.New("summary.Intent is empty")
		}
		return nil
	})

	ctx.Step(`^the resulting summary has at least one next step$`, func() error {
		if state.compactErr != nil {
			return fmt.Errorf("unexpected compact error: %w", state.compactErr)
		}
		if len(state.summary.NextSteps) == 0 {
			return errors.New("summary.NextSteps is empty")
		}
		return nil
	})

	ctx.Step(`^the summariser was called exactly once$`, func() error {
		got := state.summariser.calls.Load()
		if got != 1 {
			return fmt.Errorf("summariser calls = %d; want exactly 1", got)
		}
		return nil
	})

	ctx.Step(`^the auto-compactor returns an invalid-summary error$`, func() error {
		if state.compactErr == nil {
			return errors.New("compact returned no error; want ErrInvalidSummary")
		}
		if !errors.Is(state.compactErr, flowctx.ErrInvalidSummary) {
			return fmt.Errorf("compact err = %w; want ErrInvalidSummary", state.compactErr)
		}
		return nil
	})

	ctx.Step(`^a compaction summary whose intent anchors the next turn$`, func() error {
		state.rehydrateSummary = flowctx.CompactionSummary{
			Intent: "resume after Phase 2 compaction",
		}
		return nil
	})

	ctx.Step(`^two files are queued for rehydration$`, func() error {
		pathA := filepath.Join(state.tempDir, "file-a.txt")
		pathB := filepath.Join(state.tempDir, "file-b.txt")
		if err := os.WriteFile(pathA, []byte("file-a body"), 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(pathB, []byte("file-b body"), 0o600); err != nil {
			return err
		}
		state.rehydrateSummary.FilesToRestore = []string{pathA, pathB}
		return nil
	})

	ctx.Step(`^the auto-compactor rehydrates$`, func() error {
		compactor := flowctx.NewAutoCompactor(nil)
		state.rehydrateMessages, state.rehydrateErr = compactor.Rehydrate(state.rehydrateSummary)
		return nil
	})

	ctx.Step(`^the rehydrated window leads with a system message carrying the intent$`, func() error {
		if state.rehydrateErr != nil {
			return fmt.Errorf("rehydrate error: %w", state.rehydrateErr)
		}
		if len(state.rehydrateMessages) == 0 {
			return errors.New("rehydrated window is empty")
		}
		if state.rehydrateMessages[0].Role != "system" {
			return fmt.Errorf("rehydrated[0].Role = %q; want system", state.rehydrateMessages[0].Role)
		}
		if !strings.Contains(state.rehydrateMessages[0].Content, state.rehydrateSummary.Intent) {
			return fmt.Errorf("rehydrated[0].Content = %q; want to contain intent %q",
				state.rehydrateMessages[0].Content, state.rehydrateSummary.Intent)
		}
		return nil
	})

	ctx.Step(`^the rehydrated window carries one tool message per queued file$`, func() error {
		if state.rehydrateErr != nil {
			return fmt.Errorf("rehydrate error: %w", state.rehydrateErr)
		}
		want := 1 + len(state.rehydrateSummary.FilesToRestore)
		if len(state.rehydrateMessages) != want {
			return fmt.Errorf("rehydrated len = %d; want %d", len(state.rehydrateMessages), want)
		}
		for i, m := range state.rehydrateMessages[1:] {
			if m.Role != "tool" {
				return fmt.Errorf("rehydrated[%d].Role = %q; want tool", i+1, m.Role)
			}
			if m.Content == "" {
				return fmt.Errorf("rehydrated[%d].Content is empty", i+1)
			}
		}
		return nil
	})
}

// bddSummaryJSON builds a CompactionSummary JSON body for BDD scenarios.
// It starts from a minimal stub so individual steps can override just
// the fields they care about.
//
// Expected:
//   - override may be nil (returns the stub as-is) or a function that
//     mutates the summary in-place before marshalling.
//
// Returns:
//   - The JSON payload on success.
//   - The marshalling error on failure (vanishingly unlikely in practice,
//     kept visible so a misuse surfaces rather than silently corrupting
//     a scenario).
//
// Side effects:
//   - None.
func bddSummaryJSON(override func(*flowctx.CompactionSummary)) (string, error) {
	summary := flowctx.CompactionSummary{
		Intent:         "stub intent",
		KeyDecisions:   []string{},
		Errors:         []string{},
		NextSteps:      []string{"stub next step"},
		FilesToRestore: []string{},
	}
	if override != nil {
		override(&summary)
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return "", fmt.Errorf("marshal bdd summary: %w", err)
	}
	return string(data), nil
}
