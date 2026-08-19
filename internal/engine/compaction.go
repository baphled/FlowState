// Package engine — context-window compaction machinery.
//
// This file holds the engine's auto/micro/explicit compaction pipeline:
// threshold checks, gate-proximity force compaction, cold-range hashing,
// memoised summary reuse, rehydration, mid-tool-loop window rebuilds, the
// public CompactNow / MaybeCompactForModel entry points, and the
// compaction event/metrics publishers. Move-only extraction from
// engine.go; behaviour unchanged.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
)

// maybeAutoCompact runs the Phase 2 auto-compaction trigger when the
// engine is configured with an AutoCompactor, the feature is enabled,
// and either (a) the recent-message token load exceeds the configured
// threshold or (b) Slice 6a's gate-proximity tier (forceFire) demands
// compaction because the next request would land within 5% of the
// proactive saturation gate's refusal boundary.
//
// Expected:
//   - ctx carries cancellation/deadline for the LLM call.
//   - sessionID identifies the active session; threaded through to the
//     T10b ContextCompactedEvent so subscribers can correlate emitted
//     events with session telemetry.
//   - manifest has been prepared with the current system prompt (used to
//     determine SlidingWindowSize).
//   - tokenBudget is the full model context limit.
//   - forceTrigger is the discriminant for the force-fire path.
//     Empty string means "ratio path only — no force". Non-empty
//     skips the ratio gate; the AutoCompaction.Enabled flag and the
//     "have content to summarise" check still apply. Closed
//     vocabulary: "gate_proximity" (Slice 6a's tier),
//     "model_switch" (Phase-5 Slice α), "tool_result_wave"
//     (Phase-5 Slice γ).
//
// Returns:
//   - The summary text ("[auto-compacted summary]: <json>") when
//     compaction fired and succeeded; empty otherwise.
//   - The built window falls back to the normal path on:
//   - feature disabled,
//   - compactor nil,
//   - token load under threshold AND no force trigger,
//   - compactor error (logged, not fatal).
//
// Side effects:
//   - Issues one LLM call via the injected AutoCompactor when fired.
//   - Updates e.lastCompactionSummary on success; cleared on non-fire.
//   - Publishes a pluginevents.ContextCompactedEvent on the engine bus
//     on successful compaction (T10b per ADR - Tool-Call Atomicity).
//     Phase-5 Slice α/δ stamps the Trigger field.
func (e *Engine) maybeAutoCompact(ctx context.Context, sessionID string, manifest *agent.Manifest, tokenBudget int, forceTrigger string) string {
	forceFire := forceTrigger != ""
	threshold, ok := e.autoCompactionThreshold(manifest, tokenBudget)
	if !ok {
		// Feature disabled or preconditions unmet — clear the cross-
		// session "last summary" so LastCompactionSummary reflects
		// the current build rather than stale state from earlier
		// turns. The per-session memo is NOT cleared on this branch:
		// disabling compaction for one turn (e.g. tokenBudget <= 0
		// during a degraded build) should not force the next enabled
		// turn to re-summarise if the cold prefix has not changed.
		//
		// forceFire is honoured *only* when the feature flag and
		// preconditions allow: AutoCompaction.Enabled = false is the
		// operator's deliberate opt-out and gate-proximity must not
		// override it (see forceTrigger note below). The proactive gate then refuses the request on
		// its own — operators see the saturation loudly rather than
		// silently re-summarising.
		e.buildStateMu.Lock()
		e.lastCompactionSummary = nil
		e.buildStateMu.Unlock()
		return ""
	}

	recent, recentTokens, fullWindowTokens, fire := e.autoCompactionCandidates(ctx, manifest, tokenBudget, threshold, forceFire)
	if !fire {
		// Below threshold — clear the cross-session pointer as
		// before. Same per-session-memo retention rationale applies.
		e.buildStateMu.Lock()
		e.lastCompactionSummary = nil
		e.buildStateMu.Unlock()
		return ""
	}

	// Stage-1 prune — the OpenCode-shape port (May 2026 rename
	// bundle). Before invoking the LLM summariser, truncate old
	// tool-result outputs to a fixed character ceiling. The prune
	// pass is the cheap layer that reclaims tokens without paying
	// for a summariser call; the summariser is the expensive fallback
	// when pruning alone is insufficient.
	//
	// The prune output replaces `recent` for the rest of the trigger:
	//   - H2 memo hash sees the pruned content (changes invalidate
	//     prior memo entries — the right behaviour because the
	//     summariser input is materially different).
	//   - Summariser, when invoked, consumes the smaller pruned slice
	//     (cheaper inputs).
	//
	// Note the doc-comment lie at autoCompactionCandidates: the
	// fullWindowTokens it returns is the PRE-prune figure across
	// e.store.AllMessages(). Pruning only touches Content on
	// tool-result messages inside `recent`; tool-call args on the
	// preceding assistant messages are untouched. So subtracting
	// the prune's tokensSaved from fullWindowTokens yields the
	// post-prune full-window count without re-iterating the store.
	prunedRecent, prunedToolOutputs, prunedTokensSaved := pruneOldToolOutputs(recent, e.tokenCounter)
	recent = prunedRecent
	recentTokens -= prunedTokensSaved
	if recentTokens < 0 {
		recentTokens = 0
	}

	// Prune-only short-circuit. When the soft-ratio tier fired (NOT
	// a force-fire path) AND the prune pass reclaimed enough tokens
	// to drop the post-prune full-window ratio below the threshold,
	// skip the LLM summariser entirely. Pruning succeeded as the sole
	// reclaim mechanism; the summariser cost is wasted on this turn.
	//
	// The force-fire paths (gate_proximity, model_switch,
	// tool_result_wave, manual /compact) skip this short-circuit —
	// those callers explicitly want a fresh summary regardless of
	// what pruning saved.
	if !forceFire && prunedToolOutputs > 0 {
		postPruneFullWindowTokens := fullWindowTokens - prunedTokensSaved
		if postPruneFullWindowTokens < 0 {
			postPruneFullWindowTokens = 0
		}
		postPruneRatio := float64(postPruneFullWindowTokens) / float64(tokenBudget)
		if postPruneRatio <= threshold {
			// Mirror the !fire branch's bookkeeping: pruning replaced
			// the summary as the reclaim mechanism on this turn.
			e.buildStateMu.Lock()
			e.lastCompactionSummary = nil
			e.buildStateMu.Unlock()
			// Publish a no-summary compaction event so observability
			// surfaces the prune-only fire ("pruned X tool outputs,
			// no summary needed"). OriginalTokens is the pre-prune
			// recent-message count so the event's saved-tokens delta
			// is honest; SummaryTokens is zero because no summary
			// was generated. The publish helper handles negative-
			// delta accounting via the existing overhead path.
			e.publishContextCompactedEvent(sessionID, manifest.ID,
				recentTokens+prunedTokensSaved, "", 0,
				ratioOrForceTrigger(forceTrigger),
				prunedToolOutputs, false)
			return ""
		}
	}

	// H2 memoisation. Hash the cold-range identity; if the session's
	// stored entry matches AND a summary is cached, reuse that summary
	// instead of re-invoking the summariser. Per-session keying: a
	// hash collision between two sessions does not rob session B of
	// its own ContextCompactedEvent and per-session metrics bump.
	//
	// The hash is computed against the PRUNED slice; turns whose
	// prune decision differs (different size threshold met, different
	// protected names) produce different hashes and re-fire.
	currentHash := coldRangeHash(recent)
	if reused, hit := e.reuseMemoisedSummary(sessionID, currentHash, recentTokens); hit {
		return reused
	}

	// Anchored iterative summarisation (Feature 1): when a prior summary
	// exists for this session, use CompactExtend instead of Compact to
	// avoid re-summarising the full cold range from scratch. The prior
	// summary was cached from the most recent compaction turn; CompactExtend
	// passes it to the summariser as context alongside the current messages,
	// so the model only needs to extend rather than regenerate.
	start := time.Now()
	priorSummary := e.getPriorCompactionSummary(sessionID)
	var summary ctxstore.CompactionSummary
	var err error
	if priorSummary != nil {
		slog.Debug("engine auto-compaction: using anchored iterative extend",
			"sessionID", sessionID,
			"priorIntent", priorSummary.Intent,
		)
		summary, err = e.autoCompactor.CompactExtend(ctx, *priorSummary, recent)
	} else {
		slog.Debug("engine auto-compaction: using full summarisation (no prior summary)",
			"sessionID", sessionID,
		)
		summary, err = e.autoCompactor.Compact(ctx, recent)
	}
	if err != nil {
		slog.Warn("engine auto-compaction failed; applying naive truncation fallback",
			"error", err,
			"recentTokens", recentTokens,
			"tokenBudget", tokenBudget,
			"threshold", threshold,
		)
		return "[truncation fallback: the conversation summariser was unavailable so older messages were dropped. Use recall_search or re-read files if you need earlier context.]"
	}
	latency := time.Since(start)

	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		slog.Warn("engine auto-compaction produced unmarshallable summary", "error", err)
		return ""
	}

	summaryCopy := summary
	e.buildStateMu.Lock()
	e.lastCompactionSummary = &summaryCopy
	e.sessionCompactionMemo[sessionID] = sessionCompactionMemoEntry{
		hash:    currentHash,
		summary: &summaryCopy,
	}
	// H1 — a fresh compaction produces a new summary with its own
	// FilesToRestore. Clear the consumed flag so buildContextWindow
	// knows to rehydrate against this new summary on the next turn.
	delete(e.sessionRehydrated, sessionID)
	e.buildStateMu.Unlock()

	summaryText := "[auto-compacted summary]: " + string(summaryJSON)
	// Determine the trigger discriminant. forceTrigger wins when the
	// force-fire path drove the decision — that's the cause attribution
	// the operator wants. Empty force-trigger means the ratio tier was
	// the deciding voice; stamp "ratio" so subscribers can distinguish
	// the soft-heuristic fire from the hard force tiers.
	e.publishContextCompactedEvent(sessionID, manifest.ID, recentTokens, summaryText, latency,
		ratioOrForceTrigger(forceTrigger),
		prunedToolOutputs, true)
	return summaryText
}

