package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	compactionpkg "github.com/baphled/flowstate/internal/context/compaction"
	"github.com/baphled/flowstate/internal/provider"
)

// hotTailBudgetDisabled is the sentinel hot-tail budget for engines
// that did not opt into the Phase 2 budgeted selection (the zero value
// of the test wiring knobs). Selection then degrades to the historical
// tail behaviour so existing callers keep their semantics.
const hotTailBudgetDisabled = 0

// selectHotTailBudgeted returns the token-bounded hot tail of live
// messages: it starts from the most recent minResults messages and
// grows backwards while the cumulative content cost stays within
// budget. When even the floor exceeds budget the tail keeps the most
// recent messages that fit (never fewer than the newest one) so the
// overflow retry in the caller can shed the remainder message by
// message.
//
// Expected:
//   - messages is the live tool-loop slice; may be empty.
//   - minResults is the configured hot_tail_min_results floor.
//   - budget is the configured hot_tail_size_budget in tokens; a
//     non-positive value disables the bound (minResults governs).
//
// Returns:
//   - The bounded tail as a fresh slice anchored at the end of
//     messages; nil only when messages is nil.
//
// Side effects: None.
func (e *Engine) selectHotTailBudgeted(_ context.Context, messages []provider.Message, minResults, budget int) []provider.Message {
	if len(messages) == 0 {
		return nil
	}
	if budget <= hotTailBudgetDisabled || e.tokenCounter == nil {
		if minResults < 1 || minResults > len(messages) {
			return cloneTail(messages, len(messages))
		}
		return cloneTail(messages, minResults)
	}
	floor := minResults
	if floor > len(messages) {
		floor = len(messages)
	}
	if floor < 1 {
		floor = 1
	}
	total := 0
	for _, m := range messages[len(messages)-floor:] {
		total += e.messageContentCost(m)
	}
	if total > budget {
		keep := 0
		cost := 0
		for i := len(messages) - 1; i >= 0; i-- {
			c := e.messageContentCost(messages[i])
			if keep >= 1 && cost+c > budget {
				break
			}
			keep++
			cost += c
		}
		return cloneTail(messages, keep)
	}
	keep := floor
	for i := len(messages) - floor - 1; i >= 0; i-- {
		c := e.messageContentCost(messages[i])
		if total+c > budget {
			break
		}
		total += c
		keep++
	}
	return cloneTail(messages, keep)
}

// cloneTail returns a fresh slice holding the last count messages.
//
// Expected:
//   - messages is non-empty and 0 < count <= len(messages).
//
// Returns:
//   - A freshly allocated slice of the last count messages.
//
// Side effects: None.
func cloneTail(messages []provider.Message, count int) []provider.Message {
	out := make([]provider.Message, count)
	copy(out, messages[len(messages)-count:])
	return out
}

// messageContentCost returns the engine's token estimate for one
// message's content.
//
// Expected:
//   - m is any message; content may be empty.
//
// Returns:
//   - The token count of the content.
//
// Side effects: None.
func (e *Engine) messageContentCost(m provider.Message) int {
	if e == nil || e.tokenCounter == nil || m.Content == "" {
		return 0
	}
	return int(e.tokenCounter.Count(m.Content))
}

// rebuildHotTailBudgeted assembles the post-compaction window —
// system prompt, todo context, compaction summary, and a
// token-bounded hot tail — and guarantees the result fits the usable
// context budget. When the assembled window still exceeds the budget
// the oldest hot-tail messages are dropped one at a time, re-checking
// the estimate after each drop; if a single remaining message still
// does not fit the compaction turn fails loudly with
// ErrCompactionInsufficient instead of dispatching an oversized
// context to the provider.
//
// Expected:
//   - ctx carries the provider/model resolution keys and system-prompt
//     build context.
//   - sessionID identifies the session for todo-context assembly.
//   - messages is the live tool-loop slice; never empty.
//   - summary may be "" (no compaction summary available).
//   - minResults and budget carry the compaction config block's
//     hot_tail_min_results / hot_tail_size_budget.
//
// Returns:
//   - The rebuilt, budget-fitting slice.
//   - An error wrapping ErrCompactionInsufficient when no single-tail
//     window fits, or the budget cannot be resolved.
//
// Side effects:
//   - Logs a loud error with the token counts on the terminal
//     single-message overflow path.
func (e *Engine) rebuildHotTailBudgeted(ctx context.Context, sessionID string, messages []provider.Message, summary string, minResults, budget int) ([]provider.Message, error) {
	if e == nil || e.tokenCounter == nil || len(messages) == 0 {
		return nil, fmt.Errorf("%w: no token counter or empty message slice", ErrCompactionInsufficient)
	}
	prov := e.lastProviderCtx(ctx)
	model := e.lastModelCtx(ctx)
	limit := e.ResolveContextLength(prov, model)
	if limit <= 0 {
		return nil, fmt.Errorf("%w: no context limit resolved", ErrCompactionInsufficient)
	}
	req := provider.ChatRequest{
		Provider: prov,
		Model:    model,
		Messages: messages,
		Tools:    e.buildToolSchemasCtx(ctx),
	}
	usable := limit - e.outputReserveFor(&req)
	if usable < 1 {
		usable = 1
	}

	prefix := []provider.Message{{Role: "system", Content: e.BuildSystemPromptCtx(ctx)}}
	prefix = e.appendTodoContext(prefix, sessionID)

	tail := e.selectHotTailBudgeted(ctx, messages, minResults, budget)
	estimator := e.hotTailEstimator(ctx, prov, model, req.Tools, prefix, summary)
	tail, err := e.shrinkTailToBudget(estimator, tail, usable, sessionID, limit, summary)
	if err != nil {
		return nil, err
	}

	rebuilt := make([]provider.Message, 0, len(prefix)+1+len(tail))
	rebuilt = append(rebuilt, prefix...)
	if summary != "" {
		rebuilt = append(rebuilt, provider.Message{Role: "assistant", Content: summary})
	}
	rebuilt = append(rebuilt, tail...)
	return rebuilt, nil
}

