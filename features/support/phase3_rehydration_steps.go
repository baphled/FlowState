//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

// phase3RehydrationState carries per-scenario state for the Phase 3
// explicit-path rehydration scenarios.
type phase3RehydrationState struct {
	engine        *engine.Engine
	ctx           context.Context
	messages      []provider.Message
	summary       string
	planText      string
	files         []string
	fileStatus    string
	coordKeys     []string
	rebuilt       []provider.Message
	trimThreshold int
}

// phase3Counter counts one token per word like the hot-tail counter, with
// a configurable model limit.
type phase3Counter struct{ limit int }

func (c phase3Counter) Count(text string) int { return len(strings.Fields(text)) }
func (c phase3Counter) ModelLimit(string) int { return c.limit }

// makeToolTranscript builds an alternating assistant-tool transcript whose
// tool names ride on the preceding assistant message's ToolCalls.
func (s *phase3RehydrationState) makeToolTranscript(toolName, result string, resultTokens int) {
	s.messages = nil
	s.messages = append(s.messages, provider.Message{Role: "user", Content: "do the thing"})
	s.messages = append(s.messages, provider.Message{
		Role:    "assistant",
		Content: fmt.Sprintf("calling %s on target", toolName),
		ToolCalls: []provider.ToolCall{{
			ID:   "call-1",
			Name: toolName,
			Arguments: map[string]any{
				"target": "phase3/target",
			},
		}},
	})
	content := result
	if resultTokens > 0 {
		content = strings.Repeat("payload ", resultTokens)
	}
	s.messages = append(s.messages, provider.Message{Role: "tool", Content: content})
	s.messages = append(s.messages, provider.Message{Role: "assistant", Content: "done with the tool"})
}

// wirePhase3Engine builds an engine with a word counter over a generous
// budget so the token-budget scenario trims against a small override.
func (s *phase3RehydrationState) wirePhase3Engine(limit int) {
	s.engine = engine.New(engine.Config{
		TokenCounter:          phase3Counter{limit: limit},
		OutputReserveForTests: 16,
	})
}

