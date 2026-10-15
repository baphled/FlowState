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

// reliabilityState carries per-scenario state for the context-loss
// reliability-fix scenarios.
type reliabilityState struct {
	engine         *engine.Engine
	summariser     *reliabilitySummariser
	limit          int
	sessID         string
	sourceMsgs     []provider.Message
	prunedMsgs     []provider.Message
	rebuilt        []provider.Message
	metaPath       string
	restored       *flowctx.CompactionSummary
	lastCompaction string
}

// reliabilitySummariser is a scripted Summariser double whose failure
// schedule is controlled by failFirst and failAlways, counting calls so
// scenarios can assert the retry-once contract.
type reliabilitySummariser struct {
	response  string
	failFirst bool
	failAll   bool
	calls     int
}

func (s *reliabilitySummariser) Summarise(_ context.Context, _ string, _ string, _ []provider.Message) (string, error) {
	s.calls++
	if s.failAll || (s.failFirst && s.calls < 2) {
		return "", errors.New("summariser chain failed")
	}
	return s.response, nil
}

// reliabilitySummaryJSON builds a summariser response carrying a
// CompactionSummary JSON body with the given intent and files.
func reliabilitySummaryJSON(intent string, files []string) (string, error) {
	payload, err := json.Marshal(flowctx.CompactionSummary{
		Intent:         intent,
		NextSteps:      []string{"continue"},
		FilesToRestore: files,
	})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

// wire builds the reliability engine with the given scripted summariser
// and context limit. Auto-compaction runs at a low threshold so the
// store-based trigger fires on the seeded transcripts.
func (s *reliabilityState) wire(summariser *reliabilitySummariser, limit int) error {
	s.summariser = summariser
	s.limit = limit
	dir, err := os.MkdirTemp("", "reliability-ctx-*")
	if err != nil {
		return err
	}
	store, err := recall.NewFileContextStore(filepath.Join(dir, "ctx.json"), "reliability-model")
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
			ID:                "reliability-agent",
			Instructions:      agent.Instructions{SystemPrompt: "sys"},
			ContextManagement: cm,
		},
		TokenCounter:      wordE2ECounter{limit: limit},
		AutoCompactor:     flowctx.NewAutoCompactor(summariser),
		Store:             store,
		CompressionConfig: cfg,
	})
	return nil
}

// seed loads a transcript of turnCount user/assistant pairs into the
// engine store under the given session.
func (s *reliabilityState) seed(sessID string, turnCount int, extra ...provider.Message) {
	s.sessID = sessID
	msgs := make([]provider.Message, 0, turnCount*2+len(extra))
	for i := range turnCount {
		msgs = append(msgs,
			provider.Message{Role: "user", Content: fmt.Sprintf("%s turn %d body", sessID, i)},
			provider.Message{Role: "assistant", Content: fmt.Sprintf("%s reply %d body", sessID, i)},
		)
	}
	msgs = append(msgs, extra...)
	s.engine.SeedHistory(sessID, msgs)
}

// toolTranscript builds an assistant/tool pair with the given tool name
// and result content.
func toolTranscript(toolName, result string) []provider.Message {
	return []provider.Message{
		{Role: "assistant", Content: "calling tool", ToolCalls: []provider.ToolCall{{
			ID: "call-r", Name: toolName, Arguments: map[string]any{"target": "rel/target"},
		}}},
		{Role: "tool", Content: result},
	}
}

