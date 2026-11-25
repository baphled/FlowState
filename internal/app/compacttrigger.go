package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

// compactUnit is the pool draw of one summarising call (ADR-010 D12), taken before the turn's own
// reservation.
const compactUnit int64 = 1

// compactLimit is the line the automatic trigger compares the context with (ADR-010 "When"):
// window - reserve, where the reserve is raised to the run's MaxTokens when that is larger. It
// reports false when automatic compaction is off, the window is unknown (no default window) or
// the line is not positive: a window not larger than the reserve (or MaxTokens) cannot be
// compacted into, so it never compacts rather than compacting every turn.
func compactLimit(cfg *ExecLoopConfig) (limit int, ok bool) {
	settings := cfg.Compaction.Resolved()
	if !settings.Enabled {
		return 0, false
	}
	window, _ := ResolveContextWindow(cfg.Session.Provider, cfg.Session.ModelID, cfg.ProfileWindow, cfg.Compaction)
	if window <= 0 {
		return 0, false
	}
	limit = window - max(settings.Reserve, int(cfg.MaxTokens))
	return limit, limit > 0
}

// estimateOverhead is the bytes/4 estimate of what each request carries besides the history: the
// request-time system prompt and the tool definitions. It is the same estimate estimateTokens
// makes of messages, so tokens_after compares with a reported figure.
func estimateOverhead(systemPrompt string, definitions []provider.ToolDefinition) int {
	n := len(systemPrompt)
	for _, d := range definitions {
		if encoded, err := json.Marshal(d); err == nil {
			n += len(encoded)
		}
	}
	return n / bytesPerToken
}

// size is the context the next request will occupy: the baseline plus the estimate of what was
// appended since, else (no baseline: no reported usage, or just resumed) the estimate of the
// whole rebuilt history with the request overhead.
func (c *compactRun) size(history []provider.Message, overhead int) int {
	if c.hasBase {
		return c.base + c.appended
	}
	return estimateTokens(history) + overhead
}

// afterTurn records a finished provider turn: a turn that reported usage is the new baseline
// (the context it occupied, its own answer included); one that did not leaves none.
func (c *compactRun) afterTurn(u *provider.Usage) {
	c.hasBase, c.appended = u != nil, 0
	if u != nil {
		c.base = int(ContextTokens(*u))
	}
}

// afterResults adds the estimate of the tool results appended to the history since the baseline.
func (c *compactRun) afterResults(results []provider.Message) {
	c.appended += estimateTokens(results)
}

// before runs the automatic compaction when the context is over the line, before a provider
// request. A failed compaction writes nothing, is not retried in the run and the run continues;
// only a cancelled context or a fenced lease ends it. After a compaction the history is rebuilt
// from the log and the baseline is the event's tokens_after.
func (c *compactRun) before(ctx context.Context, cfg *ExecLoopConfig, token int64,
	definitions []provider.ToolDefinition, history *[]provider.Message) error {
	limit, ok := compactLimit(cfg)
	if c.failed || !ok {
		return nil
	}
	overhead := estimateOverhead(cfg.SystemPrompt, definitions)
	if c.size(*history, overhead) <= limit {
		return nil
	}
	if cfg.Pool != nil && cfg.Pool.Reserve(compactUnit) < compactUnit {
		c.failed = true
		return nil
	}
	res, err := compactOnce(ctx, cfg, token, CompactionReasonAuto, overhead, limit)
	if err != nil {
		return c.fail(ctx, cfg, err)
	}
	return c.rebuild(ctx, cfg, history, res)
}

// compactOnce runs one compaction under the loop lease with the line as the ceiling on tokens_after.
func compactOnce(ctx context.Context, cfg *ExecLoopConfig, token int64, reason string, overhead, limit int) (*CompactionResult, error) {
	return Compact(ctx, cfg.Store, cfg.Session.ID, CompactOptions{
		Streamer: cfg.Streamer, Model: cfg.Session.ModelID, Reason: reason,
		KeepRecent: cfg.Compaction.Resolved().KeepRecent, OverheadTokens: overhead, MaxTokensAfter: limit,
		Lease: CompactLease{Item: cfg.LeaseItem, Agent: cfg.LeaseAgent, Token: token},
	})
}

// rebuild replaces the history with the log's rebuild after a compaction and takes the event's
// tokens_after as the baseline.
func (c *compactRun) rebuild(ctx context.Context, cfg *ExecLoopConfig, history *[]provider.Message,
	res *CompactionResult) error {
	fresh := []provider.Message{}
	if err := replayInto(ctx, cfg.Store, cfg.Session.ID, &fresh); err != nil {
		return err
	}
	*history = fresh
	c.base, c.hasBase, c.appended = res.TokensAfter, true, 0
	return nil
}

// fail records a failed automatic compaction. Cancellation is the run's cancellation and a fenced
// lease ends the run; any other failure only stops the trigger for the rest of the run. A
// compaction that made no summary call (no_cut) gives its pool unit back.
func (c *compactRun) fail(ctx context.Context, cfg *ExecLoopConfig, err error) error {
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, ErrExecLoopFenced):
		return err
	}
	c.failed = true
	releaseNoCut(cfg, err)
	return nil
}

// releaseNoCut gives the pool unit back of a compaction that made no summary call (no_cut).
func releaseNoCut(cfg *ExecLoopConfig, err error) {
	var failure *CompactionFailure
	if cfg.Pool != nil && errors.As(err, &failure) && failure.Reason == CompactReasonNoCut {
		cfg.Pool.Release(compactUnit)
	}
}

