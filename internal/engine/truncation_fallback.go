package engine

import (
	"context"

	"github.com/baphled/flowstate/internal/provider"
)

// truncationFallbackTailRatio is the fraction of the model context
// limit retained verbatim by the naive truncation fallback rebuild.
// A token-proportional tail replaces the historical fixed 50-message
// window so small-turn sessions keep materially more context while
// large-turn sessions still fit the budget.
const truncationFallbackTailRatio = 0.25

// rebuildTruncationFallbackWindow rebuilds the window when the
// summariser is unavailable: system prompt, todo context, the fallback
// summary line, and a token-proportional tail of the newest messages.
//
// Expected:
//   - ctx carries provider/model resolution keys.
//   - sessionID identifies the session for todo context.
//   - prior is the pre-compaction transcript.
//   - fallbackSummary is the placeholder summary line.
//   - contextLimit is the model context limit; non-positive falls back
//     to the engine model limit.
//   - liveUserMsg, when non-nil, is appended as the trailing turn.
//
// Returns:
//   - The rebuilt window, never nil.
//
// Side effects:
//   - None — the prior slice is never mutated.
func (e *Engine) rebuildTruncationFallbackWindow(ctx context.Context, sessionID string, prior []provider.Message, fallbackSummary string, contextLimit int, liveUserMsg *provider.Message) []provider.Message {
	if contextLimit <= 0 {
		contextLimit = e.ModelContextLimit()
	}
	tailTokenBudget := int(float64(contextLimit) * truncationFallbackTailRatio)
	if tailTokenBudget < 1 {
		tailTokenBudget = 1
	}
	tail := buildTokenProportionalTail(prior, tailTokenBudget, e.tokenCounter)
	rebuilt := make([]provider.Message, 0, len(tail)+4)
	rebuilt = append(rebuilt, provider.Message{Role: "system", Content: e.BuildSystemPromptCtx(ctx)})
	rebuilt = e.appendTodoContext(rebuilt, sessionID)
	rebuilt = append(rebuilt, provider.Message{Role: "assistant", Content: fallbackSummary})
	rebuilt = append(rebuilt, tail...)
	if liveUserMsg != nil {
		rebuilt = append(rebuilt, *liveUserMsg)
	}
	return rebuilt
}

// buildTokenProportionalTail returns the newest run of messages whose
// estimated token cost fits budget, keeping at least one
// message.
//
// Expected: msgs is the transcript; budget is the token budget.
// Returns: the tail slice sharing msgs's backing array.
// Side effects: None.
func buildTokenProportionalTail(msgs []provider.Message, budget int, counter interface{ Count(string) int }) []provider.Message {
	if len(msgs) == 0 {
		return msgs
	}
	if counter == nil {
		minKeep := len(msgs) - 1
		if minKeep < 0 {
			minKeep = 0
		}
		return msgs[minKeep:]
	}
	tokens := 0
	start := len(msgs) - 1
	for start >= 0 {
		tokens += counter.Count(msgs[start].Content)
		if tokens > budget && start < len(msgs)-1 {
			start++
			break
		}
		if tokens > budget {
			break
		}
		start--
	}
	if start < 0 {
		start = 0
	}
	return msgs[start:]
}