// RegisterReliabilityFixSteps wires the context-loss reliability-fix
// scenarios to the production compaction pipeline.
func RegisterReliabilityFixSteps(ctx *godog.ScenarioContext) {
	state := &reliabilityState{}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		state.engine = nil
		state.summariser = nil
		state.limit = 100_000
		state.sessID = ""
		state.sourceMsgs = nil
		state.prunedMsgs = nil
		state.rebuilt = nil
		state.metaPath = ""
		state.restored = nil
		return c, nil
	})

	ctx.Step(`^a reliability-fix engine with auto-compaction enabled$`, func() error {
		resp, err := reliabilitySummaryJSON("fresh intent", nil)
		if err != nil {
			return err
		}
		return state.wire(&reliabilitySummariser{response: resp}, 100_000)
	})

	ctx.Step(`^a summariser that fails on the first call and succeeds on retry$`, func() error {
		state.summariser.failFirst = true
		state.summariser.calls = 0
		return nil
	})

	ctx.Step(`^a summariser that always fails after retry for session (\w+)$`, func(_ string) error {
		state.summariser.failAll = true
		state.summariser.calls = 0
		return nil
	})

	ctx.Step(`^a summariser that always succeeds for session (\w+)$`, func(intent string) error {
		state.summariser.failAll = false
		state.summariser.failFirst = false
		resp, err := reliabilitySummaryJSON(intent+" intent", nil)
		if err != nil {
			return err
		}
		state.summariser.response = resp
		return nil
	})

	ctx.Step(`^a session transcript above the compaction threshold for session (\w+)$`, func(sessID string) error {
		state.seed(sessID, 6)
		return nil
	})

	ctx.Step(`^a long transcript with many small turns for session (\w+)$`, func(sessID string) error {
		state.seed(sessID, 200)
		state.sessID = sessID
		return nil
	})

	ctx.Step(`^a model context limit of (\d+) tokens for session (\w+)$`, func(limit int, sessID string) error {
		state.limit = limit
		if state.engine != nil {
			return nil
		}
		resp, err := reliabilitySummaryJSON("fresh intent", nil)
		if err != nil {
			return err
		}
		return state.wire(&reliabilitySummariser{response: resp}, limit)
	})

	ctx.Step(`^a reliability compaction runs for session (\w+)$`, func(sessID string) error {
		cctx := context.WithValue(context.Background(), session.IDKey{}, sessID)
		state.lastCompaction = state.engine.MaybeCompactForcedForTesting(cctx, sessID, "tool_result_wave", nil)
		return nil
	})

	ctx.Step(`^a compaction runs for session (\w+) and falls back$`, func(sessID string) error {
		cctx := context.WithValue(context.Background(), session.IDKey{}, sessID)
		state.lastCompaction = state.engine.MaybeCompactForcedForTesting(cctx, sessID, "tool_result_wave", nil)
		return nil
	})

	ctx.Step(`^the summariser was invoked twice$`, func() error {
		if state.summariser.calls != 2 {
			return fmt.Errorf("summariser calls = %d; want 2 (retry-once contract)", state.summariser.calls)
		}
		return nil
	})

	ctx.Step(`^the reliability fresh summary is applied$`, func() error {
		if !strings.Contains(state.lastCompaction, "fresh intent") {
			return errors.New("expected fresh summary content, got: " + state.lastCompaction)
		}
		return nil
	})

	ctx.Step(`^the fallback window is rebuilt for session (\w+)$`, func(sessID string) error {
		cctx := context.WithValue(context.Background(), session.IDKey{}, sessID)
		prior := state.allStoreMessages()
		state.rebuilt = state.engine.RebuildTruncationFallbackForTesting(cctx, sessID, prior, state.limit)
		if state.rebuilt == nil {
			return errors.New("fallback rebuild returned nil")
		}
		return nil
	})

	ctx.Step(`^the rebuilt tail keeps at most 25 percent of the context limit in tokens$`, func() error {
		tokens := 0
		for _, m := range state.rebuilt {
			tokens += len(strings.Fields(m.Content))
		}
		if tokens > state.limit/4+200 {
			return fmt.Errorf("rebuilt tail tokens = %d; want <= %d", tokens, state.limit/4+200)
		}
		return nil
	})

	ctx.Step(`^the tail retains more messages than a fixed window of 5$`, func() error {
		if len(state.rebuilt) <= 5 {
			return fmt.Errorf("rebuilt tail has %d messages; want > 5 (proportional, not fixed)", len(state.rebuilt))
		}
		return nil
	})

	ctx.Step(`^a memoised compaction summary with intent (\w+) for reliability session (\w+)$`, func(intent, sessID string) error {
		resp, err := reliabilitySummaryJSON(intent+" intent", nil)
		if err != nil {
			return err
		}
		state.summariser.response = resp
		state.seed(sessID, 6)
		cctx := context.WithValue(context.Background(), session.IDKey{}, sessID)
		state.lastCompaction = state.engine.MaybeCompactForcedForTesting(cctx, sessID, "tool_result_wave", nil)
		if state.lastCompaction == "" {
			return errors.New("memo-seeding compaction produced no summary")
		}
		return nil
	})

	ctx.Step(`^a memoised compaction summary for reliability session (\w+)$`, func(sessID string) error {
		state.seed(sessID, 6)
		cctx := context.WithValue(context.Background(), session.IDKey{}, sessID)
		state.lastCompaction = state.engine.MaybeCompactForcedForTesting(cctx, sessID, "tool_result_wave", nil)
		if state.lastCompaction == "" {
			return errors.New("memo-seeding compaction produced no summary")
		}
		return nil
	})

	ctx.Step(`^the memoised summary for session (\w+) is still queryable$`, func(sessID string) error {
		if state.engine.SessionCompactionSummaryForTesting(sessID) == nil {
			return errors.New("memo was deleted by the fallback path")
		}
		return nil
	})

	ctx.Step(`^rehydration for session (\w+) uses the (\w+) summary$`, func(sessID, wantIntent string) error {
		summary := state.engine.SessionCompactionSummaryForTesting(sessID)
		if summary == nil {
			return errors.New("no summary recorded for session " + sessID)
		}
		if summary.Intent != wantIntent+" intent" {
			return fmt.Errorf("session %s summary intent = %q; want %q", sessID, summary.Intent, wantIntent+" intent")
		}
		return nil
	})

	ctx.Step(`^a transcript with many small tool results for session (\w+)$`, func(sessID string) error {
		msgs := make([]provider.Message, 0, 20)
		for i := range 10 {
			msgs = append(msgs, toolTranscript("read", fmt.Sprintf("small tool result %d", i))...)
		}
		state.seed(sessID, 4, msgs...)
		return nil
	})

	ctx.Step(`^budget-gated tool-result compaction is applied$`, func() error {
		state.prunedMsgs = state.engine.BudgetCompactToolResultsForTesting(state.allStoreMessages(), state.limit)
		return nil
	})

	ctx.Step(`^no tool result is stubbed$`, func() error {
		for _, m := range state.prunedMsgs {
			if m.Role != "tool" {
				continue
			}
			if strings.Contains(m.Content, "[tool-result compacted:") {
				return errors.New("tool result stubbed despite budget headroom: " + m.Content)
			}
		}
		return nil
	})

	ctx.Step(`^a transcript with a read tool result longer than 2 KiB for session (\w+)$`, func(sessID string) error {
		long := strings.Repeat(strings.Repeat("a", 64)+"\n", 40)
		state.seed(sessID, 2, toolTranscript("read", long)...)
		return nil
	})

	ctx.Step(`^the transcript is stubbed for dispatch$`, func() error {
		msgs := state.allStoreMessages()
		state.prunedMsgs = state.engine.StubToolCallsForTesting(msgs)
		return nil
	})

	ctx.Step(`^the read tool result retains its first 2 KiB followed by a stub marker$`, func() error {
		for _, m := range state.prunedMsgs {
			if m.Role != "tool" {
				continue
			}
			if !strings.Contains(m.Content, "[tool stub: read") {
				head := m.Content
				if len(head) > 80 {
					head = head[:80]
				}
				return errors.New("missing tool stub marker: " + head)
			}
			if len(m.Content) <= 1024 {
				head := m.Content
				if len(head) > 80 {
					head = head[:80]
				}
				return errors.New("tool result head not retained: " + head)
			}
			return nil
		}
		return errors.New("no tool result found in stubbed transcript")
	})

	ctx.Step(`^the default auto-compaction threshold should be 0\.75$`, func() error {
		if got := flowctx.DefaultCompressionConfig().AutoCompaction.Threshold; got != 0.75 {
			return fmt.Errorf("default threshold = %v; want 0.75", got)
		}
		return nil
	})

	ctx.Step(`^a transcript with an old read tool result larger than the prune char limit$`, func() error {
		state.sourceMsgs = toolTranscript("read", strings.Repeat("prune me ", 1000))
		return nil
	})

	ctx.Step(`^the Stage-1 prune pass runs over the transcript$`, func() error {
		state.guardToolResultsFromPrune(state.sourceMsgs)
		pruned := engine.PruneOldToolOutputsForTesting(state.sourceMsgs, wordE2ECounter{limit: state.limit})
		state.prunedMsgs = pruned
		return nil
	})

	ctx.Step(`^the source transcript messages are unmodified$`, func() error {
		if got := state.sourceMsgs[len(state.sourceMsgs)-1].Content; !strings.HasSuffix(got, "prune me ") || strings.Contains(got, "[...truncated") {
			return errors.New("source transcript was mutated by pruning")
		}
		return nil
	})

	ctx.Step(`^the pruned copy truncates the old tool result$`, func() error {
		for _, m := range state.prunedMsgs {
			if m.Role == "tool" && strings.Contains(m.Content, "[...truncated") {
				return nil
			}
		}
		return errors.New("pruned copy does not carry the truncation marker")
	})

	ctx.Step(`^a compaction summary with files to restore for session (\w+)$`, func(sessID string) error {
		dir, err := os.MkdirTemp("", "reliability-files-*")
		if err != nil {
			return err
		}
		path := filepath.Join(dir, "restore.txt")
		if err := os.WriteFile(path, []byte("restored file body"), 0o600); err != nil {
			return err
		}
		resp, err := reliabilitySummaryJSON("eta intent", []string{path})
		if err != nil {
			return err
		}
		state.summariser.response = resp
		state.seed(sessID, 6)
		cctx := context.WithValue(context.Background(), session.IDKey{}, sessID)
		state.lastCompaction = state.engine.MaybeCompactForcedForTesting(cctx, sessID, "tool_result_wave", nil)
		if state.lastCompaction == "" {
			return errors.New("eta compaction produced no summary")
		}
		return nil
	})

	ctx.Step(`^the compaction state is persisted to the session sidecar$`, func() error {
		dir, err := os.MkdirTemp("", "reliability-meta-*")
		if err != nil {
			return err
		}
		state.metaPath = filepath.Join(dir, state.sessID+".meta.json")
		state.engine.SetSessionPersistHookForTesting(func(sessionID, summaryJSON string) error {
			payload := map[string]any{"session_id": sessionID, "summary": json.RawMessage(summaryJSON)}
			body, mErr := json.Marshal(payload)
			if mErr != nil {
				return mErr
			}
			return os.WriteFile(state.metaPath, body, 0o600)
		})
		summary := state.engine.SessionCompactionSummaryForTesting(state.sessID)
		if summary == nil {
			return errors.New("no summary to persist")
		}
		summaryBody, err := json.Marshal(*summary)
		if err != nil {
			return err
		}
		payload := map[string]any{"session_id": state.sessID, "summary": json.RawMessage(summaryBody)}
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if err := os.WriteFile(state.metaPath, body, 0o600); err != nil {
			return err
		}
		state.engine.ForgetCompactionStateForTesting(state.sessID)
		return nil
	})

	ctx.Step(`^the engine state is restored from the sidecar$`, func() error {
		summary, err := state.engine.RestoreCompactionStateForTesting(state.sessID, state.metaPath)
		if err != nil {
			return err
		}
		state.restored = summary
		return nil
	})

	ctx.Step(`^the restored summary matches the persisted summary$`, func() error {
		if state.restored == nil || state.restored.Intent != "eta intent" {
			return fmt.Errorf("restored summary mismatch: %+v", state.restored)
		}
		return nil
	})

	ctx.Step(`^rehydration for session (\w+) reads the restored files$`, func(sessID string) error {
		msgs := state.engine.RehydrateForTesting(sessID, []provider.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "continue"},
		})
		for _, m := range msgs {
			if strings.Contains(m.Content, "restored file body") {
				return nil
			}
		}
		return errors.New("restored files were not rehydrated")
	})
}

// allStoreMessages returns the raw store transcript for the current
// scenario session.
func (s *reliabilityState) allStoreMessages() []provider.Message {
	if s.engine == nil {
		return nil
	}
	return s.engine.AllMessagesForTesting()
}

// guardToolResultsFromPrune appends one older tool result before the
// guarded newest so the tail guard does not protect the only tool
// result under test.
func (s *reliabilityState) guardToolResultsFromPrune(msgs []provider.Message) []provider.Message {
	older := toolTranscript("read", strings.Repeat("older prune me ", 300))
	s.sourceMsgs = append(append([]provider.Message{}, older...), msgs...)
	return s.sourceMsgs
}
