package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

// ReasonOutputTruncated is the reason of the non-terminal provider.error the loop writes before it
// retries a turn the output cap cut off, and of the terminal one when the retry is cut off too.
const ReasonOutputTruncated = CodeOutputTruncated

// MaxOutputCeiling is the largest max_tokens a run may escalate to on a model: its table maximum
// (outputTable, not limited by DefaultOutputCap), else, for an unknown model, 4 x DefaultMaxOutput.
func MaxOutputCeiling(providerName, model string) int64 {
	ceiling, _ := outputCeiling(providerName, model)
	return ceiling
}

// outputCeiling is MaxOutputCeiling and whether the model is in the table: for an unknown model the
// ceiling is a heuristic, not a limit the provider states.
func outputCeiling(providerName, model string) (ceiling int64, known bool) {
	if limit, ok := outputTable[tableKey(providerName, model)]; ok {
		return int64(limit), true
	}
	return unknownOutputCeilingFactor * DefaultMaxOutput, false
}

// unknownOutputCeilingFactor multiplies DefaultMaxOutput into the ceiling of a model the table lacks.
const unknownOutputCeilingFactor = 4

// lengthNoCalls reports a turn the output cap cut off with no complete tool call: not a good answer.
// A length finish that carries complete calls is not this: the calls run and the model continues.
func lengthNoCalls(turn execTurn) bool {
	return turn.FinishReason == provider.FinishLength && len(turn.Calls) == 0
}

// escalatedCap is the output cap of the retry: double the cap the run sends, limited to the model's
// ceiling. ok is false when the cap is already at (or above) the ceiling: there is nothing to escalate.
func escalatedCap(cfg *ExecLoopConfig) (next int64, ok bool) {
	current := ResolveMaxOutput(cfg.Session.Provider, cfg.Session.ModelID, cfg.MaxTokens)
	ceiling := MaxOutputCeiling(cfg.Session.Provider, cfg.Session.ModelID)
	if current >= ceiling {
		return 0, false
	}
	return min(current*2, ceiling), true
}

// escalate recovers a length finish with no calls on a request that was not over the compaction line
// (ADR-010 D10 keeps priority when it was): one retry with the doubled cap. It writes the non-terminal
// marker, reserves a second pool unit and counts the retry as a second turn, as the overflow retry
// does. The truncated assistant message stays in the log, and the marker tells every rebuild from the
// log to drop it (rebuildHistory); the retry history never holds it. A cap already at the ceiling, a
// retry that is cut off too, or a retry the provider rejects for the context window (the doubled cap
// does not fit; it is not compacted again, a turn has one recovery) fails the run with ErrOutputTruncated.
//
// The retry counts as a turn (recordTurn): MaxTurns is checked between turns, so a run may overshoot
// it by at most one turn here. A child's pool bounds it instead: the unit is reserved BEFORE the marker
// is written, so with no unit left the child reports a budget stop (ErrExecLoopBudget), not
// output_truncated, and the log holds no marker for a retry that never ran.
func (c *compactRun) escalate(ctx context.Context, cfg *ExecLoopConfig, definitions []provider.ToolDefinition,
	history []provider.Message, res *ExecLoopResult) (execTurn, error) {
	next, ok := escalatedCap(cfg)
	if !ok {
		return execTurn{}, failExecOutputTruncated(ctx, cfg, ceilingDetail(cfg))
	}
	if err := beginExecRequest(ctx, cfg, res); err != nil {
		return execTurn{}, err
	}
	if err := markOutputTruncated(ctx, cfg, next); err != nil {
		return execTurn{}, err
	}
	retryCfg := *cfg
	retryCfg.MaxTokens = next
	turn, err := runExecTurn(ctx, retryCfg, definitions, history, true)
	res.recordTurn(turn)
	c.afterTurn(turn.Usage)
	if errors.Is(err, provider.ErrContextOverflow) {
		return execTurn{}, failExecOutputTruncated(ctx, cfg, "the retry with the doubled cap does not fit the context window")
	}
	return c.settleRetry(ctx, cfg, turn, err)
}

// ceilingDetail says why the cap cannot grow: it is at the ceiling, which for a model the table lacks
// is the heuristic 4 x DefaultMaxOutput, not a limit the provider states.
func ceilingDetail(cfg *ExecLoopConfig) string {
	ceiling, known := outputCeiling(cfg.Session.Provider, cfg.Session.ModelID)
	if known {
		return fmt.Sprintf("the output cap is already at the model's ceiling (%d)", ceiling)
	}
	return fmt.Sprintf("the output cap is already at the ceiling (%d for an unknown model)", ceiling)
}

// settleRetry ends the one recovery a turn has: a retry that is cut off again with no calls fails
// the run, whichever recovery (compaction or escalation) ran it.
func (c *compactRun) settleRetry(ctx context.Context, cfg *ExecLoopConfig, turn execTurn, err error) (execTurn, error) {
	if err == nil && lengthNoCalls(turn) {
		return execTurn{}, failExecOutputTruncated(ctx, cfg, "the retry was cut off too")
	}
	return turn, err
}

// markOutputTruncated appends the non-terminal provider.error marker of a retry of a truncated turn:
// next is the escalated max_tokens, or 0 for the retry after a compaction (same cap). The marker also
// tells a rebuild to drop the truncated assistant message that precedes it. A marker that cannot be
// written is a provider failure, with the status untouched as in failExecTurn.
func markOutputTruncated(ctx context.Context, cfg *ExecLoopConfig, next int64) error {
	w, err := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
	if err != nil {
		return fmt.Errorf("%w: record output truncation: %w", ErrProvider, err)
	}
	defer func() { _ = w.Close() }()
	text := fmt.Sprintf("%v: the output cap cut the answer off; retrying after a compaction", ErrOutputTruncated)
	payload := map[string]any{"error": text, "reason": ReasonOutputTruncated, "terminal": false}
	if next > 0 {
		payload["error"] = fmt.Sprintf("%v: the output cap cut the answer off; retrying with max_tokens %d", ErrOutputTruncated, next)
		payload["max_tokens"] = next
	}
	if _, aerr := w.Append(ctx, session.ActorEngine, TypeProviderError, payload); aerr != nil {
		return fmt.Errorf("%w: record output truncation: %w", ErrProvider, aerr)
	}
	return nil
}

// failExecOutputTruncated records the terminal output_truncated failure as failExecMaxTurns records
// max_turns: the provider.error event (reason output_truncated, terminal true) is appended before the
// session is marked failed. The result wraps ErrOutputTruncated and any persistence failure.
func failExecOutputTruncated(ctx context.Context, cfg *ExecLoopConfig, detail string) error {
	cause := fmt.Errorf("%w: %s", ErrOutputTruncated, detail)
	w, err := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
	if err != nil {
		return fmt.Errorf("%w: record output truncation: %w", cause, err)
	}
	defer func() { _ = w.Close() }()
	payload := map[string]any{"error": cause.Error(), "reason": ReasonOutputTruncated, "terminal": true}
	if _, aerr := w.Append(ctx, session.ActorEngine, TypeProviderError, payload); aerr != nil {
		return fmt.Errorf("%w: record output truncation: %w", cause, aerr)
	}
	if uerr := cfg.Store.UpdateSessionStatus(ctx, cfg.Session.ID, "failed"); uerr != nil {
		return fmt.Errorf("%w: mark session failed: %w", cause, uerr)
	}
	return cause
}