// NaiveTruncateMessages keeps the system prompt and the most recent keep messages,
// replacing the dropped middle with a single placeholder message.
//
// Expected: parameters for NaiveTruncateMessages.
// Returns: result of NaiveTruncateMessages.
// Side effects: None.
func (e *Engine) NaiveTruncateMessages(messages []provider.Message, keep int) []provider.Message {
	if len(messages) == 0 || len(messages) <= keep+1 {
		return messages
	}
	if keep < 0 {
		keep = 0
	}
	start := len(messages) - keep
	if start < 1 {
		start = 1
	}
	truncated := make([]provider.Message, 0, keep+2)
	truncated = append(truncated, messages[0])
	truncated = append(truncated, provider.Message{
		Role:    "assistant",
		Content: "[... earlier messages truncated — summariser unavailable ...]",
	})
	truncated = append(truncated, messages[start:]...)
	return truncated
}

// maybeAutoCompactExplicit is the explicit-messages variant of
// maybeAutoCompact, introduced for CompactNow's session-resolution
// path. When explicitMessages is nil it delegates verbatim to
// maybeAutoCompact (the store-driven legacy path). When non-nil it
// uses explicitMessages as the authoritative transcript instead of
// e.store.GetRecent — going around the global store entirely so a
// /compact slash command against session A operates on A's own
// messages even if the store currently holds session B's tail
// (the bug that masked /compact from ever firing in production
// against post-restart sessions — see CompactNow's docstring).
//
// The force-fire path is the only consumer right now (forceTrigger
// carries "manual" whenever CompactNow drives this); the
// fullWindowTokens scope, ratio compare, and Stage-1 prune short-
// circuit are NOT exercised because manual /compact opts out of
// those by construction. Keeping the same surface as maybeAutoCompact
// future-proofs adding a ratio-driven per-session caller later
// without re-introducing the global-store coupling.
//
// Expected:
//   - explicitMessages is the session's full transcript in
//     chronological order; nil means "fall back to store reads".
//   - manifest carries ContextManagement.SlidingWindowSize used to
//     slice the recent tail (matches autoCompactionCandidates).
//   - forceTrigger is non-empty (CompactNow sets "manual" for forced runs).
//
// Returns:
//   - Same shape as maybeAutoCompact: the summary text on a successful
//     fire, "" otherwise.
//
// Side effects:
//   - Same as maybeAutoCompact: one summariser LLM call on a fire,
//     one ContextCompactedEvent publish, lastCompactionSummary +
//     sessionCompactionMemo update.
func (e *Engine) maybeAutoCompactExplicit(ctx context.Context, sessionID string, manifest *agent.Manifest, tokenBudget int, forceTrigger string, explicitMessages []provider.Message) string {
	if explicitMessages == nil {
		return e.maybeAutoCompact(ctx, sessionID, manifest, tokenBudget, forceTrigger)
	}

	forceFire := forceTrigger != ""
	_, ok := e.autoCompactionThreshold(manifest, tokenBudget)
	if !ok {
		// Feature disabled or preconditions unmet — mirror
		// maybeAutoCompact's bookkeeping so the manual path is
		// observationally indistinguishable from a no-fire on the
		// store path (operator opt-out via AutoCompaction.Enabled is
		// sticky regardless of which driver invoked us).
		e.buildStateMu.Lock()
		e.lastCompactionSummary = nil
		e.buildStateMu.Unlock()
		return ""
	}

	if !forceFire {
		// The explicit-message path is currently only reached via the
		// manual /compact force-trigger. Defending the branch keeps a
		// future ratio-driven caller from silently no-op'ing when the
		// ratio compare would need a full-window count we don't
		// compute here.
		return ""
	}

	slidingWindowSize := manifest.ContextManagement.SlidingWindowSize
	if slidingWindowSize <= 0 {
		slidingWindowSize = 50
	}
	recent := explicitMessages
	if len(recent) > slidingWindowSize {
		recent = recent[len(recent)-slidingWindowSize:]
	}
	if len(recent) == 0 {
		// Defensive — CompactNow already guards on empty input but
		// keep the floor so a future caller passing an empty slice
		// can't crash through the prune helper below.
		return ""
	}
	var recentTokens int
	for i := range recent {
		recentTokens += e.tokenCounter.Count(recent[i].Content)
	}

	// Stage-1 prune (mirrors maybeAutoCompact's path verbatim — see
	// that function's docstring for the prune contract). The force-
	// fire path skips the prune-only short-circuit because manual
	// callers explicitly want a summary regardless of what pruning
	// saved.
	prunedRecent, prunedToolOutputs, prunedTokensSaved := pruneOldToolOutputs(recent, e.tokenCounter)
	recent = prunedRecent
	recentTokens -= prunedTokensSaved
	if recentTokens < 0 {
		recentTokens = 0
	}

	// H2 memoisation — same per-session keying as maybeAutoCompact.
	currentHash := coldRangeHash(recent)
	if reused, hit := e.reuseMemoisedSummary(sessionID, currentHash, recentTokens); hit {
		return reused
	}

	// Anchored iterative summarisation: use CompactExtend when a prior
	// summary exists for this session to avoid re-summarising from scratch.
	start := time.Now()
	priorSummary := e.getPriorCompactionSummary(sessionID)
	var summary ctxstore.CompactionSummary
	var err error
	if priorSummary != nil {
		slog.Debug("engine manual compaction: using anchored iterative extend",
			"sessionID", sessionID,
			"priorIntent", priorSummary.Intent,
		)
		summary, err = e.autoCompactor.CompactExtend(ctx, *priorSummary, recent)
	} else {
		slog.Debug("engine manual compaction: using full summarisation (no prior summary)",
			"sessionID", sessionID,
		)
		summary, err = e.autoCompactor.Compact(ctx, recent)
	}
	if err != nil {
		slog.Warn("engine manual compaction failed; applying naive truncation fallback",
			"error", err,
			"sessionID", sessionID,
			"recentTokens", recentTokens,
			"tokenBudget", tokenBudget,
		)
		return "[truncation fallback: the conversation summariser was unavailable so older messages were dropped. Use recall_search or re-read files if you need earlier context.]"
	}
	latency := time.Since(start)

	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		slog.Warn("engine manual compaction produced unmarshallable summary",
			"error", err,
			"sessionID", sessionID,
		)
		return ""
	}

	summaryCopy := summary
	e.buildStateMu.Lock()
	e.lastCompactionSummary = &summaryCopy
	e.sessionCompactionMemo[sessionID] = sessionCompactionMemoEntry{
		hash:    currentHash,
		summary: &summaryCopy,
	}
	// H1 — a fresh compaction produces a new summary with its own
	// FilesToRestore. Clear the consumed flag so buildContextWindow
	// knows to rehydrate against this new summary on the next turn.
	delete(e.sessionRehydrated, sessionID)
	e.buildStateMu.Unlock()

	summaryText := "[auto-compacted summary]: " + string(summaryJSON)
	e.publishContextCompactedEvent(sessionID, manifest.ID, recentTokens, summaryText, latency,
		ratioOrForceTrigger(forceTrigger),
		prunedToolOutputs, true)
	return summaryText
}

// ratioOrForceTrigger maps the maybeAutoCompact-internal forceTrigger
// string to the closed-vocabulary discriminant stamped on the
// ContextCompactedEvent. Empty force-trigger means the ratio tier
// drove the decision; the explicit string preserves the cause
// attribution operators want.
//
// Expected: parameters for ratioOrForceTrigger.
// Returns: result of ratioOrForceTrigger.
// Side effects: None.
func ratioOrForceTrigger(forceTrigger string) string {
	if forceTrigger == "" {
		return "ratio"
	}
	return forceTrigger
}

// reuseMemoisedSummary looks up the per-session H2 memo and returns a
// previously-produced summary text when the cold-range hash matches
// the cached entry. Extracted from maybeAutoCompact so the funlen gate
// on the trigger stays comfortably green and the reuse policy
// (no event re-emission, no metrics re-bump) is one self-contained
// block.
//
// Expected:
//   - sessionID identifies the active session (keys the memo map).
//   - currentHash is coldRangeHash(recent) for the turn being built.
//   - recentTokens is carried only for logging on the marshal-failure
//     branch.
//
// Returns:
//   - (summaryText, true) on a memo hit; the caller should return
//     summaryText verbatim.
//   - ("", false) on a miss OR on a hit whose remarshal failed (fall
//     through to fresh compaction in the caller — the threshold says
//     a summary is wanted).
//
// Side effects:
//   - Updates e.lastCompactionSummary on hit so the engine-level
//     pointer stays consistent with the summary injected into the
//     assembled window.
//   - Logs a warning on remarshal failure.
func (e *Engine) reuseMemoisedSummary(sessionID string, currentHash [32]byte, recentTokens int) (string, bool) {
	e.buildStateMu.Lock()
	cached, hit := e.sessionCompactionMemo[sessionID]
	e.buildStateMu.Unlock()
	if !hit || cached.summary == nil || cached.hash != currentHash {
		return "", false
	}
	summaryJSON, err := json.Marshal(*cached.summary)
	if err != nil {
		// Marshal failure on a previously-marshalled struct is a
		// programming error; signal a miss so the caller falls through
		// to a fresh compaction rather than returning "" — returning
		// empty would assemble a window without the summary the
		// threshold says we want.
		slog.Warn("engine auto-compaction memo remarshal failed; refreshing",
			"error", err,
			"recentTokens", recentTokens,
		)
		return "", false
	}
	e.buildStateMu.Lock()
	e.lastCompactionSummary = cached.summary
	e.buildStateMu.Unlock()
	return "[auto-compacted summary]: " + string(summaryJSON), true
}

