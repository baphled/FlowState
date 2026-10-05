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
)

// phase3State carries per-scenario state for the @compaction-phase3
// scenarios. The Before hook zeroes it so state never leaks.
type phase3State struct {
	eng        *engine.Engine
	summariser *phase3Summariser
	store      *recall.FileContextStore
	tempDir    string

	forceTrigger string
	result       string

	tailCount   int
	toolStubs   int
	stubCost    int
	usable      int
	summaryText string

	structuralPlans []string
	modifiedFiles   []string
	coordKeys       []string

	messages []provider.Message
	rebuilt  []provider.Message
	rebuildE error

	promptText string
}

// phase3Summariser is a re-scriptable flowctx.Summariser double: the
// scenario can swap the canned response or the canned error between
// calls, and the call counter backs the "invoked again" assertions.
type phase3Summariser struct {
	mu       atomic.Int32
	response string
	err      error
}

func (s *phase3Summariser) Summarise(_ context.Context, _ string, _ string, _ []provider.Message) (string, error) {
	s.mu.Add(1)
	if s.err != nil {
		return "", s.err
	}
	return s.response, nil
}

func (s *phase3Summariser) calls() int     { return int(s.mu.Load()) }
func (s *phase3Summariser) setErr(e error) { s.err = e }

// phase3SummaryJSON renders a valid CompactionSummary payload for the
// double, differing per scenario via the intent string so the fresh and
// memoised summaries are distinguishable.
func phase3SummaryJSON(intent string) string {
	return fmt.Sprintf(
		`{"intent":%q,"key_decisions":["d"],"errors":["e"],"next_steps":["n"],"files_to_restore":[],"original_token_count":0,"summary_token_count":0}`,
		intent,
	)
}

// phase3Counter is a one-token-per-word counter with a configurable
// model limit, mirroring the other BDD counters.
type phase3Counter struct{ limit int }

func (c phase3Counter) Count(text string) int {
	if text == "" {
		return 0
	}
	return len(strings.Fields(text))
}

func (c phase3Counter) ModelLimit(string) int { return c.limit }

// wirePhase3Engine builds the Phase 3 engine: a scripted summariser
// behind the AutoCompactor, a temp context store, auto-compaction
// enabled, and a token counter whose limit the scenario controls.
func (s *phase3State) wireEngine(limit int) error {
	s.summariser = &phase3Summariser{response: phase3SummaryJSON("first summary")}
	store, err := recall.NewFileContextStore(filepath.Join(s.tempDir, "ctx.json"), "test-model")
	if err != nil {
		return fmt.Errorf("new file context store: %w", err)
	}
	s.store = store

	cfg := flowctx.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = true
	cfg.AutoCompaction.Threshold = 0.50

	cm := agentpkg.DefaultContextManagement()
	cm.CompactionThreshold = 0
	cm.SlidingWindowSize = 50

	s.eng = engine.New(engine.Config{
		Manifest: agentpkg.Manifest{
			ID:                "phase3-agent",
			Instructions:      agentpkg.Instructions{SystemPrompt: "sys"},
			ContextManagement: cm,
		},
		Store:                 store,
		TokenCounter:          phase3Counter{limit: limit},
		AutoCompactor:         flowctx.NewAutoCompactor(s.summariser),
		CompressionConfig:     cfg,
		OutputReserveForTests: 1,
	})
	return nil
}

