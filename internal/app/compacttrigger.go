package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/baphled/flowstate/internal/provider"
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
	res, err := Compact(ctx, cfg.Store, cfg.Session.ID, CompactOptions{
		Streamer: cfg.Streamer, Model: cfg.Session.ModelID, Reason: CompactionReasonAuto,
		KeepRecent: cfg.Compaction.Resolved().KeepRecent, OverheadTokens: overhead, MaxTokensAfter: limit,
		Lease: CompactLease{Item: cfg.LeaseItem, Agent: cfg.LeaseAgent, Token: token},
	})
	if err != nil {
		return c.fail(ctx, cfg, err)
	}
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
	var failure *CompactionFailure
	if cfg.Pool != nil && errors.As(err, &failure) && failure.Reason == CompactReasonNoCut {
		cfg.Pool.Release(compactUnit)
	}
	return nil
}