// RegisterPhase3RehydrationSteps wires the Phase 3 explicit-path
// rehydration scenarios to the production rebuild helpers.
func RegisterPhase3RehydrationSteps(ctx *godog.ScenarioContext) {
	state := &phase3RehydrationState{}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		state.engine = nil
		state.ctx = c
		state.messages = nil
		state.summary = ""
		state.planText = ""
		state.files = nil
		state.fileStatus = "modified"
		state.coordKeys = nil
		state.rebuilt = nil
		state.trimThreshold = 0
		return c, nil
	})

	ctx.Step(`^an engine with a rehydrating explicit-path rebuild$`, func() error {
		state.wirePhase3Engine(100_000)
		state.messages = []provider.Message{{Role: "user", Content: "live tail message"}}
		return nil
	})

	ctx.Step(`^a transcript with an old compactable tool call and result$`, func() error {
		state.makeToolTranscript("read", "line one\nline two\nline three\nline four", 0)
		return nil
	})

	ctx.Step(`^a transcript with a coordination_store tool call and a small result$`, func() error {
		state.makeToolTranscript("coordination_store", "stored key compaction/phase3/x", 0)
		return nil
	})

	ctx.Step(`^a transcript with a delegate tool call whose result exceeds the cap$`, func() error {
		state.makeToolTranscript("delegate", "", 4_000)
		return nil
	})

	ctx.Step(`^the post-compaction window is rebuilt with tool stubbing$`, func() error {
		state.rebuilt = state.engine.StubToolCallsForTesting(state.messages)
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

	ctx.Step(`^an active plan with plan text$`, func() error {
		state.planText = "phase3 plan: implement rehydration"
		state.engine.SetActivePlanForTesting("alpha", state.planText)
		return nil
	})

	ctx.Step(`^recently modified files recorded for the session$`, func() error {
		state.files = []string{"internal/engine/compaction.go", "internal/engine/engine.go"}
		state.fileStatus = "modified"
		state.engine.SetSessionFilesForTesting("alpha", state.files, state.fileStatus)
		return nil
	})

	ctx.Step(`^recently modified files recorded for the session whose full status exceeds (\d+) tokens$`, func(capped int) error {
		state.files = []string{"internal/engine/compaction.go", "internal/engine/engine.go"}
		state.fileStatus = strings.Repeat("detail ", capped)
		state.engine.SetSessionFilesForTesting("alpha", state.files, state.fileStatus)
		return nil
	})

	ctx.Step(`^coordination keys recorded for the session$`, func() error {
		state.coordKeys = []string{"bug-hunt/report", "compaction/phase3/result"}
		state.engine.SetSessionCoordinationKeysForTesting("alpha", state.coordKeys)
		return nil
	})

	ctx.Step(`^a compaction summary$`, func() error {
		state.summary = strings.Repeat("summary ", 10)
		return nil
	})

	ctx.Step(`^a transcript with several old tool calls and results$`, func() error {
		state.makeToolTranscript("read", "first tool result body here", 0)
		more := make([]provider.Message, 0, 16)
		for i := range 6 {
			more = append(more,
				provider.Message{Role: "assistant", Content: fmt.Sprintf("assistant batch %d", i), ToolCalls: []provider.ToolCall{{
					ID:        fmt.Sprintf("call-%d", i+2),
					Name:      "read",
					Arguments: map[string]any{"target": fmt.Sprintf("file%d.go", i)},
				}}},
				provider.Message{Role: "tool", Content: fmt.Sprintf("tool result %d with plenty of body words to cost tokens", i)},
			)
		}
		state.messages = append(state.messages, more...)
		state.messages = append(state.messages, provider.Message{Role: "assistant", Content: "the newest live message"})
		return nil
	})

	ctx.Step(`^the post-compaction window is rebuilt with structural context$`, func() error {
		sess := "alpha"
		if state.planText != "" {
			state.engine.SetActivePlanForTesting(sess, state.planText)
		}
		if len(state.files) > 0 {
			state.engine.SetSessionFilesForTesting(sess, state.files, state.fileStatus)
		}
		if len(state.coordKeys) > 0 {
			state.engine.SetSessionCoordinationKeysForTesting(sess, state.coordKeys)
		}
		state.rebuilt = state.engine.RebuildExplicitPathForTesting(state.ctx, sess, state.messages, state.summary)
		if state.rebuilt == nil {
			return errors.New("explicit-path rebuild returned nil")
		}
		return nil
	})

	ctx.Step(`^the rebuilt window contains the active plan text$`, func() error {
		return windowContains(state.rebuilt, state.planText)
	})

	ctx.Step(`^the rebuilt window lists the recently modified file paths with brief status$`, func() error {
		if err := windowContains(state.rebuilt, "internal/engine/compaction.go"); err != nil {
			return err
		}
		return windowContains(state.rebuilt, "modified")
	})

	ctx.Step(`^the rebuilt window lists the recently modified file paths only$`, func() error {
		if err := windowContains(state.rebuilt, "internal/engine/compaction.go"); err != nil {
			return err
		}
		for _, m := range state.rebuilt {
			if strings.Contains(m.Content, state.fileStatus) {
				return errors.New("full file status leaked into rebuilt window above the token cap")
			}
		}
		return nil
	})

	ctx.Step(`^the rebuilt window lists the coordination keys written this session$`, func() error {
		return windowContains(state.rebuilt, "bug-hunt/report")
	})

	ctx.Step(`^the rebuilt window fits the usable budget after stub trimming$`, func() error {
		if state.engine == nil || state.rebuilt == nil {
			return errors.New("rebuilt window is nil")
		}
		if state.engine.ContextEstimateOverBudgetForTesting(state.ctx, state.rebuilt) {
			return errors.New("rebuilt window still over budget")
		}
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