// RegisterPhase3Steps wires the @compaction-phase3 scenarios to the
// production compaction and rebuild paths.
func RegisterPhase3RehydrationSteps(ctx *godog.ScenarioContext) {
	state := &phase3State{}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		dir, err := os.MkdirTemp("", "phase3-bdd-*")
		if err != nil {
			return c, fmt.Errorf("temp dir: %w", err)
		}
		*state = phase3State{tempDir: dir}
		return c, nil
	})

	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if state.tempDir != "" {
			_ = os.RemoveAll(state.tempDir)
		}
		return c, nil
	})

	ctx.Step(`^a phase3 engine with auto-compaction enabled and a scripted summariser$`, func() error {
		return state.wireEngine(20)
	})

	ctx.Step(`^the session has compacted once with a cold range of (\d+) messages$`, func(n int) error {
		for i := 0; i < n; i++ {
			state.store.Append(provider.Message{Role: "user", Content: fmt.Sprintf("cold message %d with padding words here to raise the count", i)})
		}
		result, fired := state.eng.CompactNow(context.Background(), "phase3-session")
		if !fired || result == "" {
			return errors.New("initial compaction did not fire")
		}
		state.result = result
		if state.summariser.calls() != 1 {
			return fmt.Errorf("initial compaction calls = %d, want 1", state.summariser.calls())
		}
		return nil
	})

	ctx.Step(`^the summariser is re-scripted to return a different summary$`, func() error {
		state.summariser.response = phase3SummaryJSON("fresh summary")
		return nil
	})

	ctx.Step(`^the summariser fails on the next call$`, func() error {
		state.summariser.setErr(errors.New("chain failed"))
		return nil
	})

	ctx.Step(`^a forced compaction fires on the same cold range$`, func() error {
		state.forceTrigger = "forced"
		result, fired := state.eng.CompactNow(context.Background(), "phase3-session")
		if !fired {
			return errors.New("forced compaction did not fire")
		}
		state.result = result
		return nil
	})

	ctx.Step(`^a non-forced ratio compaction fires on the same cold range$`, func() error {
		state.forceTrigger = ""
		msgs := state.store.AllMessages()
		if len(msgs) == 0 {
			return errors.New("no messages in store for non-forced fire")
		}
		state.result = state.eng.MaybeAutoCompactExplicitForTesting(context.Background(), "phase3-session", "", msgs)
		return nil
	})

	ctx.Step(`^the summariser is invoked again after the re-scripting$`, func() error {
		if got := state.summariser.calls(); got < 2 {
			return fmt.Errorf("summariser calls = %d, want >= 2", got)
		}
		return nil
	})

	ctx.Step(`^the summariser is not invoked again$`, func() error {
		if got := state.summariser.calls(); got != 1 {
			return fmt.Errorf("summariser calls = %d, want 1", got)
		}
		return nil
	})

	ctx.Step(`^the returned summary is the fresh summary$`, func() error {
		if !strings.Contains(state.result, "fresh summary") {
			return fmt.Errorf("result does not carry the fresh summary: %q", state.result)
		}
		return nil
	})

	ctx.Step(`^the returned summary is the memoised summary$`, func() error {
		if !strings.Contains(state.result, "first summary") {
			return fmt.Errorf("result does not carry the memoised summary: %q", state.result)
		}
		return nil
	})

	ctx.Step(`^the memo for the session is invalidated$`, func() error {
		if state.eng.PriorCompactionSummaryForTesting("phase3-session") != nil {
			return errors.New("memo entry still present after chain failure")
		}
		return nil
	})

	ctx.Step(`^the returned summary is the truncation fallback summary$`, func() error {
		if !strings.Contains(state.result, "[truncation fallback:") {
			return fmt.Errorf("result is not the truncation fallback: %q", state.result)
		}
		return nil
	})

	ctx.Step(`^a phase3 engine with structural context carrying an active plan, (\d+) modified files, and (\d+) coordination keys$`,
		func(files, keys int) error {
			if err := state.wireEngine(0); err != nil {
				return err
			}
			state.structuralPlans = []string{"phase3-plan: implement rehydration"}
			state.modifiedFiles = make([]string, files)
			for i := range state.modifiedFiles {
				state.modifiedFiles[i] = fmt.Sprintf("internal/pkg/file%d.go", i)
			}
			state.coordKeys = make([]string, keys)
			for i := range state.coordKeys {
				state.coordKeys[i] = fmt.Sprintf("phase3/key%d", i)
			}
			return nil
		})

	ctx.Step(`^a phase3 engine with a (\d+)-token usable budget and structural context carrying an active plan, (\d+) modified files, and (\d+) coordination keys$`,
		func(usable, files, keys int) error {
			if err := state.wireEngine(usable); err != nil {
				return err
			}
			state.usable = usable
			state.structuralPlans = []string{"phase3-plan: budget trim"}
			state.modifiedFiles = make([]string, files)
			state.coordKeys = make([]string, keys)
			return nil
		})

	ctx.Step(`^a compaction summary and a verbatim tail of (\d+) messages$`, func(n int) error {
		state.summaryText = "[auto-compacted summary]: " + phase3SummaryJSON("rehydrate summary")
		state.tailCount = n
		state.messages = make([]provider.Message, 0, n)
		for i := 0; i < n; i++ {
			state.messages = append(state.messages, provider.Message{Role: "user", Content: fmt.Sprintf("tail message %d", i)})
		}
		return nil
	})

	ctx.Step(`^a message slice with (\d+) compactable tool results and (\d+) recent user turns$`, func(tools, users int) error {
		state.summaryText = "[auto-compacted summary]: " + phase3SummaryJSON("stub summary")
		state.tailCount = users
		state.messages = nil
		for i := 0; i < tools; i++ {
			toolMsg := provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{
				ID:   fmt.Sprintf("call_compact_%d", i),
				Name: "read",
			}}}
			state.messages = append(state.messages, toolMsg)
			state.messages = append(state.messages, provider.Message{
				Role:    "tool",
				Content: fmt.Sprintf("line one of the read output %d\nline two of the read output %d\nline three of the read output %d\nline four", i, i, i),
				ToolCalls: []provider.ToolCall{{
					ID:   fmt.Sprintf("call_compact_%d", i),
					Name: "read",
				}},
			})
		}
		state.messages = append(state.messages, provider.Message{
			Role:      "assistant",
			ToolCalls: []provider.ToolCall{{ID: "call_keep_0", Name: "coordination_store"}},
		})
		state.messages = append(state.messages, provider.Message{
			Role:      "tool",
			Content:   "coordination_store result kept verbatim because it is non-compactable",
			ToolCalls: []provider.ToolCall{{ID: "call_keep_0", Name: "coordination_store"}},
		})
		for i := 0; i < users; i++ {
			state.messages = append(state.messages, provider.Message{Role: "user", Content: fmt.Sprintf("recent user turn %d", i)})
		}
		return nil
	})

	ctx.Step(`^a message slice with (\d+) stubbed tool results each costing (\d+) tokens followed by a user turn$`,
		func(stubs, cost int) error {
			state.summaryText = "[auto-compacted summary]: " + phase3SummaryJSON("trim summary")
			state.toolStubs = stubs
			state.stubCost = cost
			state.messages = nil
			for i := 0; i < stubs; i++ {
				toolMsg := provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{
					ID:   fmt.Sprintf("call_stub_%d", i),
					Name: "read",
				}}}
				state.messages = append(state.messages, toolMsg)
				content := strings.Repeat(fmt.Sprintf("word%d ", i), cost)
				state.messages = append(state.messages, provider.Message{
					Role:    "tool",
					Content: content,
					ToolCalls: []provider.ToolCall{{
						ID:   fmt.Sprintf("call_stub_%d", i),
						Name: "read",
					}},
				})
			}
			state.messages = append(state.messages, provider.Message{Role: "user", Content: "newest user turn"})
			return nil
		})

	ctx.Step(`^the rehydrated window is rebuilt$`, func() error {
		state.rebuilt, state.rebuildE = state.eng.RebuildRehydratedForTesting(
			context.Background(),
			"phase3-session",
			state.messages,
			state.summaryText,
			state.structuralPlans,
			state.modifiedFiles,
			state.coordKeys,
		)
		return nil
	})

	ctx.Step(`^the rebuilt window contains the active plan block$`, func() error {
		if state.rebuildE != nil {
			return state.rebuildE
		}
		for _, m := range state.rebuilt {
			if strings.Contains(m.Content, "phase3-plan") && strings.Contains(m.Content, "Active plan") {
				return nil
			}
		}
		return errors.New("rebuilt window lacks the active plan block")
	})

	ctx.Step(`^the rebuilt window contains the (\d+) modified file paths$`, func(n int) error {
		if state.rebuildE != nil {
			return state.rebuildE
		}
		for i := 0; i < n; i++ {
			want := fmt.Sprintf("internal/pkg/file%d.go", i)
			found := false
			for _, m := range state.rebuilt {
				if strings.Contains(m.Content, want) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("rebuilt window lacks modified file path %q", want)
			}
		}
		return nil
	})

	ctx.Step(`^the rebuilt window contains the (\d+) coordination keys$`, func(n int) error {
		if state.rebuildE != nil {
			return state.rebuildE
		}
		for i := 0; i < n; i++ {
			want := fmt.Sprintf("phase3/key%d", i)
			found := false
			for _, m := range state.rebuilt {
				if strings.Contains(m.Content, want) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("rebuilt window lacks coordination key %q", want)
			}
		}
		return nil
	})

	ctx.Step(`^the rebuilt window contains the compaction summary$`, func() error {
		if state.rebuildE != nil {
			return state.rebuildE
		}
		for _, m := range state.rebuilt {
			if strings.Contains(m.Content, "auto-compacted summary") {
				return nil
			}
		}
		return errors.New("rebuilt window lacks the compaction summary")
	})

	ctx.Step(`^the rebuilt window retains the (\d+) verbatim tail messages$`, func(n int) error {
		if state.rebuildE != nil {
			return state.rebuildE
		}
		for i := 0; i < n; i++ {
			want := fmt.Sprintf("tail message %d", i)
			found := false
			for _, m := range state.rebuilt {
				if strings.Contains(m.Content, want) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("rebuilt window lacks verbatim tail message %q", want)
			}
		}
		return nil
	})

	ctx.Step(`^the rebuilt window contains one-line stubs for the compacted tool calls$`, func() error {
		if state.rebuildE != nil {
			return state.rebuildE
		}
		for i := 0; i < 2; i++ {
			want := fmt.Sprintf("[tool read #call_compact_%d", i)
			found := false
			for _, m := range state.rebuilt {
				if strings.Contains(m.Content, want) && !strings.Contains(m.Content, "line two of the read output") {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("rebuilt window lacks stub for compacted tool call %d", i)
			}
		}
		return nil
	})

	ctx.Step(`^the non-compactable tool calls are retained verbatim$`, func() error {
		if state.rebuildE != nil {
			return state.rebuildE
		}
		for _, m := range state.rebuilt {
			if strings.Contains(m.Content, "coordination_store result kept verbatim") {
				return nil
			}
		}
		return errors.New("rebuilt window dropped the non-compactable tool result")
	})

	ctx.Step(`^the rehydrated window fits the usable budget$`, func() error {
		if state.rebuildE != nil {
			return state.rebuildE
		}
		total := 0
		for _, m := range state.rebuilt {
			total += len(strings.Fields(m.Content))
		}
		if state.usable > 0 && total > state.usable {
			return fmt.Errorf("rebuilt window cost %d tokens exceeds usable budget %d", total, state.usable)
		}
		return nil
	})

	ctx.Step(`^the rebuilt window retains the newest user turn$`, func() error {
		if state.rebuildE != nil {
			return state.rebuildE
		}
		for _, m := range state.rebuilt {
			if strings.Contains(m.Content, "newest user turn") {
				return nil
			}
		}
		return errors.New("rebuilt window dropped the newest user turn")
	})

	ctx.Step(`^the summary prompt is rendered for (\d+) messages$`, func(n int) error {
		msgs := make([]provider.Message, n)
		for i := range msgs {
			msgs[i] = provider.Message{Role: "user", Content: "message"}
		}
		text, err := flowctx.RenderSummaryPrompt(msgs)
		if err != nil {
			return err
		}
		state.promptText = text
		return nil
	})

	ctx.Step(`^the prompt names the keep-list sections "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", and "([^"]*)"$`,
		func(a, b, c, d, e string) error {
			for _, section := range []string{a, b, c, d, e} {
				if !strings.Contains(state.promptText, section) {
					return fmt.Errorf("prompt lacks keep-list section %q", section)
				}
			}
			return nil
		})

	// Steps carried over from the summariser-chain Phase-3 work for the
	// token-budget trimming scenario in phase3_rehydration.feature.
	ctx.Step(`^a compaction summary$`, func() error {
		state.summaryText = "[auto-compacted summary]: " + phase3SummaryJSON("trim summary")
		return nil
	})

	ctx.Step(`^a transcript with several old tool calls and results$`, func() error {
		state.messages = append(state.messages, provider.Message{
			Role:    "assistant",
			Content: "calling read on target",
			ToolCalls: []provider.ToolCall{{
				ID:        "call-1",
				Name:      "read",
				Arguments: map[string]any{"target": "phase3/target"},
			}},
		})
		state.messages = append(state.messages, provider.Message{Role: "tool", Content: "first tool result body here"})
		for i := range 6 {
			state.messages = append(state.messages,
				provider.Message{Role: "assistant", Content: fmt.Sprintf("assistant batch %d", i), ToolCalls: []provider.ToolCall{{
					ID:        fmt.Sprintf("call-%d", i+2),
					Name:      "read",
					Arguments: map[string]any{"target": fmt.Sprintf("file%d.go", i)},
				}}},
				provider.Message{Role: "tool", Content: fmt.Sprintf("tool result %d with plenty of body words to cost tokens", i)},
			)
		}
		state.messages = append(state.messages, provider.Message{Role: "assistant", Content: "the newest live message"})
		return nil
	})

	ctx.Step(`^the newest live message is retained$`, func() error {
		if len(state.rebuilt) == 0 {
			return errors.New("rebuilt window is empty")
		}
		last := state.rebuilt[len(state.rebuilt)-1]
		if !strings.Contains(last.Content, "newest live message") {
			return errors.New("newest live message not retained: " + last.Content)
		}
		return nil
	})

	ctx.Step(`^the oldest stubs are trimmed first$`, func() error {
		foundNewest := false
		for i := len(state.rebuilt) - 1; i >= 0; i-- {
			if strings.Contains(state.rebuilt[i].Content, "newest live message") {
				foundNewest = true
			}
		}
		if !foundNewest {
			return errors.New("newest live message missing from trimmed window")
		}
		for _, m := range state.rebuilt {
			if m.Role == "tool" && strings.Contains(m.Content, "first tool result body here") {
				return errors.New("oldest tool result survived trimming")
			}
		}
		return nil
	})

	ctx.Step(`^an engine with a rehydrating explicit-path rebuild$`, func() error {
		if err := state.wireEngine(1000); err != nil {
			return err
		}
		state.messages = []provider.Message{{Role: "user", Content: "live tail message"}}
		return nil
	})

	ctx.Step(`^the post-compaction window is rebuilt with structural context$`, func() error {
		state.rebuilt = state.eng.RebuildExplicitPathForTesting(
			context.Background(), "phase3-session", state.messages, state.summaryText)
		if state.rebuilt == nil {
			return errors.New("explicit-path rebuild returned nil")
		}
		return nil
	})

	ctx.Step(`^the rebuilt window fits the usable budget after stub trimming$`, func() error {
		if state.eng == nil || state.rebuilt == nil {
			return errors.New("rebuilt window is nil")
		}
		if state.eng.ContextEstimateOverBudgetForTesting(context.Background(), state.rebuilt) {
			return errors.New("rebuilt window still over budget")
		}
		return nil
	})

	// Stubbing and structural-context assertions carried over from the
	// summariser-chain Phase-3 step set, adapted to phase3State.
	ctx.Step(`^a transcript with an old compactable tool call and result$`, func() error {
		state.messages = []provider.Message{{Role: "user", Content: "do the thing"},
			{Role: "assistant", Content: "calling read on target", ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "read", Arguments: map[string]any{"target": "phase3/target"}}}},
			{Role: "tool", Content: "line one\nline two\nline three\nline four"},
			{Role: "assistant", Content: "done with the tool"}}
		return nil
	})

	ctx.Step(`^a transcript with a coordination_store tool call and a small result$`, func() error {
		state.messages = []provider.Message{{Role: "user", Content: "do the thing"},
			{Role: "assistant", Content: "calling coordination_store", ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "coordination_store", Arguments: map[string]any{"target": "phase3/target"}}}},
			{Role: "tool", Content: "stored key compaction/phase3/x"},
			{Role: "assistant", Content: "done with the tool"}}
		return nil
	})

	ctx.Step(`^a transcript with a delegate tool call whose result exceeds the cap$`, func() error {
		state.messages = []provider.Message{{Role: "user", Content: "do the thing"},
			{Role: "assistant", Content: "calling delegate", ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "delegate", Arguments: map[string]any{"target": "phase3/target"}}}},
			{Role: "tool", Content: strings.Repeat("payload ", 4000)},
			{Role: "assistant", Content: "done with the tool"}}
		return nil
	})

	ctx.Step(`^the post-compaction window is rebuilt with tool stubbing$`, func() error {
		state.rebuilt = state.eng.StubToolCallsForTesting(state.messages)
		return nil
	})

	ctx.Step(`^the old tool result is replaced by a one-line stub$`, func() error {
		for _, m := range state.rebuilt {
			if m.Role != "tool" {
				continue
			}
			if strings.Count(strings.TrimSpace(m.Content), "\n") != 0 {
				return errors.New("tool result not a one-line stub: " + m.Content)
			}
			return nil
		}
		return errors.New("no tool result found in rebuilt window")
	})

	ctx.Step(`^an active plan with plan text$`, func() error {
		state.structuralPlans = []string{"phase3 plan: implement rehydration"}
		state.eng.SetActivePlanForTesting("phase3-session", "phase3 plan: implement rehydration")
		return nil
	})

	ctx.Step(`^recently modified files recorded for the session$`, func() error {
		state.modifiedFiles = []string{"internal/engine/compaction.go", "internal/engine/engine.go"}
		state.eng.SetSessionFilesForTesting("phase3-session", state.modifiedFiles, "modified")
		return nil
	})

	ctx.Step(`^recently modified files recorded for the session whose full status exceeds (\d+) tokens$`, func(capped int) error {
		state.modifiedFiles = []string{"internal/engine/compaction.go", "internal/engine/engine.go"}
		state.eng.SetSessionFilesForTesting("phase3-session", state.modifiedFiles, strings.Repeat("detail ", capped))
		return nil
	})

	ctx.Step(`^coordination keys recorded for the session$`, func() error {
		state.coordKeys = []string{"bug-hunt/report", "compaction/phase3/result"}
		state.eng.SetSessionCoordinationKeysForTesting("phase3-session", state.coordKeys)
		return nil
	})

	ctx.Step(`^the rebuilt window lists the recently modified file paths with brief status$`, func() error {
		if err := windowContains(state.rebuilt, "internal/engine/compaction.go"); err != nil {
			return err
		}
		return windowContains(state.rebuilt, "modified")
	})

	ctx.Step(`^the rebuilt window contains the active plan text$`, func() error {
		return windowContains(state.rebuilt, "phase3 plan: implement rehydration")
	})

	ctx.Step(`^the stub names the tool, its target and its outcome$`, func() error {
		for _, m := range state.rebuilt {
			if m.Role != "tool" {
				continue
			}
			if !strings.Contains(m.Content, "read") || !strings.Contains(m.Content, "phase3/target") {
				return errors.New("stub missing tool or target: " + m.Content)
			}
			if !strings.Contains(m.Content, "outcome") {
				return errors.New("stub missing outcome field: " + m.Content)
			}
			return nil
		}
		return errors.New("no tool result found in rebuilt window")
	})

	ctx.Step(`^the coordination_store tool result is retained verbatim$`, func() error {
		for _, m := range state.rebuilt {
			if m.Role != "tool" {
				continue
			}
			if !strings.Contains(m.Content, "stored key compaction/phase3/x") {
				return errors.New("coordination_store result not retained verbatim: " + m.Content)
			}
			return nil
		}
		return errors.New("no tool result found in rebuilt window")
	})

	ctx.Step(`^the delegate tool result is replaced by a stub with a key reference$`, func() error {
		for _, m := range state.rebuilt {
			if m.Role != "tool" {
				continue
			}
			if !strings.Contains(m.Content, "coordination key") {
				return errors.New("delegate stub missing key reference: " + m.Content)
			}
			return nil
		}
		return errors.New("no tool result found in rebuilt window")
	})

	ctx.Step(`^the rebuilt window lists the recently modified file paths only$`, func() error {
		if err := windowContains(state.rebuilt, "internal/engine/compaction.go"); err != nil {
			return err
		}
		for _, m := range state.rebuilt {
			if m.Role != "assistant" {
				continue
			}
			if strings.Contains(m.Content, "detail") && strings.Contains(m.Content, "compaction.go") {
				return errors.New("full file status leaked into rebuilt window above the token cap")
			}
		}
		return nil
	})

	ctx.Step(`^the rebuilt window lists the coordination keys written this session$`, func() error {
		return windowContains(state.rebuilt, "bug-hunt/report")
	})
}

// windowContains reports whether any message in msgs contains want.
func windowContains(msgs []provider.Message, want string) error {
	if want == "" {
		return nil
	}
	for _, m := range msgs {
		if strings.Contains(m.Content, want) {
			return nil
		}
	}
	return errors.New("rebuilt window does not contain: " + want)
}
