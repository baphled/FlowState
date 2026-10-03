package engine

import (
	"context"
	"fmt"
	"strings"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// ctxstoreCompactionSummary aliases the context store's compaction
// summary type for in-package use.
type ctxstoreCompactionSummary = ctxstore.CompactionSummary

// nonCompactableTools lists tool names whose results must never be
// stubbed by the rehydrated rebuild — they carry coordination state
// (delegation chains, coordination-store payloads, todo lists) that
// has no recoverable source-of-truth inside the transcript.
var nonCompactableTools = map[string]bool{
	"delegate":           true,
	"coordination_store": true,
	"todowrite":          true,
	"todo_update":        true,
}

// nonCompactableToolTokenCap bounds how many tokens a single
// non-compactable tool result may occupy in the rehydrated tail
// before it too is stubbed with a key reference. Keeps one giant
// coordination payload from consuming the whole tail budget.
const nonCompactableToolTokenCap = 400

// structuralContextTokenCap is the cap above which re-injected file
// content degrades to path-only listings (Claude Code style): when
// the rendered structural-context block would exceed this many
// tokens, the file section degrades to path + status rather than
// content. Enforced in renderStructuralContext.
const structuralContextTokenCap = 5000

// sessionRecentFilesCap bounds how many file paths the
// recently-modified section tracks per session (oldest evicted
// first) so long sessions cannot grow the tracking state — or the
// re-injected block — without bound.
const sessionRecentFilesCap = 20

// sessionCoordinationKeysCap bounds how many coordination keys are
// tracked per session for re-injection, mirroring
// sessionRecentFilesCap.
const sessionCoordinationKeysCap = 20

// recordSessionFile appends path to the session's recently-modified
// list (evicting the oldest duplicate entry first so a re-edit moves
// the path to the newest slot) under the sessionRecentFilesCap.
//
// Expected:
//   - sessionID identifies the session; path is the file just
//     modified.
//
// Side effects:
//   - Updates e.sessionRecentFiles[sessionID] under buildStateMu.
//
// Returns: result of recordSessionFile.
func (e *Engine) recordSessionFile(sessionID, path string) {
	e.buildStateMu.Lock()
	defer e.buildStateMu.Unlock()
	files := e.sessionRecentFiles[sessionID]
	out := files[:0]
	for _, f := range files {
		if f != path {
			out = append(out, f)
		}
	}
	out = append(out, path)
	if len(out) > sessionRecentFilesCap {
		out = out[len(out)-sessionRecentFilesCap:]
	}
	e.sessionRecentFiles[sessionID] = out
}

// recordSessionCoordinationKey appends key to the session's
// coordination-key list, mirroring recordSessionFile's eviction.
//
// Expected:
//   - sessionID identifies the session; key is the coordination-store
//     key just written.
//
// Side effects:
//   - Updates e.sessionCoordinationKeys[sessionID] under buildStateMu.
//
// Returns: result of recordSessionCoordinationKey.
func (e *Engine) recordSessionCoordinationKey(sessionID, key string) {
	e.buildStateMu.Lock()
	defer e.buildStateMu.Unlock()
	keys := e.sessionCoordinationKeys[sessionID]
	out := keys[:0]
	for _, k := range keys {
		if k != key {
			out = append(out, k)
		}
	}
	out = append(out, key)
	if len(out) > sessionCoordinationKeysCap {
		out = out[len(out)-sessionCoordinationKeysCap:]
	}
	e.sessionCoordinationKeys[sessionID] = out
}

// fileMutatingTools names the tools whose primary argument is a
// filesystem path the session has just modified; the first string
// argument of a work tool is recorded when no known field matches.
var fileMutatingTools = map[string][]string{
	"write":    {"path", "file", "filename"},
	"edit":     {"path", "file", "filename"},
	"write_fd": {"path", "file", "filename"},
}

// recordSessionFileForTool records the file a mutating tool touched
// (Phase 3, rank 6) for later re-injection as structural context. A
// write/edit whose path argument resolves records that path; other
// work tools record nothing (their primary output is not a durable
// file target the agent would need re-injected).
//
// Expected:
//   - sessionID identifies the session; toolName names the tool;
//     args are the parsed call arguments.
//
// Side effects:
//   - Records the resolved path via recordSessionFile.
//
// Returns: result of recordSessionFileForTool.
func (e *Engine) recordSessionFileForTool(sessionID, toolName string, args map[string]any) {
	fields, ok := fileMutatingTools[toolName]
	if !ok {
		return
	}
	for _, f := range fields {
		if path, _ := args[f].(string); path != "" {
			e.recordSessionFile(sessionID, path)
			return
		}
	}
}

// structuralContextSources resolves the re-injection inputs for
// sessionID from engine state: the todo store's live items act as
// the active plan (path + status, one line each), the session's
// recently-modified file paths, and its coordination keys.
//
// Expected:
//   - sessionID identifies the session to resolve state for.
//
// Returns:
//   - plans, modifiedFiles, coordKeys for the session (nil when the
//     engine or its state is absent).
//
// Side effects:
//   - None (read-only under buildStateMu).
func (e *Engine) structuralContextSources(sessionID string) (plans, modifiedFiles, coordKeys []string) {
	if e == nil {
		return nil, nil, nil
	}
	e.buildStateMu.Lock()
	modifiedFiles = append(modifiedFiles, e.sessionRecentFiles[sessionID]...)
	coordKeys = append(coordKeys, e.sessionCoordinationKeys[sessionID]...)
	e.buildStateMu.Unlock()
	if e.todoStore != nil {
		for _, it := range e.todoStore.Get(sessionID) {
			plans = append(plans, fmt.Sprintf("[%s] %s", it.Status, it.Content))
		}
	}
	return plans, modifiedFiles, coordKeys
}

// stubToolResult renders the one-line stub left in place of a
// compacted tool call: tool name, call id reference, and outcome
// line. The stub is deliberately single-line so token accounting
// stays cheap and the tail scan stays readable.
//
// Expected:
//   - name/id identify the tool call; content is the original result.
//
// Returns:
//   - The single-line stub string.
//
// Side effects:
//   - None (pure).
func stubToolResult(name, id string, content string) string {
	outcome := strings.TrimSpace(content)
	if idx := strings.IndexAny(outcome, "\r\n"); idx >= 0 {
		outcome = outcome[:idx]
	}
	if len(outcome) > 80 {
		outcome = outcome[:80]
	}
	return fmt.Sprintf("[tool %s #%s: %s]", name, id, outcome)
}

// stubTailMessages rewrites the tail's compactable tool results as
// one-line stubs while retaining non-compactable tool results
// verbatim (up to nonCompactableToolTokenCap tokens, else stubbed
// with a key reference). Pure over msgs; returns a new slice.
//
// Expected:
//   - msgs is the tail window post-compaction.
//
// Returns:
//   - A new slice with compactable tool results stubbed.
//
// Side effects:
//   - None (allocates a new slice).
func (e *Engine) stubTailMessages(msgs []provider.Message) []provider.Message {
	if e == nil || e.tokenCounter == nil {
		return msgs
	}
	out := make([]provider.Message, len(msgs))
	for i, m := range msgs {
		if m.Role != "tool" || len(m.ToolCalls) == 0 {
			out[i] = m
			continue
		}
		call := m.ToolCalls[len(m.ToolCalls)-1]
		if !nonCompactableTools[call.Name] {
			out[i] = provider.Message{Role: "tool", Content: stubToolResult(call.Name, call.ID, m.Content), ToolCalls: m.ToolCalls}
			continue
		}
		if e.tokenCounter.Count(m.Content) <= nonCompactableToolTokenCap {
			out[i] = m
			continue
		}
		out[i] = provider.Message{
			Role:      "tool",
			Content:   fmt.Sprintf("[tool %s #%s: result exceeded the rehydration cap; re-read via the coordination store key]", call.Name, call.ID),
			ToolCalls: m.ToolCalls,
		}
	}
	return out
}

// renderStructuralContext renders the re-injected structural context
// block: active plan, recently-modified files (path + status), and
// coordination keys written this session. When the rendered block
// exceeds structuralContextTokenCap the file section degrades to
// path-only so the block stays bounded.
//
// Expected:
//   - plans, modifiedFiles, coordKeys are the rendered sections' rows.
//
// Returns:
//   - The structural-context block ("" when all sections are empty).
//
// Side effects: None (pure rendering).
func (e *Engine) renderStructuralContext(plans, modifiedFiles, coordKeys []string) string {
	var b strings.Builder
	if len(plans) > 0 {
		b.WriteString("Active plan:\n")
		for _, p := range plans {
			b.WriteString("- " + p + "\n")
		}
	}
	pathOnly := false
	if e != nil && e.tokenCounter != nil {
		full := renderModifiedFiles(modifiedFiles, false)
		if e.tokenCounter.Count(full) > structuralContextTokenCap {
			pathOnly = true
		}
	}
	if len(modifiedFiles) > 0 {
		b.WriteString(renderModifiedFiles(modifiedFiles, pathOnly))
	}
	if len(coordKeys) > 0 {
		b.WriteString("Session coordination keys:\n")
		for _, k := range coordKeys {
			b.WriteString("- " + k + "\n")
		}
	}
	return b.String()
}

// renderModifiedFiles renders the recently-modified-files section,
// degrading to path-only listings when pathOnly is set so the block
// honours structuralContextTokenCap.
//
// Expected:
//   - files is the recently-modified path list; pathOnly selects the
//     degraded rendering.
//
// Returns:
//   - The rendered file section.
//
// Side effects:
//   - None (pure).
func renderModifiedFiles(files []string, pathOnly bool) string {
	var b strings.Builder
	b.WriteString("Recently modified files:\n")
	for _, f := range files {
		if pathOnly {
			b.WriteString("- " + f + "\n")
			continue
		}
		b.WriteString("- " + f + " (modified this session)\n")
	}
	return b.String()
}

// rebuildRehydrated assembles the Phase 3 rehydrated window:
//
//	system prompt + structural-context block + compaction summary +
//	stubbed verbatim tail,
//
// all token-budgeted at usable*4/5 with deterministic trimming that
// drops the oldest stubbed tail messages first and never drops the
// newest message.
//
// Expected:
//   - sessionID keys the todo context appended after the system prompt.
//   - messages is the post-compaction verbatim tail (tool results may
//     be compactable; they are stubbed here).
//   - summary is the compaction summary text ("" omits the block).
//   - plans / modifiedFiles / coordKeys are the re-injected structural
//     context sources; empty slices omit the corresponding sections.
//
// Returns:
//   - The rebuilt window. When the model limit cannot be resolved the
//     unbounded assembly is returned so callers degrade gracefully.
//
// Side effects:
//   - None beyond whatever appendTodoContext records for the session.
func (e *Engine) rebuildRehydrated(ctx context.Context, sessionID string, messages []provider.Message, summary string, plans, modifiedFiles, coordKeys []string) []provider.Message {
	if e == nil || len(messages) == 0 {
		return nil
	}

	prefix := []provider.Message{{Role: "system", Content: e.BuildSystemPromptCtx(ctx)}}
	prefix = e.appendTodoContext(prefix, sessionID)

	structural := e.renderStructuralContext(plans, modifiedFiles, coordKeys)
	if structural != "" {
		prefix = append(prefix, provider.Message{Role: "system", Content: structural})
	}

	mid := make([]provider.Message, 0, 1)
	if summary != "" {
		mid = append(mid, provider.Message{Role: "assistant", Content: summary})
	}

	tail := e.stubTailMessages(messages)

	prefixTokens := 0
	for _, m := range prefix {
		prefixTokens += e.tokenCounter.Count(m.Content)
	}
	midTokens := 0
	for _, m := range mid {
		midTokens += e.tokenCounter.Count(m.Content)
	}

	usable := e.rehydratedUsableBudget(ctx)
	if usable <= 0 || e.tokenCounter == nil {
		rebuilt := make([]provider.Message, 0, len(prefix)+len(mid)+len(tail))
		rebuilt = append(rebuilt, prefix...)
		rebuilt = append(rebuilt, mid...)
		return append(rebuilt, tail...)
	}
	tail = e.trimTailToBudget(tail, prefixTokens+midTokens, usable*4/5)

	rebuilt := make([]provider.Message, 0, len(prefix)+len(mid)+len(tail))
	rebuilt = append(rebuilt, prefix...)
	rebuilt = append(rebuilt, mid...)
	return append(rebuilt, tail...)
}

// trimTailToBudget drops the oldest tail messages until prefixTokens
// plus the tail cost fits target, with a keep-one floor so the newest
// message is never dropped.
//
// Expected:
//   - tail is the stubbed tail window; prefixTokens is the already
//     counted prefix cost; target is the token budget.
//
// Returns:
//   - The trimmed tail (at least one message).
//
// Side effects:
//   - None (pure function over the input slice).
func (e *Engine) trimTailToBudget(tail []provider.Message, prefixTokens, target int) []provider.Message {
	for len(tail) > 1 {
		total := prefixTokens
		for _, m := range tail {
			total += e.tokenCounter.Count(m.Content)
		}
		if total <= target {
			break
		}
		tail = tail[1:]
	}
	return tail
}

// rehydratedUsableBudget resolves the usable request budget (model
// limit minus output reserve) for the rehydrated rebuild, mirroring
// rebuildContextWindowTokenBounded's arithmetic.
//
// Expected:
//   - ctx carries the provider/model context used to resolve the
//     model context length.
//
// Returns:
//   - the usable request budget (limit minus output reserve, floored
//     at 1), or 0 when the limit cannot be resolved.
//
// Side effects:
//   - None.
func (e *Engine) rehydratedUsableBudget(ctx context.Context) int {
	prov := e.lastProviderCtx(ctx)
	model := e.lastModelCtx(ctx)
	limit := e.ResolveContextLength(prov, model)
	if limit <= 0 {
		return 0
	}
	req := provider.ChatRequest{Provider: prov, Model: model}
	usable := limit - e.outputReserveFor(&req)
	if usable < 1 {
		usable = 1
	}
	return usable
}

// RebuildRehydratedForTesting exposes rebuildRehydrated to the BDD
// harness, mirroring BuildContextWindowForTesting in testing.go.
// When the caller supplies no structural sources (all slices nil or
// empty), the engine resolves them from session state via
// structuralContextSources so the harness exercises the same
// production inputs the explicit/session path would.
//
// Expected:
//   - ctx carries a live context; sessionID identifies the session.
//   - messages is the candidate window (stubs and tail) to assemble.
//   - summary is the compaction summary to inject.
//   - plans, modifiedFiles, coordKeys name structural sources; when
//     all are empty they are resolved from session state.
//
// Returns:
//   - the rebuilt message window.
//
// Side effects:
//   - None beyond resolving structural sources from session state.
func (e *Engine) RebuildRehydratedForTesting(ctx context.Context, sessionID string, messages []provider.Message, summary string, plans, modifiedFiles, coordKeys []string) ([]provider.Message, error) {
	if len(plans) == 0 && len(modifiedFiles) == 0 && len(coordKeys) == 0 {
		p, f, k := e.structuralContextSources(sessionID)
		plans, modifiedFiles, coordKeys = p, f, k
	}
	return e.rebuildRehydrated(ctx, sessionID, messages, summary, plans, modifiedFiles, coordKeys), nil
}

// PriorCompactionSummaryForTesting exposes the per-session memo
// entry (nil when absent or invalidated) to the BDD harness so the
// memo-invalidation scenarios can assert the eviction directly.
//
// Expected:
//   - sessionID identifies a session that previously compacted.
//
// Returns:
//   - the memoised CompactionSummary for the session, or nil when no
//     memo exists or it was invalidated.
//
// Side effects:
//   - None (read-only access to the memo cache).
func (e *Engine) PriorCompactionSummaryForTesting(sessionID string) *ctxstoreCompactionSummary {
	return e.getPriorCompactionSummary(sessionID)
}
