package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
)

// sessionFileState is the per-session record of recently modified files
// for the Phase 3 structural-context rebuild.
type sessionFileState struct {
	paths  []string
	status string
}

// MaybeCompactForcedForTesting drives the compaction trigger with an
// explicit force discriminant so BDD scenarios can exercise the force
// and non-force memo policies without going through the gate-proximity
// machinery. forceTrigger is the closed-vocabulary trigger ("" means
// the ratio path).
//
// Expected:
//   - ctx carries the session ID key.
//   - sessionID identifies the session to compact.
//   - forceTrigger matches maybeAutoCompact's vocabulary.
//   - explicitMessages is nil (store path).
//
// Returns:
//   - The compaction summary text, "" when no fire.
//
// Side effects:
//   - Same as maybeAutoCompact.
func (e *Engine) MaybeCompactForcedForTesting(ctx context.Context, sessionID, forceTrigger string, explicitMessages []provider.Message) string {
	manifest := e.Manifest()
	return e.maybeAutoCompact(ctx, sessionID, &manifest, e.ModelContextLimit(), forceTrigger)
}

// SessionCompactionMemoValidForTesting reports whether the session's
// memo entry still holds a summary — false after Phase 3 invalidation.
//
// Expected: sessionID identifies the session.
// Returns: true when a cached summary remains.
// Side effects: None.
func (e *Engine) SessionCompactionMemoValidForTesting(sessionID string) bool {
	e.buildStateMu.Lock()
	defer e.buildStateMu.Unlock()
	cached, hit := e.sessionCompactionMemo[sessionID]
	return hit && cached.summary != nil
}

// SetActivePlanForTesting records the session's active plan text that
// the Phase 3 structural-context rebuild re-injects after compaction.
//
// Expected: sessionID is non-empty; planText is the plan body.
// Returns: None.
// Side effects: Stores the plan under e.mu.
func (e *Engine) SetActivePlanForTesting(sessionID, planText string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sessionActivePlans == nil {
		e.sessionActivePlans = make(map[string]string)
	}
	e.sessionActivePlans[sessionID] = planText
}

// SetSessionFilesForTesting records the session's recently modified
// files with a brief per-file status for the structural-context rebuild.
//
// Expected: sessionID is non-empty; files is the path list; status is a
// one-line description applied to each path.
// Returns: None.
// Side effects: Stores the entries under e.mu.
func (e *Engine) SetSessionFilesForTesting(sessionID string, files []string, status string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	entry := sessionFileState{paths: append([]string(nil), files...), status: status}
	if e.sessionRecentFiles == nil {
		e.sessionRecentFiles = make(map[string]sessionFileState)
	}
	e.sessionRecentFiles[sessionID] = entry
}

// SetSessionCoordinationKeysForTesting records the coordination-store
// keys written this session for the structural-context rebuild.
//
// Expected: sessionID is non-empty; keys is the key list.
// Returns: None.
// Side effects: Stores the keys under e.mu.
func (e *Engine) SetSessionCoordinationKeysForTesting(sessionID string, keys []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sessionCoordinationKeys == nil {
		e.sessionCoordinationKeys = make(map[string][]string)
	}
	e.sessionCoordinationKeys[sessionID] = append([]string(nil), keys...)
}

// StubToolCallsForTesting applies the Phase 3 tool-call stubbing policy
// to a message slice without rebuilding the window.
//
// Expected: messages is the transcript slice.
// Returns: The stubbed copy of the slice.
// Side effects: None — pure function over engine state.
func (e *Engine) StubToolCallsForTesting(messages []provider.Message) []provider.Message {
	return e.stubToolCalls(messages)
}

// RebuildExplicitPathForTesting drives the Phase 3 explicit-path
// rebuild: stub tool calls, then assemble system prompt, structural
// context, summary and token-bounded tail.
//
// Expected:
//   - ctx carries provider/model resolution keys.
//   - sessionID identifies the session for structural context.
//   - messages is the pre-compaction transcript.
//   - summary is the compaction summary text ("" allowed).
//
// Returns:
//   - The rebuilt window, never nil.
//
// Side effects: None beyond reading engine state.
func (e *Engine) RebuildExplicitPathForTesting(ctx context.Context, sessionID string, messages []provider.Message, summary string) []provider.Message {
	stubbed := e.stubToolCalls(messages)
	rebuilt := e.rebuildExplicitPathTokenBounded(ctx, sessionID, stubbed, summary)
	if rebuilt == nil {
		floor := []provider.Message{{Role: "system", Content: e.BuildSystemPromptCtx(ctx)}}
		floor = append(floor, stubbed...)
		return floor
	}
	return rebuilt
}

