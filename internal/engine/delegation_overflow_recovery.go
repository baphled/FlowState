package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// recoverableChildOverflow reports whether err is a
// context_window_exceeded provider error that the delegation child
// path should treat as recoverable — the same classification the
// parent streamFromProvider path applies before its compact-and-retry.
//
// Expected:
//   - err is the collected delegate failure.
//
// Returns:
//   - True when err wraps a provider.Error of type
//     context_window_exceeded.
//
// Side effects:
//   - None.
func recoverableChildOverflow(err error) bool {
	var pErr *provider.Error
	return errors.As(err, &pErr) && pErr != nil && pErr.ErrorType == provider.ErrorTypeContextWindowExceeded
}

// recoverChildOverflow performs the parent-parity overflow recovery for
// a delegated child attempt: compact the child session's transcript via
// the same compactSessionForOverflowRecovery the parent Stream path
// uses, then re-dispatch the child stream once with the local context
// gate skipped so the real provider — whose limit may differ from the
// reserve-inflated local estimate — gets the final say.
//
// Expected:
//   - ctx is the delegate attempt context carrying the child session id.
//   - d is the delegate tool under dispatch.
//   - target is the resolved delegation target.
//   - result receives the re-dispatch's collected result.
//
// Returns:
//   - nil when the re-dispatch drained cleanly (result populated).
//   - The re-dispatch error otherwise; a still-overflowing retry
//     surfaces the terminal ErrCompactionInsufficient failure the same
//     way the parent tool loop does.
//
// Side effects:
//   - May compact the child session transcript (summariser call or
//     naive truncation fallback) and increment the child session's
//     overflow-recovery compaction counter.
//
// recoverChildOverflow performs the parent-parity overflow recovery
// for a delegated child attempt: compact the child session's transcript
// via the same compactSessionForOverflowRecovery the parent Stream path
// uses, re-check the usable-limit gate on the rebuilt window, and
// re-dispatch only when the compacted request now fits. A window that
// still exceeds the usable limit fails closed with
// ErrCompactionInsufficient — the gate is never skipped, so the
// provider is never called over budget.
//
// Expected:
//   - ctx is the delegate attempt context carrying the child session id.
//   - target is the resolved delegation target.
//   - result receives the re-dispatch's collected result.
//
// Returns:
//   - nil when the re-dispatch drained cleanly (result populated).
//   - An error wrapping ErrCompactionInsufficient when the compacted
//     window still exceeds the usable limit.
//
// Side effects:
//   - May compact the child session transcript (summariser call or
//     naive truncation fallback) and increment the child session's
//     overflow-recovery compaction counter.
func (d *DelegateTool) recoverChildOverflow(ctx context.Context, target delegationTarget, result *delegationResult) error {
	eng := target.engine
	if eng == nil {
		return fmt.Errorf("delegation overflow recovery: no child engine for %s", target.agentID)
	}
	childSessionID := sessionIDFromContext(ctx)
	window := eng.BuildContextWindowForTesting(ctx, childSessionID, target.message)
	req := &provider.ChatRequest{
		Provider: eng.LastProvider(),
		Model:    eng.LastModel(),
		Messages: window,
	}
	eng.compactSessionForOverflowRecovery(ctx, childSessionID, req)
	req.Messages = compactOldToolResultsForBudget(req.Messages, eng.toolMessageCompactionThreshold, eng.toolMessageCompactionKeepRecent, eng.tokenCounter, eng.ModelContextLimit())
	if rebuilt := eng.rebuildContextWindowTokenBounded(ctx, childSessionID, req.Messages, eng.lastCompactionSummaryText()); rebuilt != nil && eng.contextEstimateDropped(req.Messages, rebuilt) {
		req.Messages = rebuilt
	}
	if gateErr := eng.checkContextWindowOverflow(req); gateErr != nil {
		return fmt.Errorf("%w: child session %s still over usable limit after compaction", ErrCompactionInsufficient, childSessionID)
	}
	chunks, streamErr := d.resolveStreamer(target.agentID, eng).Stream(ctx, target.agentID, target.message)
	if streamErr != nil {
		return streamErr
	}
	chunks = d.withHarnessEvents(ctx, target, chunks, nil, false)
	chunks = d.wrapWithAccumulator(ctx, chunks, childSessionID, target.agentID)
	res, collectErr := d.collectWithPolicy(ctx, chunks, time.Now().UTC())
	*result = res
	return collectErr
}