// hotTailEstimator returns a closure estimating the full assembled
// window (prefix + optional summary + tail) in tokens.
//
// Expected:
//   - prefix and tail are message slices; summary may be "".
//
// Returns:
//   - A func estimating the request tokens for the assembled window.
//
// Side effects: None.
func (e *Engine) hotTailEstimator(_ context.Context, prov, model string, tools []provider.Tool, prefix []provider.Message, summary string) func([]provider.Message) int {
	return func(tail []provider.Message) int {
		window := make([]provider.Message, 0, len(prefix)+1+len(tail))
		window = append(window, prefix...)
		if summary != "" {
			window = append(window, provider.Message{Role: "assistant", Content: summary})
		}
		window = append(window, tail...)
		return e.estimateRequestTokens(&provider.ChatRequest{
			Provider: prov,
			Model:    model,
			Messages: window,
			Tools:    tools,
		})
	}
}

// shrinkTailToBudget drops the oldest tail messages one at a time
// until the estimated window fits the usable budget. A tail that
// cannot fit even as a single message fails loudly with
// ErrCompactionInsufficient carrying the token counts.
//
// Expected:
//   - tail is the token-bounded hot tail; usable >= 1.
//
// Returns:
//   - The shrunk, fitting tail.
//   - An error wrapping ErrCompactionInsufficient on terminal overflow.
//
// Side effects:
//   - Logs a loud error with the token counts on the terminal path.
func (e *Engine) shrinkTailToBudget(estimated func([]provider.Message) int, tail []provider.Message, usable int, sessionID string, limit int, summary string) ([]provider.Message, error) {
	for {
		if estimated(tail) <= usable {
			return tail, nil
		}
		if len(tail) <= 1 {
			slog.Error("compaction insufficient: single-message window still exceeds context budget",
				"session", sessionID,
				"estimated_tokens", estimated(tail),
				"usable_tokens", usable,
				"context_limit", limit,
				"summary_tokens", e.messageContentCost(provider.Message{Content: summary}),
				"tail_messages", len(tail),
			)
			return nil, fmt.Errorf("%w: estimated %d tokens over usable budget %d", ErrCompactionInsufficient, estimated(tail), usable)
		}
		tail = tail[1:]
	}
}

// errNoLiveMessages is the sentinel error returned by the budgeted
// selection test shim when the caller passes an empty slice, so step
// definitions surface a deterministic failure instead of a nil window.
var errNoLiveMessages = errors.New("hot tail selection: no live messages")

// selectHotTailBudgetedResolving resolves the effective hot-tail floor
// and budget (test overrides first, then the compaction config block)
// and delegates to selectHotTailBudgeted.
//
// Expected:
//   - messages is a non-empty live slice.
//
// Returns:
//   - The bounded tail; errNoLiveMessages on an empty slice.
//
// Side effects: None.
func (e *Engine) selectHotTailBudgetedResolving(ctx context.Context, messages []provider.Message) ([]provider.Message, error) {
	if len(messages) == 0 {
		return nil, errNoLiveMessages
	}
	minResults, budget := e.resolveHotTailKnobs()
	return e.selectHotTailBudgeted(ctx, messages, minResults, budget), nil
}

// rebuildHotTailBudgetedResolving resolves the effective hot-tail floor
// and budget and delegates to rebuildHotTailBudgeted with a fixed
// test session identifier.
//
// Expected:
//   - messages is a non-empty live slice; summary may be "".
//
// Returns:
//   - Same as rebuildHotTailBudgeted.
//
// Side effects: Same as rebuildHotTailBudgeted.
func (e *Engine) rebuildHotTailBudgetedResolving(ctx context.Context, messages []provider.Message, summary string) ([]provider.Message, error) {
	minResults, budget := e.resolveHotTailKnobs()
	return e.rebuildHotTailBudgeted(ctx, "hot-tail-budget-test", messages, summary, minResults, budget)
}

// resolveHotTailKnobs returns the effective (floor, budget) pair from
// the test overrides when set, otherwise from the engine's compaction
// config with defaults applied.
//
// Returns:
//   - The hot-tail minimum floor and token budget.
//
// Side effects: None.
//
// Expected: parameters for resolveHotTailKnobs.
func (e *Engine) resolveHotTailKnobs() (int, int) {
	cfg := e.compactionConfig
	compactionpkg.ApplyDefaults(&cfg)
	budget := e.hotTailBudgetOverride
	if budget == hotTailBudgetDisabled {
		budget = cfg.HotTailSizeBudget
	}
	minResults := e.hotTailMinFloorOverride
	if minResults == 0 {
		minResults = cfg.HotTailMinResults
	}
	return minResults, budget
}