// phase3StubCapTokens caps the verbatim retention of non-compactable
// tool results before they degrade to a key-reference stub.
const phase3StubCapTokens = 2000

// nonCompactableToolNames is the closed set of tools whose results must
// survive Phase 3 stubbing verbatim up to phase3StubCapTokens.
var nonCompactableToolNames = map[string]struct{}{
	"delegate":           {},
	"coordination_store": {},
	"todowrite":          {},
}

// stubToolCalls replaces compactable tool results older than the last
// one with one-line stubs naming tool, target and outcome; non-compactable
// tool results are retained verbatim under phase3StubCapTokens, else
// stubbed with a coordination key reference.
//
// Expected: messages is the transcript; tool names ride on the
// preceding assistant message's ToolCalls.
// Returns: The stubbed slice.
// Side effects: None — input never mutated.
func (e *Engine) stubToolCalls(messages []provider.Message) []provider.Message {
	if len(messages) == 0 {
		return messages
	}
	out := make([]provider.Message, len(messages))
	copy(out, messages)
	for i := range out {
		if out[i].Role != "tool" {
			continue
		}
		name, target := precedingToolNameAndTarget(out, i)
		if _, protected := nonCompactableToolNames[name]; protected {
			if e.tokenCounter != nil && e.tokenCounter.Count(out[i].Content) <= phase3StubCapTokens {
				continue
			}
			out[i].Content = fmt.Sprintf("[tool stub: %s target=%q coordination key reference — re-read via coordination_store List]", name, target)
			continue
		}
		out[i].Content = toolCallStubLine(name, target, out[i].Content)
	}
	return out
}

// toolCallStubLine renders the one-line stub for a compacted tool call.
//
// Expected: name and target describe the call; content is the result.
// Returns: The single-line stub text.
// Side effects: None.
func toolCallStubLine(name, target, content string) string {
	outcome := "completed"
	if strings.HasPrefix(strings.TrimSpace(content), "Error") {
		outcome = "errored"
	}
	return fmt.Sprintf("[tool stub: %s target=%q outcome=%s]", name, target, outcome)
}

// precedingToolNameAndTarget resolves the tool name and target argument
// for the tool-result message at index i from the nearest preceding
// assistant message carrying ToolCalls.
//
// Expected: messages is the transcript; i indexes a tool message.
// Returns: The tool name ("" when unknown) and target.
// Side effects: None.
func precedingToolNameAndTarget(messages []provider.Message, i int) (string, string) {
	for j := i - 1; j >= 0; j-- {
		if messages[j].Role != "assistant" || len(messages[j].ToolCalls) == 0 {
			continue
		}
		call := messages[j].ToolCalls[0]
		target := ""
		if v, ok := call.Arguments["target"].(string); ok {
			target = v
		} else if v, ok := call.Arguments["key"].(string); ok {
			target = v
		}
		return call.Name, target
	}
	return "", ""
}

// structuralContextFilesTokenCap is the token ceiling above which the
// recently-modified-files block degrades to path-only.
const structuralContextFilesTokenCap = 5000

// buildStructuralContextMessages assembles the re-injected structural
// context for the explicit-path rebuild: active plan text,
// recently-modified files (path + brief status, path-only above
// 5000 tokens), and coordination keys written this session.
//
// Expected: sessionID identifies the session.
// Returns: Zero or one system messages carrying the blocks.
// Side effects: None — reads engine state under e.mu.
func (e *Engine) buildStructuralContextMessages(sessionID string) []provider.Message {
	e.mu.RLock()
	plan := e.sessionActivePlans[sessionID]
	files := e.sessionRecentFiles[sessionID]
	keys := e.sessionCoordinationKeys[sessionID]
	e.mu.RUnlock()

	var sections []string
	if plan != "" {
		sections = append(sections, "[active plan]\n"+plan)
	}
	if len(files.paths) > 0 {
		var lines []string
		statusTokens := 0
		if e.tokenCounter != nil {
			statusTokens = e.tokenCounter.Count(files.status)
		}
		pathOnly := statusTokens*len(files.paths) > structuralContextFilesTokenCap
		for _, p := range files.paths {
			if pathOnly {
				lines = append(lines, "- "+p)
				continue
			}
			lines = append(lines, "- "+p+" ("+files.status+")")
		}
		sections = append(sections, "[recently modified files]\n"+strings.Join(lines, "\n"))
	}
	if len(keys) > 0 {
		sections = append(sections, "[coordination keys written this session]\n"+strings.Join(keys, "\n"))
	}
	if len(sections) == 0 {
		return nil
	}
	return []provider.Message{{Role: "system", Content: strings.Join(sections, "\n\n")}}
}

