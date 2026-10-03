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
func RegisterPhase3Steps(ctx *godog.ScenarioContext) {
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

	ctx.Step(`^the summariser is invoked again$`, func() error {
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
}
