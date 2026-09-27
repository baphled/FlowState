package engine

import (
	"context"
	"strings"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// truncationFallbackSummaryPrefix marks summaries produced by the
// naive truncation fallback (summariser unavailable or errored).
const truncationFallbackSummaryPrefix = "[truncation fallback: "

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
// than collapsing the window to the system head alone. A leading
// assistant summary placeholder produced by the naive truncation
// fallback (recognised by the "[truncation fallback: " prefix) is
// treated as part of the protected head alongside the system messages,
// so budget trimming can never strip the summary away from the tail it
// summarises. When the counter is nil, the limit is non-positive, or the
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

	budget := dispatchBudget(contextLimit, maxTokens)
	if estimateMessageTokens(msgs, counter) <= budget {
		return msgs, false
	}

	fixed := protectedPrefixLen(msgs)
	tail := msgs[fixed:]
	if len(tail) == 0 {
		return msgs, false
	}

	drop := budgetDropCount(tail, estimateMessageTokens(msgs[:fixed], counter), counter, budget)
	if drop <= 0 {
		return msgs, false
	}

	kept := make([]provider.Message, 0, len(msgs)-drop)
	kept = append(kept, msgs[:fixed]...)
	kept = append(kept, tail[drop:]...)
	return kept, true
}

// dispatchBudget computes the token budget available for the request
// window: the context limit minus a reserve for the completion (at
// least 4096 tokens, or the configured per-response cap when larger).
// A non-positive result falls back to half the context limit.
//
// Expected:
//   - contextLimit is the model's context window in tokens (positive).
//   - maxTokens is the configured per-response output cap; 0 or
//     negative values fall back to the built-in reserve.
//
// Returns:
//   - The token budget for the request window.
//
// Side effects:
//   - None.
func dispatchBudget(contextLimit, maxTokens int) int {
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
	return budget
}

// protectedPrefixLen returns the length of the leading run of messages
// that must never be trimmed: the system prompt prefix followed by any
// assistant summary placeholder produced by the naive truncation
// fallback.
//
// Expected:
//   - msgs is the pending request window (possibly empty).
//
// Returns:
//   - The number of leading messages that must never be trimmed.
//
// Side effects:
//   - None.
func protectedPrefixLen(msgs []provider.Message) int {
	fixed := 0
	for fixed < len(msgs) && msgs[fixed].Role == "system" {
		fixed++
	}
	for fixed < len(msgs) && msgs[fixed].Role == "assistant" && strings.HasPrefix(msgs[fixed].Content, truncationFallbackSummaryPrefix) {
		fixed++
	}
	return fixed
}

// budgetDropCount returns how many of the oldest messages in tail can
// be dropped so the remaining tokens (plus systemTokens) fit within
// budget. It never drops below a minimum viable window: if no prefix of
// the tail fits, or the only fitting prefix would empty the tail, the
// final message is always kept. Returns 0 when nothing should be
// dropped.
//
// Expected:
//   - tail is the trimmable suffix of the window (non-empty).
//   - systemTokens is the estimated token cost of the protected head.
//   - counter is a non-nil TokenCounter.
//   - budget is the dispatch budget from dispatchBudget.
//
// Returns:
//   - The number of oldest tail messages to drop; always leaves at
//     least the final tail message in place.
//
// Side effects:
//   - None.
func budgetDropCount(tail []provider.Message, systemTokens int, counter ctxstore.TokenCounter, budget int) int {
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
	if drop < 0 || drop >= len(tail) {
		drop = len(tail) - 1
	}
	return drop
}

// estimateMessageTokens sums the counter's estimate of every message's
// textual content plus a small per-message overhead so structural fields
// (roles, tool-call wrappers) are not treated as free.
//
// Expected:
//   - msgs may be empty; counter is a non-nil TokenCounter.
//
// Returns:
//   - The estimated total token cost of msgs.
//
// Side effects:
//   - None.
func estimateMessageTokens(msgs []provider.Message, counter ctxstore.TokenCounter) int {
	total := 0
	for i := range msgs {
		total += counter.Count(msgs[i].Content) + 8
	}
	return total
}
