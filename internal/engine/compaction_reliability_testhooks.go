package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// PruneOldToolOutputsForTesting exposes the Stage-1 prune pass for BDD
// steps so scenarios can assert the source slice is never mutated.
//
// Expected: msgs is the summariser input slice.
// Returns: the pruned copy.
// Side effects: None — the input slice is never mutated.
func PruneOldToolOutputsForTesting(msgs []provider.Message, counter ctxstore.TokenCounter) []provider.Message {
	out, _, _ := pruneOldToolOutputs(msgs, counter)
	return out
}

// AllMessagesForTesting exposes the store transcript for BDD steps.
//
// Expected: store is wired.
// Returns: the full store message slice, nil when no store.
// Side effects: None.
func (e *Engine) AllMessagesForTesting() []provider.Message {
	if e == nil || e.store == nil {
		return nil
	}
	return e.store.AllMessages()
}

// SessionCompactionSummaryForTesting reports the per-session summary
// recorded by the last compaction, mirroring what maybeRehydrate
// consumes for that session.
//
// Expected: sessionID identifies the session.
// Returns: the recorded summary, nil when absent.
// Side effects: None.
func (e *Engine) SessionCompactionSummaryForTesting(sessionID string) *ctxstore.CompactionSummary {
	e.buildStateMu.Lock()
	defer e.buildStateMu.Unlock()
	if s, ok := e.sessionCompactionSummaries[sessionID]; ok {
		return s
	}
	if e.lastCompactionSummary == nil {
		return nil
	}
	copy := *e.lastCompactionSummary
	return &copy
}

// BudgetCompactToolResultsForTesting applies the budget-gated
// compactOldToolResultsForBudget pass for BDD steps.
//
// Expected: msgs is the dispatch slice; contextLimit is the model limit.
// Returns: the compacted slice.
// Side effects: None.
func (e *Engine) BudgetCompactToolResultsForTesting(msgs []provider.Message, contextLimit int) []provider.Message {
	return compactOldToolResultsForBudget(msgs, e.toolMessageCompactionThreshold, e.toolMessageCompactionKeepRecent, e.tokenCounter, contextLimit)
}

// RehydrateForTesting drives maybeRehydrate for BDD steps.
//
// Expected: sessionID identifies the session; msgs is the window.
// Returns: the rehydrated window.
// Side effects: same as maybeRehydrate.
func (e *Engine) RehydrateForTesting(sessionID string, msgs []provider.Message) []provider.Message {
	return e.maybeRehydrate(sessionID, msgs)
}

// compactionSidecarShape is the JSON body persisted alongside the
// session meta sidecar so compaction state survives a restart.
type compactionSidecarShape struct {
	SessionID string                      `json:"session_id"`
	Summary   *ctxstore.CompactionSummary `json:"summary,omitempty"`
}

// SetSessionPersistHookForTesting wires the sidecar persist callback
// used by persistCompactionSidecar.
//
// Expected: hook is the callback; nil disables sidecar persistence.
// Returns: None.
// Side effects: Replaces e.sessionPersistHook.
func (e *Engine) SetSessionPersistHookForTesting(hook func(sessionID, summaryJSON string) error) {
	e.sessionPersistHook = hook
}

// PersistCompactionStateForTesting writes the session's current
// compaction summary identity to a sidecar path atomically.
//
// Expected: sessionID identifies the session; path is the sidecar file.
// Returns: an error on marshalling or write failure.
// Side effects: creates or replaces the sidecar file.
func (e *Engine) PersistCompactionStateForTesting(sessionID, path string) error {
	summary := e.SessionCompactionSummaryForTesting(sessionID)
	body, err := json.Marshal(compactionSidecarShape{SessionID: sessionID, Summary: summary})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RestoreCompactionStateForTesting loads a persisted compaction
// sidecar and reinstates the per-session summary for rehydration.
//
// Expected: sessionID identifies the session; path is the sidecar file.
// Returns: the restored summary, an error on read/parse failure.
// Side effects: reinstates sessionCompactionSummaries[sessionID].
func (e *Engine) RestoreCompactionStateForTesting(sessionID, path string) (*ctxstore.CompactionSummary, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var shape compactionSidecarShape
	if err := json.Unmarshal(body, &shape); err != nil {
		return nil, err
	}
	if shape.SessionID != sessionID {
		return nil, errors.New("compaction sidecar session mismatch")
	}
	if shape.Summary == nil {
		return nil, errors.New("compaction sidecar carries no summary")
	}
	e.buildStateMu.Lock()
	if e.sessionCompactionSummaries == nil {
		e.sessionCompactionSummaries = make(map[string]*ctxstore.CompactionSummary)
	}
	summaryCopy := *shape.Summary
	e.sessionCompactionSummaries[sessionID] = &summaryCopy
	e.buildStateMu.Unlock()
	return shape.Summary, nil
}

// ForgetCompactionStateForTesting clears the session's in-memory
// compaction state, simulating a restart.
//
// Expected: sessionID identifies the session.
// Returns: None.
// Side effects: clears sessionCompactionSummaries[sessionID] and the
// sessionRehydrated flag.
func (e *Engine) ForgetCompactionStateForTesting(sessionID string) {
	e.buildStateMu.Lock()
	delete(e.sessionCompactionSummaries, sessionID)
	delete(e.sessionRehydrated, sessionID)
	e.buildStateMu.Unlock()
}

// RebuildTruncationFallbackForTesting drives the naive-truncation
// fallback rebuild used when the summariser is unavailable.
//
// Expected: ctx carries provider/model keys; sessionID identifies the
// session; prior is the pre-compaction transcript; contextLimit is the
// model context limit.
// Returns: the rebuilt window: system prompt, todo context, fallback
// summary line, token-proportional tail, live user tail.
// Side effects: None.
func (e *Engine) RebuildTruncationFallbackForTesting(ctx context.Context, sessionID string, prior []provider.Message, contextLimit int) []provider.Message {
	return e.rebuildTruncationFallbackWindow(ctx, sessionID, prior, truncationFallbackSummaryText, contextLimit, nil)
}