// getPriorCompactionSummary retrieves the most recent successful compaction
// summary for the given session from the per-session memoisation cache.
// Returns nil when no prior summary exists (first compaction for this
// session, or memo was evicted on session end).
//
// The returned summary is safe to use for anchored iterative compaction
// (CompactExtend) — it represents the summariser's last view of this
// session's conversation before the current message burst.
//
// Expected:
//   - sessionID identifies the active session.
//
// Returns:
//   - A pointer to the prior CompactionSummary, or nil if none exists.
//
// Side effects:
//   - None. Read-only access under buildStateMu.RLock.
func (e *Engine) getPriorCompactionSummary(sessionID string) *ctxstore.CompactionSummary {
	e.buildStateMu.Lock()
	cached, hit := e.sessionCompactionMemo[sessionID]
	e.buildStateMu.Unlock()
	if !hit || cached.summary == nil {
		return nil
	}
	return cached.summary
}

// maybeRehydrate resolves the FilesToRestore listed on the session's
// current CompactionSummary and returns a new message slice with the
// file contents inserted just before the trailing user turn, or
// before the tail if no user turn is present.
//
// Consume-once semantics: the first call after a fresh compaction
// reads the files and sets the sessionRehydrated flag; subsequent
// builds that see the same summary skip the disk I/O and return msgs
// unchanged. The flag clears when a new compaction produces a new
// summary (see maybeAutoCompact) and when the session ends.
//
// Graceful degradation on missing files: the audit flagged re-read
// of moved/deleted files as a real risk. A read failure on any
// listed path logs a warning and skips that file; the rest of the
// rehydration still fires. The build never aborts.
//
// Expected:
//   - sessionID identifies the active session.
//   - msgs is the already-assembled window, including the trailing
//     user turn the Summary path appends via appendUserMessageToResult.
//
// Returns:
//   - msgs unchanged when rehydration is not applicable (no summary,
//     no autoCompactor, no FilesToRestore, already consumed).
//   - A new slice with one provider.Message per rehydrated file
//     inserted before the trailing user turn, when applicable.
//
// Side effects:
//   - One os.ReadFile per listed path on the consume turn.
//   - Sets e.sessionRehydrated[sessionID] on a successful rehydration.
func (e *Engine) maybeRehydrate(sessionID string, msgs []provider.Message) []provider.Message {
	if e.autoCompactor == nil {
		return msgs
	}
	e.buildStateMu.Lock()
	summary := e.lastCompactionSummary
	_, consumed := e.sessionRehydrated[sessionID]
	e.buildStateMu.Unlock()
	if summary == nil || consumed || len(summary.FilesToRestore) == 0 {
		return msgs
	}

	rehydrated, err := e.autoCompactor.Rehydrate(*summary)
	if err != nil {
		// Rehydrate's all-or-nothing contract returns on first
		// read failure. The engine relaxes that into best-effort:
		// we log the failure and fall back to per-file reads so a
		// single missing entry does not rob the turn of the rest.
		slog.Warn("engine rehydration failed; falling back to per-file reads",
			"session_id", sessionID,
			"error", err,
		)
		rehydrated = e.rehydrateBestEffort(sessionID, summary)
	}

	// Mark consumed even on a best-effort path — re-reading next
	// turn will not make missing files suddenly present, and re-
	// reading present files duplicates the content in-window.
	e.buildStateMu.Lock()
	e.sessionRehydrated[sessionID] = struct{}{}
	e.buildStateMu.Unlock()

	if len(rehydrated) == 0 {
		return msgs
	}
	return insertBeforeUserTurn(msgs, rehydrated)
}

// applyMicroCompaction runs the RLM Phase A compactor on the in-flight
// message slice. When the compactor is nil (feature disabled or
// mis-configured at construction), msgs is returned unchanged.
//
// Expected:
//   - ctx is the request context; cancellation aborts compaction with
//     a fall-through to the original slice.
//   - sessionID identifies the session whose cold storage receives any
//     spilled .txt payloads. An empty sessionID is treated as a
//     no-op for safety.
//   - msgs is the finalised provider request slice from
//     assembleBuildResult and maybeRehydrate.
//
// Returns:
//   - The compacted slice on success.
//   - The original slice on compactor failure (the engine prefers a
//     full window over a half-rewritten one — Phase A is best-effort).
//
// Side effects:
//   - May write per-message .txt payloads under
//     <CompactionStoreDir>/<sessionID>/compacted/.
//   - Logs a warning when Compact returns an error; never panics.
func (e *Engine) applyMicroCompaction(ctx context.Context, sessionID string, msgs []provider.Message) []provider.Message {
	if e.microCompactor == nil || sessionID == "" || len(msgs) == 0 {
		return msgs
	}
	out, err := e.microCompactor.Compact(ctx, sessionID, msgs)
	if err != nil {
		slog.Warn("engine micro-compaction failed; using full window",
			"session_id", sessionID,
			"error", err,
		)
		return msgs
	}
	return out
}

// rehydrateBestEffort applyFactRecall asks the Phase B service for the top-K facts most relevant to userMessage and splices a single "[recalled facts]" system message between the system prompt and the rest of msgs.
//
// Expected:
//   - ctx is the request context.
//   - sessionID identifies the session whose facts.jsonl is consulted;
//     an empty sessionID is a no-op.
//   - userMessage is the next user turn — the recall query. Empty
//     queries degrade to "most recent K" inside the store.
//   - msgs is the in-flight slice from assembleBuildResult /
//     maybeRehydrate.
//
// Returns:
//   - msgs with the recall block inserted at index 1 (immediately
//     after the system prompt) when the service yields ≥1 fact.
//   - msgs unchanged when the service is nil, the toggle is off, the
//     session has no facts, or the recall call fails.
//
// Side effects:
//   - May read <sessionsDir>/<sessionID>/facts.jsonl.
//   - Logs a warning on Recall errors; never panics.
//
// rehydrateBestEffort iterates FilesToRestore and reads each in turn,
// logging missing entries and returning the subset that was readable.
// Used when AutoCompactor.Rehydrate's all-or-nothing contract trips
// on a single missing file but the engine wants to continue with the
// readable remainder.
//
// Expected:
//   - summary carries FilesToRestore the caller has already
//     confirmed is non-empty.
//
// Returns:
//   - A slice of provider.Message (one per successfully-read file,
//     plus a system anchor message matching Rehydrate's shape).
//
// Side effects:
//   - os.ReadFile per path; slog.Warn on per-file failures.
func (e *Engine) rehydrateBestEffort(sessionID string, summary *ctxstore.CompactionSummary) []provider.Message {
	msgs := make([]provider.Message, 0, 1+len(summary.FilesToRestore))
	msgs = append(msgs, provider.Message{
		Role:    "system",
		Content: "Session rehydrated. Continuing from: " + summary.Intent,
	})
	for _, path := range summary.FilesToRestore {
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("engine rehydration: file unreadable, skipping",
				"session_id", sessionID, "path", path, "error", err)
			continue
		}
		msgs = append(msgs, provider.Message{
			Role:    "tool",
			Content: string(data),
		})
	}
	if len(msgs) == 1 {
		// Only the anchor with no files — no point injecting.
		return nil
	}
	return msgs
}

// insertBeforeUserTurn splices rehydrated messages into msgs just
// before the trailing user turn when one exists; appends to the end
// otherwise. Keeps the injected content in the natural position —
// tool contexts sit ahead of the user's current turn, not after it.
//
// Expected:
//   - msgs is non-nil.
//   - rehydrated is non-empty.
//
// Returns:
//   - A new slice with rehydrated messages inserted.
//
// Side effects:
//   - None. Allocates a new slice.
func insertBeforeUserTurn(msgs, rehydrated []provider.Message) []provider.Message {
	idx := len(msgs)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			idx = i
			break
		}
	}
	out := make([]provider.Message, 0, len(msgs)+len(rehydrated))
	out = append(out, msgs[:idx]...)
	out = append(out, rehydrated...)
	out = append(out, msgs[idx:]...)
	return out
}

// toolOutputPruneCharLimit is the per-tool-result truncation ceiling
// applied by the Stage-1 prune pass. Tool-result messages whose Content
// exceeds this length are cut to the limit and stamped with a
// "[...truncated, N tokens]" sentinel so the remaining context still
// signals what was there.
//
// 2000 chars ≈ 500-700 tokens depending on the counter; the figure is
// borrowed from OpenCode's pruning policy (port reference: May 2026
// OpenCode-shape auto-compact rename bundle) and is intentionally
// generous — the prune pass is the cheap layer that runs BEFORE the
// LLM summariser, so it errs on the side of preserving signal.
const toolOutputPruneCharLimit = 2000

// toolOutputPruneTailGuard is the count of trailing tool-RESULT
// messages the Stage-1 prune pass leaves untouched (counted by
// tool-result message, not by raw slice index). The model's live
// work happens at the tail of the sliding window; pruning the most
// recent tool outputs would defeat the point of having them in
// context at all.
//
// 1 is conservative: only the most recent tool-result message is
// guaranteed to stay intact. Anything older is fair game for the
// size + name gates. The figure can grow if observability shows the
// model losing context on the second-most-recent tool result;
// growing it must come with a behaviour pin in
// auto_compaction_trigger_test.go.
const toolOutputPruneTailGuard = 1