// rebuildExplicitPathTokenBounded rebuilds the explicit/session-path
// window after compaction: system prompt + todo context + structural
// context + summary + verbatim tail, token-budgeted at usable*4/5 with
// deterministic trimming (oldest stubs first).
//
// Expected:
//   - ctx carries provider/model resolution keys.
//   - sessionID identifies the session.
//   - messages is the stubbed transcript; never empty.
//   - summary may be "".
//
// Returns:
//   - The rebuilt slice, or nil when the budget cannot be resolved.
//
// Side effects: None beyond reading engine state.
func (e *Engine) rebuildExplicitPathTokenBounded(ctx context.Context, sessionID string, messages []provider.Message, summary string) []provider.Message {
	if e == nil || e.tokenCounter == nil || len(messages) == 0 {
		return nil
	}
	limit := e.explicitPathContextLimit(ctx)
	if limit <= 0 {
		return nil
	}
	usable := limit - e.outputReserveFor(&provider.ChatRequest{
		Provider: e.lastProviderCtx(ctx),
		Model:    e.lastModelCtx(ctx),
		Messages: messages,
		Tools:    e.buildToolSchemasCtx(ctx),
	})
	if usable < 1 {
		usable = 1
	}
	target := usable * 4 / 5

	prefix := e.explicitPathPrefix(ctx, sessionID)
	prefixTokens := 0
	for _, m := range prefix {
		prefixTokens += e.tokenCounter.Count(m.Content)
	}
	summaryTokens := 0
	if summary != "" {
		summaryTokens = e.tokenCounter.Count(summary)
	}

	trimmed := append([]provider.Message(nil), messages...)
	for len(trimmed) > 1 && e.explicitPathTailTokens(prefixTokens, summaryTokens, trimmed) > target {
		trimmed = trimmed[1:]
	}

	rebuilt := make([]provider.Message, 0, len(prefix)+1+len(trimmed))
	rebuilt = append(rebuilt, prefix...)
	if summary != "" {
		rebuilt = append(rebuilt, provider.Message{Role: "assistant", Content: summary})
	}
	rebuilt = append(rebuilt, trimmed...)
	return rebuilt
}

// explicitPathContextLimit resolves the context length for the
// explicit-path rebuild, falling back to the engine model limit.
//
// Expected: ctx carries provider/model resolution keys.
// Returns: The resolved limit, 0 when unresolvable.
// Side effects: None.
func (e *Engine) explicitPathContextLimit(ctx context.Context) int {
	prov := e.lastProviderCtx(ctx)
	model := e.lastModelCtx(ctx)
	limit := e.ResolveContextLength(prov, model)
	if limit <= 0 {
		limit = e.ModelContextLimit()
	}
	return limit
}

// explicitPathPrefix assembles the fixed prefix for the explicit-path
// rebuild: system prompt, todo context, structural context.
//
// Expected: ctx carries system-prompt build keys; sessionID keys
// todo and structural context.
// Returns: The prefix slice (never empty).
// Side effects: None beyond reading engine state.
func (e *Engine) explicitPathPrefix(ctx context.Context, sessionID string) []provider.Message {
	prefix := []provider.Message{{Role: "system", Content: e.BuildSystemPromptCtx(ctx)}}
	prefix = e.appendTodoContext(prefix, sessionID)
	prefix = append(prefix, e.buildStructuralContextMessages(sessionID)...)
	return prefix
}

// explicitPathTailTokens estimates the full-window token cost of a
// candidate tail given the fixed prefix and summary costs.
//
// Expected: prefixTokens/summaryTokens are the precomputed fixed
// costs; tail is the candidate verbatim tail.
// Returns: The estimated total token cost.
// Side effects: None.
func (e *Engine) explicitPathTailTokens(prefixTokens, summaryTokens int, tail []provider.Message) int {
	total := prefixTokens + summaryTokens
	for _, m := range tail {
		total += e.tokenCounter.Count(m.Content)
	}
	return total
}