// ReasonContextOverflow is the reason of the non-terminal provider.error the loop writes when the
// provider says a request does not fit the context window (ADR-010 D10).
const ReasonContextOverflow = "context_overflow"

// errNoCompactUnit reports a pool that cannot cover the overflow compaction's summarising call.
var errNoCompactUnit = errors.New("app: no pool unit for the overflow compaction")

// Error reports the provider's own error.
func (e *execOverflow) Error() string { return e.cause.Error() }

// Unwrap exposes the provider's error, so errors.Is finds provider.ErrContextOverflow.
func (e *execOverflow) Unwrap() error { return e.cause }

// markExecOverflow appends the non-terminal provider.error marker of a first overflow (no failed
// status) and returns the *execOverflow that tells the loop to compact and retry. A marker that
// cannot be written is a provider failure, with the status untouched as in failExecTurn.
func markExecOverflow(ctx context.Context, w EventWriter, cause error) error {
	payload := map[string]any{"error": cause.Error(), "reason": ReasonContextOverflow, "terminal": false}
	if _, aerr := w.Append(ctx, session.ActorEngine, TypeProviderError, payload); aerr != nil {
		return fmt.Errorf("%w: %w: record provider error: %w", ErrProvider, cause, aerr)
	}
	return &execOverflow{cause: cause}
}

// failExecOverflow ends the run as an ordinary terminal provider failure with cause.
func failExecOverflow(ctx context.Context, cfg *ExecLoopConfig, cause error) error {
	w, err := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
	if err != nil {
		return fmt.Errorf("%w: %w: record provider error: %w", ErrProvider, cause, err)
	}
	defer func() { _ = w.Close() }()
	return failExecTurn(ctx, w, cfg.Store, cfg.Session.ID, cause)
}

// request runs one provider request and the reactive net around it (ADR-010 "When" > Overflow):
// a context-overflow error, or a length finish with no tool calls on a request whose baseline was
// already over the line, compacts (reason overflow) and retries the request once.
//
// The net needs a known window, as the automatic trigger does: with none, or with compaction off,
// the failure is an ordinary provider failure and the summariser is never called. The overflow
// compaction makes its own attempt: it ignores the run's failed-automatic-compaction flag, and
// each request has at most one retry, which is itself never retried. A failed compaction after
// an error ends the run as the provider failure; after a length finish the truncated turn stands
// (the existing behaviour), because that turn was a success.
func (c *compactRun) request(ctx context.Context, cfg *ExecLoopConfig, token int64, definitions []provider.ToolDefinition,
	history *[]provider.Message, res *ExecLoopResult) (execTurn, error) {
	limit, enabled := compactLimit(cfg)
	overhead := estimateOverhead(cfg.SystemPrompt, definitions)
	over := enabled && c.size(*history, overhead) > limit
	turn, err := runExecTurn(ctx, *cfg, definitions, *history, enabled)
	res.recordTurn(turn)
	c.afterTurn(turn.Usage)
	var overflow *execOverflow
	switch {
	case errors.As(err, &overflow):
		return c.retry(ctx, cfg, token, definitions, history, res, retryAfter{cause: overflow.cause}, limit, overhead)
	case err == nil && over && len(turn.Calls) == 0 && turn.FinishReason == provider.FinishLength:
		return c.retry(ctx, cfg, token, definitions, history, res, retryAfter{truncated: turn}, limit, overhead)
	}
	return turn, err
}

// retry compacts for the overflow and runs the request a second time, which reserves a second pool
// unit and counts as a second turn in memory (ADR-010 packet (d)). The history is rebuilt from the
// log. After a length finish the truncated assistant message stays in the log, but the retry request does not
// end on an assistant message, so rebuildHistory omits it from the retry request.
func (c *compactRun) retry(ctx context.Context, cfg *ExecLoopConfig, token int64, definitions []provider.ToolDefinition,
	history *[]provider.Message, res *ExecLoopResult, after retryAfter, limit, overhead int) (execTurn, error) {
	if err := c.compactForRetry(ctx, cfg, token, history, limit, overhead); err != nil {
		return c.retryFailed(ctx, cfg, after, err)
	}
	if err := beginExecRequest(ctx, cfg, token, res); err != nil {
		return execTurn{}, err
	}
	turn, err := runExecTurn(ctx, *cfg, definitions, *history, false)
	res.recordTurn(turn)
	c.afterTurn(turn.Usage)
	return turn, err
}

// compactForRetry makes the overflow compaction: its pool unit, the summary, the rebuilt history.
func (c *compactRun) compactForRetry(ctx context.Context, cfg *ExecLoopConfig, token int64,
	history *[]provider.Message, limit, overhead int) error {
	if cfg.Pool != nil && cfg.Pool.Reserve(compactUnit) < compactUnit {
		return errNoCompactUnit
	}
	cres, err := compactOnce(ctx, cfg, token, CompactionReasonOverflow, overhead, limit)
	if err != nil {
		releaseNoCut(cfg, err)
		return err
	}
	return c.rebuild(ctx, cfg, history, cres)
}

// retryFailed ends a run whose overflow compaction failed: cancellation and a fenced lease end it as
// themselves, a failure after an error is the provider failure, and a failure after a length
// finish leaves that turn as the result.
func (c *compactRun) retryFailed(ctx context.Context, cfg *ExecLoopConfig, after retryAfter, err error) (execTurn, error) {
	switch {
	case ctx.Err() != nil:
		return execTurn{}, ctx.Err()
	case errors.Is(err, ErrExecLoopFenced):
		return execTurn{}, err
	case after.cause == nil:
		return after.truncated, nil
	}
	return execTurn{}, failExecOverflow(ctx, cfg, after.cause)
}