// protectedCompactionToolNames is the closed set of tool names whose
// outputs the Stage-1 prune pass MUST leave intact even when they
// exceed toolOutputPruneCharLimit. These are tools whose results are
// load-bearing for downstream agents or for plan correctness — a
// truncated plan_write return, for example, drops the persisted plan
// ID downstream readers need. delegate results carry the entire
// sub-agent output (up to 100 KB); truncating to 2 KB silently
// discards the sub-agent's work. bash output frequently contains
// compilation errors or test results whose tail holds the root cause;
// truncating to the first 2 KB causes the agent to "fix" visible
// errors while the real failure remains invisible.
//
// Option (b) (per the May 2026 rename brief) — protect by tool name
// list. The simpler choice over a `Tool.PreserveOnCompact() bool`
// interface marker because:
//   - The set is small and stable (the load-bearing tools are well
//     known).
//   - Adding the marker to the Tool interface would touch every tool
//     implementation in the registry; a name list keeps the policy
//     in one place at the prune site.
//   - The tool-result message ALREADY carries the originating
//     tool name via ToolCalls[0].Name (see engine/tool_call_test.go
//     L1181) so the lookup is a pure read against existing data.
//
// Subscribers who want a more flexible protection rule can grow this
// into a Tool-interface marker later without changing the prune
// surface — this name set then becomes the default for tools that
// did not opt in.
var protectedCompactionToolNames = map[string]struct{}{
	"plan_write":       {},
	"recall_search":    {},
	"question_request": {},
	"delegate":         {},
	"bash":             {},
}

// pruneOldToolOutputs runs the Stage-1 prune pass over the cold-range
// slice that the L2 auto-compactor is about to summarise. Tool-result
// messages whose Content exceeds toolOutputPruneCharLimit are
// truncated to that ceiling and stamped with a token-count sentinel.
//
// Tail-guard: the last toolOutputPruneTailGuard TOOL-RESULT messages
// are left untouched (counted by tool-result, NOT by raw slice
// index — the live work tail may be assistant/user messages between
// tool calls and a fixed slice-index guard would protect the wrong
// rows). The most recent tool result is the live one the model is
// mid-reasoning over; truncating it defeats the prune's "cheap
// pre-summary reclaim" rationale.
//
// Name-guard: tool-result messages whose originating tool name is in
// protectedCompactionToolNames are left intact regardless of size —
// their content is load-bearing for downstream agents.
//
// The returned slice is a fresh copy; the input is NOT mutated. That
// means the H2 memoisation hash naturally distinguishes a turn that
// was pruned from one that was not (the pruned Content bytes differ),
// preserving the H2 invariant that identical inputs reuse the cached
// summary and changed inputs re-fire.
//
// Expected:
//   - recent is the cold-range slice from autoCompactionCandidates.
//   - counter is the engine's TokenCounter; used to estimate the
//     dropped-token figure for the sentinel and the tokensSaved
//     return.
//
// Returns:
//   - out: the pruned copy (same length, same order; only Content of
//     eligible tool-result messages changes).
//   - prunedCount: how many messages had their Content truncated.
//   - tokensSaved: estimated token reclaim from the prune pass; sum
//     across all truncated messages of (original-tokens - kept-tokens).
//
// Side effects:
//   - None. Pure function (does not touch the engine, store, or bus).
func pruneOldToolOutputs(recent []provider.Message, counter ctxstore.TokenCounter) ([]provider.Message, int, int) {
	if len(recent) == 0 {
		return recent, 0, 0
	}
	out := make([]provider.Message, len(recent))
	copy(out, recent)
	if counter == nil {
		return out, 0, 0
	}
	// First pass: count tool-result messages and identify the index
	// of the toolOutputPruneTailGuard-th most recent tool result.
	// Everything at or after this index is guarded. Iterate from the
	// end so we can stop as soon as the guard quota is filled.
	guardStopIndex := len(out)
	{
		seen := 0
		for i := len(out) - 1; i >= 0; i-- {
			if out[i].Role != "tool" {
				continue
			}
			seen++
			if seen >= toolOutputPruneTailGuard {
				guardStopIndex = i
				break
			}
		}
	}

	var (
		prunedCount int
		tokensSaved int
	)
	for i := 0; i < guardStopIndex; i++ {
		m := &out[i]
		if m.Role != "tool" {
			continue
		}
		if len(m.Content) <= toolOutputPruneCharLimit {
			continue
		}
		// Name-guard: read the originating tool name from the
		// tool-result message's own ToolCalls slice. The engine
		// stamps the name at construction time (see
		// internal/engine/tool_call_test.go L1181 for the
		// invariant); a tool-result message without a ToolCalls
		// entry is unusual but treated as "unknown" — fall through
		// to the size guard rather than crashing.
		if len(m.ToolCalls) > 0 {
			if _, protected := protectedCompactionToolNames[m.ToolCalls[0].Name]; protected {
				continue
			}
		}
		originalTokens := counter.Count(m.Content)
		truncated := m.Content[:toolOutputPruneCharLimit]
		droppedTokens := counter.Count(m.Content[toolOutputPruneCharLimit:])
		m.Content = truncated + fmt.Sprintf("\n[...truncated, %d tokens]", droppedTokens)
		keptTokens := counter.Count(m.Content)
		prunedCount++
		tokensSaved += originalTokens - keptTokens
		if tokensSaved < 0 {
			// Defensive: sentinel inflation must never report
			// negative savings. Zero is the honest figure when the
			// truncated form costs more tokens than the original.
			tokensSaved = 0
		}
	}
	return out, prunedCount, tokensSaved
}

// coldRangeHash produces a deterministic SHA-256 of the given message
// slice in a form that distinguishes any semantic change: role,
// content, ModelID, tool-call IDs, and tool-call arguments all
// contribute. Stable across runs (no map iteration, no time values)
// so the H2 memoisation decision is reproducible.
//
// Expected:
//   - recent is the cold-range slice passed to autoCompactor.Compact.
//
// Returns:
//   - A 32-byte hash that changes whenever the slice's observable
//     content changes and matches byte-for-byte on identical inputs.
//
// Side effects:
//   - None. Pure function.
func coldRangeHash(recent []provider.Message) [32]byte {
	h := sha256.New()
	for i := range recent {
		m := &recent[i]
		_, _ = h.Write([]byte(m.Role))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(m.Content))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(m.ModelID))
		_, _ = h.Write([]byte{0})
		for j := range m.ToolCalls {
			tc := &m.ToolCalls[j]
			_, _ = h.Write([]byte(tc.ID))
			_, _ = h.Write([]byte{0})
			_, _ = h.Write([]byte(tc.Name))
			_, _ = h.Write([]byte{0})
			// Arguments is a map — iterate the keys sorted so the
			// hash does not flap on map-iteration order. Keys are
			// small strings so the sort cost is negligible.
			if len(tc.Arguments) > 0 {
				argsJSON, err := json.Marshal(tc.Arguments)
				if err == nil {
					_, _ = h.Write(argsJSON)
				}
				_, _ = h.Write([]byte{0})
			}
		}
		_, _ = h.Write([]byte{0, 1}) // message separator
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// autoCompactionThreshold returns the configured auto-compaction ratio
// threshold when every prerequisite for compaction is met. The second
// return value is false when the feature is disabled or a dependency is
// missing, so the caller can short-circuit without inspecting fields
// individually.
//
// Precedence (H3 audit — per-agent override):
//
//   - manifest.ContextManagement.CompactionThreshold when > 0 — the
//     per-agent override configured in the agent manifest.
//   - e.compressionConfig.AutoCompaction.Threshold otherwise — the
//     global configuration fallback.
//
// Callers supplying a manifest with CompactionThreshold == 0 inherit
// the global, which is what tests and agents that have not opted in
// want. A negative manifest value is rejected at manifest load; a
// negative global is rejected at config load; this function trusts
// both invariants and only range-checks the final resolved value as
// defence in depth.
//
// Expected:
//   - manifest is the active agent manifest (non-nil — the caller
//     buildContextWindow hands in a prepared copy on this path).
//   - tokenBudget is the model context limit passed through from
//     buildContextWindow.
//
// Returns:
//   - (threshold, true) when the feature is enabled, the AutoCompactor
//     is wired, the store and counter are present, tokenBudget is
//     positive, and the resolved threshold is positive.
//   - (0, false) otherwise.
//
// Side effects:
//   - None.
func (e *Engine) autoCompactionThreshold(manifest *agent.Manifest, tokenBudget int) (float64, bool) {
	if e.autoCompactor == nil || !e.compressionConfig.AutoCompaction.Enabled {
		return 0, false
	}
	if e.store == nil || e.tokenCounter == nil {
		return 0, false
	}
	if tokenBudget <= 0 {
		return 0, false
	}
	threshold := e.compressionConfig.AutoCompaction.Threshold
	if manifest != nil && manifest.ContextManagement.CompactionThreshold > 0 {
		threshold = manifest.ContextManagement.CompactionThreshold
	}
	if threshold <= 0 {
		return 0, false
	}
	return threshold, true
}

