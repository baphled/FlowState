// Package engine — context-loss regression guards for the compaction
// stack. This file carries the naive-truncation-fallback hardening
// (loud emission accounting, token-proportional tail, memo
// preservation), the per-session rehydration keying, and the budget
// gated Phase-3 tool-result stubbing policy.
package engine

import (
	"fmt"
	"strings"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// fallbackTailTokenShare is the fraction of the session's estimated
// token load the naive truncation fallback retains as a verbatim hot
// tail. The fallback fires only when the summariser is unavailable, so
// the tail is the last line of defence against total context loss;
// keeping roughly half of the live window (versus the historic
// fixed 50-message cap) preserves proportional recall on long
// sessions while still reclaiming budget on short ones. A third proved
// too thin: short-message transcripts lost more than they kept, which
// the context-loss regression suite pins as a hard failure.
const fallbackTailTokenShare = 1.0 / 2.0

// fallbackTailMinMessages is the floor on the proportional tail so a
// very small transcript never degrades to a single-message window.
const fallbackTailMinMessages = 8

// sessionLastCompactionSummaryLocked is the lock-held core: callers
// must already hold buildStateMu.
//
// Expected: sessionID identifies the session whose summary to read.
// Returns: The session's most recent compaction summary, or nil.
// Side effects: None.
func (e *Engine) sessionLastCompactionSummaryLocked(sessionID string) *ctxstore.CompactionSummary {
	if summary, ok := e.sessionCompactionSummary[sessionID]; ok {
		return summary
	}
	if e.lastCompactionSummary == nil {
		return nil
	}
	if _, memoised := e.sessionCompactionMemo[sessionID]; !memoised {
		return nil
	}
	return e.lastCompactionSummary
}

// recordSessionCompactionSummary stores the summary against the session
// so later rehydration and restart recovery can resolve it without the
// engine-global pointer.
//
// Expected:
//   - sessionID identifies the session; summary is the fresh summary.
//
// Returns:
//   - None.
//
// Side effects:
//   - Overwrites the per-session summary slot under buildStateMu.
func (e *Engine) recordSessionCompactionSummary(sessionID string, summary *ctxstore.CompactionSummary) {
	e.buildStateMu.Lock()
	defer e.buildStateMu.Unlock()
	if e.sessionCompactionSummary == nil {
		e.sessionCompactionSummary = make(map[string]*ctxstore.CompactionSummary)
	}
	e.sessionCompactionSummary[sessionID] = summary
}

// noteFallbackEmission records that the naive truncation fallback
// fired for the session. Every emission is also WARN-logged at the
// fire site; the counter exists so tests and telemetry can assert the
// fallback is loud rather than silent context loss.
//
// Expected:
//   - sessionID identifies the session the fallback fired for.
//
// Returns:
//   - None.
//
// Side effects:
//   - Increments the per-session fallback emission counter.
func (e *Engine) noteFallbackEmission(sessionID string) {
	e.buildStateMu.Lock()
	defer e.buildStateMu.Unlock()
	if e.sessionFallbackEmissions == nil {
		e.sessionFallbackEmissions = make(map[string]int)
	}
	e.sessionFallbackEmissions[sessionID]++
}

// FallbackEmissionsForTesting reports how many times the naive
// truncation fallback fired for the session.
//
// Expected: sessionID identifies the session.
// Returns: The fallback emission count.
// Side effects: None.
func (e *Engine) FallbackEmissionsForTesting(sessionID string) int {
	e.buildStateMu.Lock()
	defer e.buildStateMu.Unlock()
	return e.sessionFallbackEmissions[sessionID]
}

// SetLastCompactionSummaryForTesting installs a per-session compaction
// summary so isolation scenarios can seed two sessions with distinct
// summaries without driving full compaction turns.
//
// Expected: sessionID is non-empty; summary is the summary to install.
// Returns: None.
// Side effects: Records the summary against the session.
func (e *Engine) SetLastCompactionSummaryForTesting(sessionID string, summary ctxstore.CompactionSummary) {
	copyVal := summary
	e.recordSessionCompactionSummary(sessionID, &copyVal)
}

// MaybeRehydrateForTesting exposes maybeRehydrate so isolation
// scenarios can drive the per-session rehydration path directly.
//
// Expected: sessionID identifies the session; msgs is the window.
// Returns: The possibly rehydrated window.
// Side effects: Same as maybeRehydrate.
func (e *Engine) MaybeRehydrateForTesting(sessionID string, msgs []provider.Message) []provider.Message {
	return e.maybeRehydrate(sessionID, msgs)
}

// fallbackTailTokenBounded selects the trailing message slice whose
// cumulative token cost first reaches share*totalTokens when scanned
// from the newest message backwards, with a minimum message floor.
// Unlike a fixed message-count window this keeps a token-proportional
// tail: long sessions keep proportionally more verbatim context and
// short sessions keep (at least) the floor.
//
// Expected:
//   - messages is the full session transcript in chronological order.
//   - counter estimates token cost; nil disables the token scan and
//     falls back to the message floor.
//   - totalTokens is the transcript's precomputed token estimate; a
//     non-positive value triggers re-estimation when a counter exists.
//
// Returns:
//   - The token-proportional trailing slice (never empty for non-empty
//     input).
//
// Side effects:
//   - None.
func fallbackTailTokenBounded(messages []provider.Message, counter ctxstore.TokenCounter, totalTokens int) []provider.Message {
	if len(messages) == 0 {
		return messages
	}
	if counter != nil && totalTokens <= 0 {
		totalTokens = 0
		for i := range messages {
			totalTokens += counter.Count(messages[i].Content)
		}
	}
	if len(messages) <= fallbackTailMinMessages || counter == nil {
		return messages
	}
	target := int(float64(totalTokens) * fallbackTailTokenShare)
	spent := 0
	cut := len(messages) - fallbackTailMinMessages
	for i := len(messages) - 1; i >= 0; i-- {
		spent += counter.Count(messages[i].Content)
		if spent >= target {
			cut = i
			break
		}
	}
	if cut < 0 {
		cut = 0
	}
	return messages[cut:]
}

// cappedToolResultHead retains the first headCap bytes of a tool
// result with a truncation sentinel, replacing the historic one-line
// stub. Ordinary tools (read/bash/grep) carry their load-bearing
// signal at the head of the output, so a capped head preserves
// recoverable context where a one-line stub destroyed it.
const cappedToolResultHeadBytes = 2048

// cappedToolResultHead renders the capped-head retention form for a
// compacted tool result.
//
// Expected:
//   - name and target describe the originating call; content is the
//     full result text.
//
// Returns:
//   - The head-capped content with a truncation sentinel.
//
// Side effects:
//   - None.
func cappedToolResultHead(name, target, content string) string {
	if len(content) <= cappedToolResultHeadBytes {
		return content
	}
	head := content[:cappedToolResultHeadBytes]
	return fmt.Sprintf("%s\n[tool result head retained: %s target=%q, truncated after %d bytes]", head, name, target, cappedToolResultHeadBytes)
}

// isCappedHeadStub reports whether content is a previously produced
// capped-head retention form, so the pass is idempotent.
//
// Expected: content is a tool-result body.
// Returns: true when the content carries the retention sentinel.
// Side effects: None.
func isCappedHeadStub(content string) bool {
	return strings.Contains(content, "[tool result head retained:")
}
