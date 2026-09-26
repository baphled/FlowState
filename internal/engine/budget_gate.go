package engine

import (
	"context"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// TrimForDispatchBudget returns a message window that fits the model's
// context budget before the request is dispatched to a provider. It
// estimates the token cost of the window with the supplied counter,
// compares that estimate against the context limit minus a reserve for
// the completion itself, and — when the estimate exceeds the budget —
// drops the oldest non-system messages until the remaining window fits.
// The leading run of system messages (the assembled prompt prefix) is
// always preserved, mirroring the fixed-head semantics used elsewhere in
// the engine. Even under the tightest budgets the gate never trims
// below a minimum viable window: the system head plus at least the
// final non-system message (the pending user turn) is always preserved;
// if that floor alone exceeds the budget it is kept regardless rather
// than collapsing the window to the system head alone. When the counter is nil, the limit is non-positive, or the
// window already fits, the input is returned unchanged.
//
// Expected:
//   - ctx carries cancellation; unused at present but reserved for future
//     counter implementations that perform I/O.
//   - msgs is the pending request window (possibly empty).
//   - counter supplies token estimation; nil disables the gate.
//   - contextLimit is the model's context window in tokens.
//   - maxTokens is the configured per-response output cap; the gate
//     reserves max(maxTokens, 1024) tokens, falling back to 4096 when
//     maxTokens is unset.
//
// Returns:
//   - A window that shares the input's backing array when no trimming
//     occurred, otherwise a freshly allocated window; and true when any
//     trimming was applied.
//
// Side effects:
//   - None.
func TrimForDispatchBudget(ctx context.Context, msgs []provider.Message, counter ctxstore.TokenCounter, contextLimit, maxTokens int) ([]provider.Message, bool) {
	if counter == nil || contextLimit <= 0 || len(msgs) == 0 {
		return msgs, false
	}

	reserve := maxTokens
	if reserve < 1024 {
		reserve = 1024
	}
	if reserve < 4096 {
		reserve = 4096
	}
	budget := contextLimit - reserve
	if budget <= 0 {
		budget = contextLimit / 2
	}

	total := estimateMessageTokens(msgs, counter)
	if total <= budget {
		return msgs, false
	}

	fixed := 0
	for fixed < len(msgs) && msgs[fixed].Role == "system" {
		fixed++
	}

	systemTokens := estimateMessageTokens(msgs[:fixed], counter)
	tail := msgs[fixed:]
	if len(tail) == 0 {
		return msgs, false
	}

	kept := make([]provider.Message, 0, len(tail))
	tailTokens := make([]int, len(tail)+1)
	for i := len(tail) - 1; i >= 0; i-- {
		tailTokens[i] = tailTokens[i+1] + counter.Count(tail[i].Content) + 8
	}
	drop := -1
	for i := 0; i <= len(tail); i++ {
		if systemTokens+tailTokens[i] <= budget {
			drop = i
			break
		}
	}
	if drop < 0 {
		drop = len(tail) - 1
	}
	if drop <= 0 {
		return msgs, false
	}

	kept = append(kept, msgs[:fixed]...)
	kept = append(kept, tail[drop:]...)
	return kept, true
}

// estimateMessageTokens sums the counter's estimate of every message's
// textual content plus a small per-message overhead so structural fields
// (roles, tool-call wrappers) are not treated as free.
func estimateMessageTokens(msgs []provider.Message, counter ctxstore.TokenCounter) int {
	total := 0
	for i := range msgs {
		total += counter.Count(msgs[i].Content) + 8
	}
	return total
}