// autoCompactionCandidates pulls the recent-message slice from the
// store, counts its tokens, and decides whether the load crosses the
// threshold. Split out of maybeAutoCompact so the decision logic can
// be unit-tested independently of the LLM call and so the trigger
// function stays inside the funlen gate.
//
// Slice 6a (Phase 4 follow-ups) added the forceFire signal so the
// gate-proximity tier — computed in buildContextWindow against the
// full assembled-request token estimate — can OR onto the existing
// ratio decision. When forceFire is true, the only remaining guard
// is the "have content to summarise" check; the ratio threshold is
// skipped.
//
// Expected:
//   - manifest carries ContextManagement to pick the sliding window size.
//   - tokenBudget is the model context limit.
//   - threshold is the ratio above which compaction fires.
//   - forceFire is true when an external signal (Slice 6a's gate-
//     proximity check) demands compaction regardless of the ratio.
//
// Returns:
//   - recent: the recent-message slice counted against the budget.
//   - recentTokens: sum of token counts for those messages.
//   - fullWindowTokens: token count across e.store.AllMessages() (the
//     scope the chip and the proactive gate use). Returned regardless
//     of fire so the Stage-1 prune pass in maybeAutoCompact can
//     re-check the ratio after truncating tool outputs without
//     re-iterating the store. Zero when forceFire short-circuits the
//     full-window count.
//   - fire: true when (ratio > threshold OR forceFire) and there is
//     content to summarise; false when compaction should be skipped.
//
// Side effects:
//   - None.
func (e *Engine) autoCompactionCandidates(ctx context.Context, manifest *agent.Manifest, tokenBudget int, threshold float64, forceFire bool) ([]provider.Message, int, int, bool) {
	slidingWindowSize := manifest.ContextManagement.SlidingWindowSize
	if slidingWindowSize <= 0 {
		slidingWindowSize = 50
	}
	recent := e.store.GetRecent(slidingWindowSize)
	if len(recent) == 0 {
		return nil, 0, 0, false
	}
	var recentTokens int
	for i := range recent {
		recentTokens += e.tokenCounter.Count(recent[i].Content)
	}
	if forceFire {
		return recent, recentTokens, 0, true
	}
	// Bug Hunt (May 2026) — soft trigger measures the FULL persisted
	// window, not the sliding-window subset. Pre-fix the ratio
	// compared `recentTokens` (sliding window — default 10 messages)
	// against `tokenBudget` (the model context limit). That was a
	// category error: the ContextUsageChip displays the full-request
	// estimate (via buildContextUsagePayload → estimateRequestTokens
	// over e.store.AllMessages()) and the proactive overflow gate
	// uses the same full-request scope. So a 50-message session
	// sitting at 90% on the chip stayed silent because the recent 10
	// messages alone were comfortably under the 0.75 threshold; the
	// configured ratio knob never matched what the user was looking
	// at. Slice 6a's gate-proximity tier masked the divergence by
	// force-firing near the saturation boundary, but operators tuning
	// the soft threshold expect it to fire against the figure on the
	// chip — not against an arbitrary 10-message subset.
	//
	// The fix: count tokens across e.store.AllMessages() for the
	// threshold compare. The summariser still consumes `recent`
	// (sliding-window slice) — that's a separate decision about what
	// content to summarise — but the trigger decision now uses the
	// same scope the chip and gate use.
	syntheticAll := &provider.ChatRequest{
		Messages: e.store.AllMessages(),
		Tools:    e.assembleToolSchemasLocked(ctx),
	}
	fullWindowTokens := e.estimateRequestTokens(syntheticAll)
	ratio := float64(fullWindowTokens) / float64(tokenBudget)
	if ratio <= threshold {
		return nil, 0, fullWindowTokens, false
	}
	return recent, recentTokens, fullWindowTokens, true
}

// shouldAutoCompactForGate reports whether the proactive saturation
// gate (Phase 1 — checkContextWindowOverflow) would refuse the next
// request within a 5% safety margin of its hard boundary. This is
// Slice 6a's force-trigger source: the L2 ratio threshold is decoupled
// from the gate's actual usable budget, so under heavy single-turn
// loads the gate could refuse a request that the ratio path declined
// to compact. shouldAutoCompactForGate fires compaction *before* the
// gate gets a chance to refuse, leaving the gate as the unconditional
// floor that catches degenerate cases (compaction failure, summary
// still over budget).
//
// Boundary: estimated > limit - reserve - (limit / 20)
//
// The 5% safety margin (limit / 20) gives the compactor headroom to
// produce a summary that still fits under the gate. Without it the
// trigger would fire only when there is no room left for the summary
// itself, defeating the point of compacting.
//
// Degenerate-budget guard: when limit <= reserve + safetyMargin the
// helper returns false. The proactive gate's own clamp (usable < 1 →
// 1) means it would refuse essentially every non-empty request, and
// firing the trigger in that territory would just churn compaction
// against a budget that can never accept the result. The ratio path
// remains the sole signal in that regime — operators see refusals
// loudly via the gate rather than silently via burnt summariser
// tokens.
//
// Expected:
//   - estimated is the prompt-token cost of the assembled request.
//   - limit is the model's resolved context length (matches
//     checkContextWindowOverflow's `limit`).
//   - reserve is the output reserve from outputReserveFor (matches
//     checkContextWindowOverflow's `reserve`).
//
// Returns:
//   - true when compaction should be force-fired.
//   - false when the request comfortably fits OR the budget is
//     degenerate.
//
// Side effects:
//   - None.
func (e *Engine) shouldAutoCompactForGate(estimated, limit, reserve int) bool {
	if limit <= 0 {
		return false
	}
	safetyMargin := limit / 20
	usable := limit - reserve - safetyMargin
	if usable < 1 {
		// Degenerate territory — the gate's own clamp means it will
		// refuse nearly every request. Force-trigger here would just
		// loop the summariser against an unattainable target.
		return false
	}
	return estimated > usable
}

// gateProximityForceCompact composes the gate-proximity decision for
// the current build: pick the preferred provider/model from the
// manifest (so reserve resolves through the same registry pipeline
// the gate uses), synthesise a candidate ChatRequest from the
// persisted store + the current user turn, and ask
// shouldAutoCompactForGate whether the estimated input sits within
// 5% of refusal.
//
// Returns false in any of these conditions (which mirror the gate's
// own no-op cases):
//   - tokenBudget <= 0 (degenerate model resolution).
//   - tokenCounter is nil (cannot estimate).
//   - store is nil (no transcript to compact).
//
// The returned boolean is then ORed into autoCompactionCandidates'
// fire decision via maybeAutoCompact's forceFire parameter.
//
// Expected:
//   - manifest is the per-stream manifest copy maybeAutoCompact will
//     use (provider/model preferences flow through PreferredModels).
//   - userMessage is the in-flight user turn — counted into the
//     estimate so a single turn that pushes through the boundary
//     also forces the trigger.
//   - tokenBudget is the resolved per-model context limit (matches
//     the value passed into maybeAutoCompact).
//
// Returns:
//   - true when the gate-proximity boundary would be crossed.
//   - false when the request fits comfortably OR pre-conditions are
//     unmet.
//
// Side effects:
//   - None.
func (e *Engine) gateProximityForceCompact(manifest *agent.Manifest, userMessage string, tokenBudget int, tools []provider.Tool) bool {
	if e == nil || tokenBudget <= 0 || e.tokenCounter == nil || e.store == nil {
		return false
	}
	prefProvider, prefModel := e.LastProvider(), e.LastModel()
	if prefProvider == "" || prefModel == "" {
		prefProvider, prefModel = preferredProviderModel(manifest)
	}
	allMessages := e.store.AllMessages()
	candidate := make([]provider.Message, 0, len(allMessages)+1)
	candidate = append(candidate, allMessages...)
	if userMessage != "" {
		candidate = append(candidate, provider.Message{Role: "user", Content: userMessage})
	}
	syntheticReq := &provider.ChatRequest{
		Provider: prefProvider,
		Model:    prefModel,
		Messages: candidate,
		Tools:    tools,
	}
	estimated := e.estimateRequestTokens(syntheticReq)
	reserve := e.outputReserveFor(syntheticReq)
	return e.shouldAutoCompactForGate(estimated, tokenBudget, reserve)
}

// shouldCompactExplicitForGate mirrors gateProximityForceCompact but
// operates on an explicit message slice instead of reading from the
// shared e.store. This allows the session-scoped path in
// buildContextWindow (which sources prior messages from context, not
// the engine's process-wide store) to detect when the next request
// would land within 5% of the proactive saturation gate's refusal
// boundary.
//
// The estimate builds a synthetic ChatRequest from the explicit
// messages plus the user turn, then delegates to the same
// shouldAutoCompactForGate helper that gateProximityForceCompact uses.
// The reserve resolves through outputReserveFor with no MaxTokens
// override — matching the production seam where Stream() callers
// seldom set MaxTokens explicitly.
//
// Pre-conditions and no-op cases mirror gateProximityForceCompact:
//   - tokenBudget <= 0 (degenerate model resolution).
//   - tokenCounter is nil (cannot estimate).
//   - explicitMessages is empty (nothing to compact).
//
// Expected:
//   - manifest carries provider/model preferences (PreferredModels)
//     used to resolve the output reserve.
//   - userMessage is the in-flight user turn — counted into the
//     estimate so a single turn that pushes through the boundary
//     also forces the trigger.
//   - tokenBudget is the resolved per-model context limit.
//   - tools are the assembled tool schemas for token estimation.
//   - explicitMessages are the session-scoped prior messages
//     extracted from context.
//
// Returns:
//   - true when the gate-proximity boundary would be crossed.
//   - false when the request fits comfortably OR pre-conditions are
//     unmet.
//
// Side effects:
//   - None.
func (e *Engine) shouldCompactExplicitForGate(manifest *agent.Manifest, userMessage string, tokenBudget int, tools []provider.Tool, explicitMessages []provider.Message) bool {
	if e == nil || tokenBudget <= 0 || e.tokenCounter == nil {
		return false
	}
	if len(explicitMessages) == 0 {
		return false
	}
	prefProvider, prefModel := e.LastProvider(), e.LastModel()
	if prefProvider == "" || prefModel == "" {
		prefProvider, prefModel = preferredProviderModel(manifest)
	}
	candidate := make([]provider.Message, 0, len(explicitMessages)+1)
	candidate = append(candidate, explicitMessages...)
	if userMessage != "" {
		candidate = append(candidate, provider.Message{Role: "user", Content: userMessage})
	}
	syntheticReq := &provider.ChatRequest{
		Provider: prefProvider,
		Model:    prefModel,
		Messages: candidate,
		Tools:    tools,
	}
	estimated := e.estimateRequestTokens(syntheticReq)
	reserve := e.outputReserveFor(syntheticReq)
	return e.shouldAutoCompactForGate(estimated, tokenBudget, reserve)
}

// rebuildContextWindowAfterMidLoopCompaction emitMidToolLoopRefresh runs the Phase-5 Slice γ post-tool-batch affordances: emits a fresh context_usage chunk so the chip ticks up to reflect the just-extended persisted store, AND consults gateProximityForceCompact on the active manifest's persisted history; on a positive verdict, force-fires the auto-compactor with trigger="tool_result_wave" so the next user turn's buildContextWindow injects the freshly-computed summary rather than running the cold path against a swollen prefix.
//
// Called from streamWithToolLoop between
// appendToolResultsBatchToMessages and retryStreamForToolResult.
// processStreamChunks only invokes the postTurnUsage callback on
// terminal Done; without this hook the chip stays stale even after
// a 700KB tool result wave, and retryStreamForToolResult builds a
// fresh ChatRequest that skips buildContextWindow so
// maybeAutoCompact never fires from the tool-loop path either.
//
// Bug #35 — returns compacted=true when the gate-proximity tier
// fired so the caller (streamWithToolLoop) can swap its in-memory
// messages slice for the freshly-rebuilt compacted view via
// rebuildContextWindowAfterMidLoopCompaction. Returning false on
// the no-op path lets the caller skip the (still cheap, but
// pointless) message-reload.
//
// Bug #36 — writes the emitted payload into e.lastUsagePayload[sess]
// so the post-retry hook can detect "nothing changed since the last
// emission" and coalesce a duplicate chunk.
//
// Expected:
//   - ctx carries cancellation/deadline for the (potential) summariser call.
//   - sessionID identifies the active session; threaded through to
//     publishContextCompactedEvent on a fire.
//   - outChan is the engine's stream output channel; the helper writes
//     one context_usage chunk to it when a usage figure can be
//     computed.
//
// Returns:
//   - compacted=true iff gateProximityForceCompact fired and
//     maybeAutoCompact produced a non-empty summary on this call.
//
// Side effects:
//   - Writes one context_usage StreamChunk to outChan (best-effort —
//     suppressed when buildContextUsagePayload reports no figure).
//   - Records the emitted payload in lastUsagePayload[sessionID].
//   - On a positive gate-proximity verdict, fires maybeAutoCompact
//     which can issue one summariser LLM call and publish one
//     ContextCompactedEvent with Trigger="tool_result_wave".
//
// rebuildContextWindowAfterMidLoopCompaction returns the rebuilt
// message slice the next retryStreamForToolResult call should hand to
// the provider after emitMidToolLoopRefresh fired the tool-result-wave
// compaction trigger.
//
// Bug #35 — streamWithToolLoop previously kept its pre-compaction
// in-memory messages slice and handed it straight to
// retryStreamForToolResult. The store carried the persisted history
// and the engine's per-session memo carried the fresh summary, but
// the next provider request inherited the bloated pre-compaction
// view. Calling this helper after a compacted=true signal swaps the
// caller's slice for the compacted view (system prompt + summary +
// recent history).
//
// rebuildContextWindowAfterMidLoopCompaction re-assembles the session
// window after a mid-tool-loop compaction fires via buildContextWindow.
//
// The implementation routes through buildContextWindow with an empty
// user message so the same assembly path that handles user turns
// (recall, system prompt rebuild, micro-compaction) also covers the
// mid-loop reload. maybeAutoCompact's H2 memo means the second call
// reuses the cached summary instead of re-invoking the summariser.
//
// Expected:
//   - ctx is the active stream context.
//   - sessionID identifies the session whose history should be
//     re-assembled.
//
// Returns:
//   - The rebuilt message slice. Returns nil when the engine cannot
//     reassemble (no store, no windowBuilder); the caller should fall
//     back to its pre-fix slice rather than send nothing.
//
// Side effects:
//   - Same as buildContextWindow (publishes context-window events,
//     updates lastContextResult). Acceptable because mid-loop reload
//     is a real assembly cycle the operator wants observability for.
func (e *Engine) rebuildContextWindowAfterMidLoopCompaction(ctx context.Context, sessionID string, messages []provider.Message) []provider.Message {
	if e == nil || e.store == nil || sessionID == "" {
		return nil
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return e.buildContextWindow(ctx, sessionID, messages[i].Content)
		}
	}
	return nil
}

// MaybeCompactForModel resolves the supplied (newProvider, newModel)
// pair through the registry pipeline (ResolveContextLength /
// ResolveOutputLimit) and force-fires the auto-compactor when the
// persisted history estimate would saturate the new model's window.
// Phase-5 Slice α — orchestrator.SwitchModel calls this BEFORE
// engine.SetModelPreference so a switch to a smaller-window model
// cannot strand the next Stream call behind the proactive overflow
// gate's refusal with no auto-recovery.
//
// The trigger threads "model_switch" through publishContextCompactedEvent
// so subscribers (Slice δ's chip tooltip) can attribute the cause
// distinctly from ratio / gate_proximity / tool_result_wave fires.
//
// Expected:
//   - ctx carries cancellation/deadline for the (potential) summariser call.
//   - sessionID identifies the session the switch is happening in;
//     threaded through the emitted event so per-session subscribers
//     (chatStore handleContextCompactedEvent) only see their session's
//     compaction.
//   - newProvider / newModel identify the destination model; resolved
//     via the same ResolveContextLength pipeline the proactive
//     overflow gate uses so the trigger and the gate agree on the
//     same picture of the budget.
//
// Returns:
//   - The summary text ("[auto-compacted summary]: <json>") when
//     compaction fired and succeeded; empty otherwise (degenerate
//     resolution, fits comfortably, feature disabled, summariser
//     error).
//
// No-op cases (return ""):
//   - sessionID empty (no session-scoped trigger to drive),
//   - newProvider/newModel resolves to a non-positive ContextLength
//     (degenerate registry data — refuse to compact against garbage
//     budgets),
//   - the persisted history estimate fits comfortably under the new
//     model's usable window (limit - reserve - 5% safety margin),
//   - maybeAutoCompact's own preconditions reject (compactor nil,
//     enabled=false, empty transcript, summariser error).
//
// Side effects:
//   - One LLM call via the AutoCompactor on the fire path.
//   - Updates e.lastCompactionSummary / sessionCompactionMemo on success.
//   - Publishes a pluginevents.ContextCompactedEvent with
//     Trigger="model_switch" on the engine bus on a successful fire.
func (e *Engine) MaybeCompactForModel(ctx context.Context, sessionID, newProvider, newModel string) string {
	if e == nil || sessionID == "" || e.tokenCounter == nil {
		slog.Debug("engine MaybeCompactForModel: precondition not met",
			"sessionID", sessionID,
			"newProvider", newProvider,
			"newModel", newModel,
			"reason", "engine nil, sessionID empty, or tokenCounter nil",
		)
		return ""
	}

	slog.Debug("engine MaybeCompactForModel: entry",
		"sessionID", sessionID,
		"newProvider", newProvider,
		"newModel", newModel,
	)

	// Resolve session-scoped messages when a SessionLookup is wired
	// (production path). Without this, e.store.AllMessages() reads
	// from the process-wide shared store that mixes every session's
	// history together — the same isolation bug that CompactNow had
	// before its May 2026 fix (see SnapshotForCompaction).
	e.mu.RLock()
	lookup := e.sessionLookup
	e.mu.RUnlock()

	var explicitMessages []provider.Message
	manifest := e.Manifest()
	tokenBudget := 0

	if lookup != nil {
		messages, agentID, providerID, modelID, ok := lookup.SnapshotForCompaction(sessionID)
		if !ok || len(messages) == 0 {
			slog.Debug("engine MaybeCompactForModel: no-op, no session messages",
				"sessionID", sessionID,
				"newProvider", newProvider,
				"newModel", newModel,
			)
			return ""
		}
		explicitMessages = messages

		// Resolve per-session manifest so the summariser sees the
		// session's agent — not whatever happens to live on
		// e.manifest at the moment a concurrent SetManifest fires.
		if agentID != "" && e.agentRegistry != nil {
			if resolved, found := e.agentRegistry.Get(agentID); found && resolved != nil {
				manifest = *resolved
			}
		}

		// Resolve token budget: prefer the destination model's
		// window; fall back to the session's current provider/model.
		tokenBudget = e.ResolveContextLength(newProvider, newModel)
		if tokenBudget <= 0 && providerID != "" && modelID != "" {
			tokenBudget = e.ResolveContextLength(providerID, modelID)
		}
	} else {
		// Legacy / unit-test path: no SessionLookup wired. Fall
		// back to the shared store so existing tests that seed the
		// store directly continue to pass unchanged.
		if e.store == nil {
			slog.Debug("engine MaybeCompactForModel: no-op, no store wired",
				"sessionID", sessionID,
				"newProvider", newProvider,
				"newModel", newModel,
			)
			return ""
		}
		tokenBudget = e.ResolveContextLength(newProvider, newModel)
	}

	if tokenBudget <= 0 {
		slog.Debug("engine MaybeCompactForModel: no-op, tokenBudget <= 0",
			"sessionID", sessionID,
			"newProvider", newProvider,
			"newModel", newModel,
		)
		return ""
	}

	// Build the candidate request for the gate-proximity check.
	// Uses either the session-scoped explicit messages (production)
	// or the shared store (legacy).
	allMessages := explicitMessages
	if allMessages == nil {
		allMessages = e.store.AllMessages()
	}
	syntheticReq := &provider.ChatRequest{
		Provider: newProvider,
		Model:    newModel,
		Messages: allMessages,
	}
	estimated := e.estimateRequestTokens(syntheticReq)
	reserve := e.outputReserveFor(syntheticReq)

	// Same boundary the gate-proximity tier uses: fire when the
	// estimate would land within the proactive overflow gate's
	// 5% safety margin of refusal on the new window.
	if !e.shouldAutoCompactForGate(estimated, tokenBudget, reserve) {
		slog.Debug("engine MaybeCompactForModel: no-op, within gate safety margin",
			"sessionID", sessionID,
			"newProvider", newProvider,
			"newModel", newModel,
			"estimatedTokens", estimated,
			"tokenBudget", tokenBudget,
			"outputReserve", reserve,
		)
		return ""
	}

	slog.Info("engine MaybeCompactForModel: firing compaction on model switch",
		"sessionID", sessionID,
		"newProvider", newProvider,
		"newModel", newModel,
		"estimatedTokens", estimated,
		"tokenBudget", tokenBudget,
		"outputReserve", reserve,
	)

	// Attach the destination model so the summariser routes against
	// the correct provider (mirrors CompactNow's WithSessionModel).
	ctx = WithSessionModel(ctx, newProvider, newModel)

	if explicitMessages != nil {
		return e.maybeAutoCompactExplicit(ctx, sessionID, &manifest, tokenBudget, "model_switch", explicitMessages)
	}
	return e.maybeAutoCompact(ctx, sessionID, &manifest, tokenBudget, "model_switch")
}

// CompactNow is the engine seam the /compress slash command and the
// POST /api/v1/sessions/{id}/compress endpoint wire to. It
// force-fires the L2 auto-compactor against the session's full
// persisted history regardless of the configured ratio threshold or
// the gate-proximity boundary — the operator is explicitly asking
// for a buy-back of context budget right now.
//
// The AutoCompaction.Enabled flag is still honoured: a disabled layer
// cannot be conjured back into life by a slash command. That keeps
// the operator's deliberate opt-out sticky and avoids a confusing
// "feature disabled but somehow fired" failure mode.
//
// On a successful fire the helper publishes a ContextCompactedEvent
// with Trigger="manual" on the engine bus; the existing api SSE
// bridge forwards it as a `context_compacted` chunk so the chip's
// flash + tooltip pick up the manual trigger via the same path the
// automatic tiers use.
//
// Expected:
//   - ctx carries cancellation/deadline for the summariser LLM call.
//   - sessionID identifies the active session; empty string is a
//     no-op (no transcript to compact against).
//
// Returns:
//   - (summary, true) on a successful fire — the summary text
//     ("[auto-compacted summary]: <json>") is suitable for the
//     chat UI's confirmation toast.
//   - ("", false) when the layer is disabled, the store is empty,
//     the engine has no AutoCompactor wired, or the summariser
//     errored out.
//
// Side effects:
//   - One summariser LLM call via the wired AutoCompactor on a fire.
//   - One ContextCompactedEvent published on the engine bus on a fire.
//   - Updates lastCompactionSummary / sessionCompactionMemo on success.
func (e *Engine) CompactNow(ctx context.Context, sessionID string) (string, bool) {
	if e == nil || sessionID == "" {
		return "", false
	}
	if e.tokenCounter == nil || e.store == nil {
		return "", false
	}

	// Defaults: engine-level manifest + engine-level model context limit.
	// These are the legacy "compact whatever the engine currently looks
	// like" knobs — preserved so existing engine unit tests (which seed
	// the store directly and never wire SessionLookup) keep working.
	manifest := e.Manifest()
	tokenBudget := e.ModelContextLimit()

	// When a SessionLookup is wired (production path) we MUST resolve
	// the targeted session out of the session manager before deciding
	// what to compact. Pre-fix this method ignored sessionID entirely:
	// `e.Manifest()` returned whatever agent the engine's last
	// SetManifest call landed (could be a sibling session's agent), and
	// `e.store.GetRecent` returned the global store's tail — empty for a
	// freshly-resumed session, or polluted with another session's
	// messages. Result: /compact returned {fired: false} against
	// sessions where the user could see plenty of content. The
	// resolution chain matches handleSessionMessage at server.go:1313-
	// 1321 (CurrentAgentID overrides AgentID).
	e.mu.RLock()
	lookup := e.sessionLookup
	e.mu.RUnlock()

	var explicitMessages []provider.Message
	var sessionProviderID, sessionModelID string
	if lookup != nil {
		messages, agentID, providerID, modelID, ok := lookup.SnapshotForCompaction(sessionID)
		if !ok {
			// Session not found — refuse cleanly. Returning ("", false)
			// keeps the api handler's "nothing to compact" branch
			// untouched and avoids panicking on a stale slash-command
			// URL.
			return "", false
		}
		if len(messages) == 0 {
			// Empty session — no transcript to compact against. The
			// pre-fix path silently fell through to the global store
			// (which might still hold a different session's tail);
			// returning ("", false) here is the correct "nothing to
			// compact" signal.
			return "", false
		}
		explicitMessages = messages

		// Resolve the manifest the session is actually running under.
		// agentID is empty on legacy sessions persisted before the
		// agent-stamping fields existed; in that case we fall back to
		// the engine's current manifest (which is what the pre-fix
		// code did in every case — preserved as the floor).
		if agentID != "" && e.agentRegistry != nil {
			if resolved, found := e.agentRegistry.Get(agentID); found && resolved != nil {
				manifest = *resolved
			}
		}

		// Resolve the per-session token budget. The engine's
		// ModelContextLimit reads e.LastModel(), which tracks the most-
		// recent Stream invocation across all sessions — wrong for a
		// /compact call against a session whose provider/model pair
		// differs from the engine's last-streamed pair. Use the
		// session's current provider/model when both are stamped;
		// otherwise the engine-level fallback above stays in force.
		if providerID != "" && modelID != "" {
			if perSessionLimit := e.ResolveContextLength(providerID, modelID); perSessionLimit > 0 {
				tokenBudget = perSessionLimit
			}
		}

		// Capture the session's provider+model so the summariser route
		// can fall back to it when category routing yields an
		// unresolved abstract descriptor (e.g. "fast" without a
		// ModelLister wired). Pre-fix this gap caused /compact to fail
		// with `Unknown Model` against z.ai because ProviderSummariser
		// sent the literal "fast" descriptor to the provider — May
		// 2026 force-fire regression. ProviderSummariser.resolveRoute
		// reads the hint via sessionModelFromContext.
		sessionProviderID = providerID
		sessionModelID = modelID

		// Seed the engine's process-wide store with the session's
		// history. This is a defensive mirror — the explicit-messages
		// path below does NOT read from e.store, but other engine
		// surfaces (LastCompactionSummary, sessionRehydrated bookkeeping)
		// still index by sessionID and a future caller that switches
		// back to the store-driven path will benefit. Idempotent per
		// sessionID via the seededSessions tracker.
		e.SeedHistory(sessionID, messages)
	}

	if tokenBudget <= 0 {
		// No budget signal — refuse rather than feeding the summariser
		// against garbage. Matches the MaybeCompactForModel guard.
		return "", false
	}

	// Attach the session's (provider, model) so ProviderSummariser
	// can fall through to it when category routing yields an
	// unresolved abstract descriptor. WithSessionModel is a no-op
	// when sessionModelID is empty (legacy sessions persisted before
	// the agent-stamping fields existed), preserving the pre-fix
	// behaviour for the bootstrap callers.
	ctx = WithSessionModel(ctx, sessionProviderID, sessionModelID)

	summary := e.maybeAutoCompactExplicit(ctx, sessionID, &manifest, tokenBudget, "manual", explicitMessages)
	return summary, summary != ""
}

// SetAutoCompactionThreshold updates the soft trigger's ratio
// threshold at runtime. Deliverable 2 of the May 2026 context-
// accuracy + manual-compaction bundle: pre-fix the threshold was
// frozen at engine construction (read from cfg.Compression.AutoCompaction.Threshold)
// and operators had to restart the process to retune it. The api
// layer's PATCH /api/v1/config/compression/threshold endpoint and
// the SettingsView's slider both flow through this method.
//
// Validation mirrors CompressionConfig.Validate's auto_compaction.threshold
// rules so the runtime knob cannot land the engine in a state the
// startup loader would have rejected:
//   - finite (NaN rejected — never compares true, would silently
//     disable the trigger),
//   - in (0.0, 1.0] (values <= 0 never fire; values > 1 fire on
//     every turn).
//
// Expected:
//   - threshold is the new ratio. Must satisfy 0 < threshold <= 1.
//
// Returns:
//   - nil on a successful mutation.
//   - A diagnostic error when the input fails validation; the caller
//     surfaces the message to the operator (api 400 / settings UI
//     inline error).
//
// Side effects:
//   - Updates e.compressionConfig.AutoCompaction.Threshold under
//     buildStateMu so the next autoCompactionThreshold read sees the
//     new value.
func (e *Engine) SetAutoCompactionThreshold(threshold float64) error {
	if math.IsNaN(threshold) {
		return errors.New(
			"compression: threshold must be a finite fraction in (0.0, 1.0]; " +
				"got NaN, which never compares true and would silently disable the layer")
	}
	if threshold <= 0.0 || threshold > 1.0 {
		return fmt.Errorf(
			"compression: threshold must be in the (0.0, 1.0] interval (got %v); "+
				"values <= 0 never trigger, values > 1 trigger every turn",
			threshold,
		)
	}
	e.buildStateMu.Lock()
	e.compressionConfig.AutoCompaction.Threshold = threshold
	e.buildStateMu.Unlock()
	return nil
}

// AutoCompactionThreshold reports the engine's current soft trigger
// threshold. Companion to SetAutoCompactionThreshold; the GET
// /api/v1/config/compression endpoint surfaces this so the
// SettingsView slider can hydrate to the current value on page
// load rather than guessing the default.
//
// Returns:
//   - The threshold as a fraction in (0.0, 1.0]. Returns the
//     configured value verbatim — operators expect what they
//     wrote in config.yaml to round-trip through readback.
//
// Side effects:
//   - None.
//
// Expected: parameters for AutoCompactionThreshold.
func (e *Engine) AutoCompactionThreshold() float64 {
	if e == nil {
		return 0
	}
	e.buildStateMu.Lock()
	defer e.buildStateMu.Unlock()
	return e.compressionConfig.AutoCompaction.Threshold
}

// publishContextCompactedEvent preferredProviderModel returns the first PreferredModels entry on the manifest, falling back to the empty pair when none is set. The empty pair flows through ResolveOutputLimit → 0 → outputReserveFor → defaultOutputReserve, mirroring the no-MaxTokens path the gate itself takes for callers without an explicit override.
//
// Side effects:
//   - None.
//
// Expected: parameters for preferredProviderModel.
// Returns: result of preferredProviderModel.
// publishContextCompactedEvent emits the T10b ContextCompactedEvent on
// the engine bus and mirrors the compression metrics it computes.
//
// It emits the T10b ContextCompactedEvent on
// the engine bus when compaction succeeds. Counted as observability:
// failed or no-op compactions are not emitted so subscribers do not see
// phantom events.
//
// Phase-5 Slice α added the trigger parameter so the emitted event
// carries a discriminant identifying which tier fired the compaction
// ("ratio", "gate_proximity", "model_switch", "tool_result_wave").
// Slice δ surfaces the field across the wire bridge and onto the chip
// tooltip; this seam is the source of truth.
//
// Expected:
//   - sessionID and agentID identify the emission source.
//   - recentTokens is the pre-compaction token count the summary replaces.
//   - summaryText is the final "[auto-compacted summary]: <json>" string
//     injected into the built window. Empty when summaryGenerated is
//     false (the Stage-1 prune-only short-circuit) — summaryTokens
//     then evaluates to 0 and the delta accounting reports the prune
//     reclaim as raw savings.
//   - latency is the wall-clock duration of the Compact call. Zero on
//     the prune-only short-circuit path (no LLM call was made).
//   - trigger is the closed-vocabulary discriminant identifying the
//     fire path. Empty is tolerated for forward-compatibility.
//   - prunedToolOutputs is the count of tool-result messages whose
//     Content the Stage-1 prune pass truncated on this turn. Zero is
//     the honest figure for fires that did not benefit from pruning
//     (no eligible messages or all protected by name).
//   - summaryGenerated is true when the LLM summariser was invoked
//     and produced summaryText; false when pruning alone reclaimed
//     enough tokens to drop below the threshold.
//
// Side effects:
//   - Publishes one event on the engine bus if non-nil; otherwise no-op.
//
// Returns: result of publishContextCompactedEvent.
func (e *Engine) publishContextCompactedEvent(sessionID, agentID string, recentTokens int, summaryText string, latency time.Duration, trigger string, prunedToolOutputs int, summaryGenerated bool) {
	summaryTokens := e.tokenCounter.Count(summaryText)
	delta := recentTokens - summaryTokens
	if e.compressionMetrics != nil {
		e.compressionMetrics.AutoCompactionCount++
		if delta > 0 {
			e.compressionMetrics.TokensSaved += delta
		} else if delta < 0 {
			// Item 5 — honest accounting for the cost the layer added.
			e.compressionMetrics.OverheadTokens += -delta
		}
	}
	// Mirror the same deltas onto the per-session ledger so
	// flowstate run --stats reports the CURRENT session's numbers
	// instead of the cumulative aggregate. The aggregate above still
	// grows in lockstep because flowstate serve dashboards depend on
	// it.
	e.recordSessionAutoCompaction(sessionID, delta)
	if e.recorder != nil {
		// M3/Item 5 — mutually exclusive emit paths. Delta > 0 fires
		// the savings counter, delta < 0 fires the overhead counter,
		// and delta == 0 (break-even) fires neither so we do not
		// double-count or produce misleading traffic. The Recorder
		// interface contract also mandates implementations ignore
		// non-positive values, so the guards here are defence in depth.
		switch {
		case delta > 0:
			e.recorder.RecordCompressionTokensSaved(agentID, delta)
		case delta < 0:
			e.recorder.RecordCompressionOverheadTokens(agentID, -delta)
		}
	}
	if e.bus == nil {
		return
	}
	e.bus.Publish(events.EventContextCompacted, events.NewContextCompactedEvent(events.ContextCompactedEventData{
		SessionID:         sessionID,
		AgentID:           agentID,
		OriginalTokens:    recentTokens,
		SummaryTokens:     summaryTokens,
		LatencyMS:         latency.Milliseconds(),
		Trigger:           trigger,
		PrunedToolOutputs: prunedToolOutputs,
		SummaryGenerated:  summaryGenerated,
	}))
}

// LastCompactionSummary returns the most recent auto-compaction summary
// produced by buildContextWindow, or nil if compaction has not fired
// since the engine was created (or since the last non-firing build).
//
// Expected:
//   - The engine has been used to assemble at least one context window.
//
// Returns:
//   - A pointer to the stored summary. The caller must not mutate it;
//     it is the same value persisted on the engine.
//   - nil when compaction has not fired on the most recent build.
//
// Side effects:
//   - None.
func (e *Engine) LastCompactionSummary() *ctxstore.CompactionSummary {
	e.buildStateMu.Lock()
	defer e.buildStateMu.Unlock()
	return e.lastCompactionSummary
}

// recordSessionAutoCompaction compressionMetrics returns a snapshot of the per-engine compression counters (micro/auto counts, tokens saved, overhead tokens). The returned value is a copy; callers may retain it without affecting live accounting. Item 2 exposes this so `flowstate run --stats` can emit a per-turn summary before exit, sidestepping the limitation that ephemeral CLI processes do not feed the /metrics endpoint.
//
// Expected:
//   - None; safe to call at any point in the engine lifecycle. Returns
//     a zero-valued struct when compression metrics were not wired
//     (e.g. the CompressionMetrics field was nil in Config).
//
// Returns:
//   - A CompressionMetrics value capturing the current counters. Zero
//     when no compression metrics are attached to the engine.
//
// Side effects:
//   - None.
//
// recordSessionAutoCompaction mirrors the compressionMetrics bumps
// publishContextCompactedEvent applies onto the per-session ledger.
//
// It mirrors the compressionMetrics bumps
// publishContextCompactedEvent already applies to the cumulative
// aggregate onto the per-session ledger keyed by sessionID. The
// mapping is intentionally lazy — a session id that never fires
// compaction never allocates an entry — so the map only grows for
// sessions that actually produced work. The C1 eviction hook
// (handleSessionEnded) removes the entry when the session ends, so
// long-running flowstate serve processes do not accumulate dead
// per-session ledgers forever.
//
// Expected:
//   - sessionID is the identifier passed into publishContextCompactedEvent.
//     Empty strings map to the "" bucket deliberately — the caller's
//     choice determines whether that is meaningful.
//   - delta is OriginalTokens - SummaryTokens. Positive values bump
//     TokensSaved; negative values bump OverheadTokens. Zero-deltas
//     still count the compaction call itself (AutoCompactionCount),
//     matching the aggregate accounting contract.
//
// Side effects:
//   - Allocates a CompressionMetrics under the supplied sessionID on
//     first use.
//
// Returns: result of recordSessionAutoCompaction.
func (e *Engine) recordSessionAutoCompaction(sessionID string, delta int) {
	e.sessionCompressionMetricsMu.Lock()
	defer e.sessionCompressionMetricsMu.Unlock()
	entry, ok := e.sessionCompressionMetrics[sessionID]
	if !ok || entry == nil {
		entry = &ctxstore.CompressionMetrics{}
		e.sessionCompressionMetrics[sessionID] = entry
	}
	entry.AutoCompactionCount++
	if delta > 0 {
		entry.TokensSaved += delta
	} else if delta < 0 {
		entry.OverheadTokens += -delta
	}
}

// recordSessionMicroCompaction mirrors the aggregate
// MicroCompactionCount bump WindowBuilder applies via its attached
// *CompressionMetrics onto the per-session ledger. The delta is the
// number of cold messages HotColdSplitter offloaded on the current
// Build call, captured via BuildResult.MicroCompactedCount and
// forwarded here by buildContextWindow.
//
// Expected:
//   - sessionID identifies the active session.
//   - delta is the non-negative number of cold offloads from the most
//     recent Build call; zero-deltas are skipped so the map does not
//     fill with empty entries for sessions that only saw hot-tail
//     messages.
//
// Side effects:
//   - Allocates a CompressionMetrics under the supplied sessionID on
//     first use.
//
// Returns: result of recordSessionMicroCompaction.
func (e *Engine) recordSessionMicroCompaction(sessionID string, delta int) {
	if delta <= 0 {
		return
	}
	e.sessionCompressionMetricsMu.Lock()
	defer e.sessionCompressionMetricsMu.Unlock()
	entry, ok := e.sessionCompressionMetrics[sessionID]
	if !ok || entry == nil {
		entry = &ctxstore.CompressionMetrics{}
		e.sessionCompressionMetrics[sessionID] = entry
	}
	entry.MicroCompactionCount += delta
}

// buildResultInputs groups the inputs assembleBuildResult needs so
// the method signature stays inside the project's per-function
// argument limit. A struct here is more honest than a free-for-all
// signature: these fields are all parallel context carried between
// buildContextWindow and the WindowBuilder entry points.
