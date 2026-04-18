package swarmactivity

import (
	"github.com/baphled/flowstate/internal/streaming"
)

// coalesceToolCalls collapses tool_call / tool_result event pairs into a
// single body line keyed on SwarmEvent.ID, so that every tool invocation
// surfaces as one entry in the activity timeline rather than two.
//
// The coalesce step is pure — it takes the current events slice and a
// visibility filter and returns the ordered, renderable body strings. This
// keeps the state machine independent of the TUI render pipeline and makes
// it directly unit-testable (see coalesce_test.go).
//
// Rules:
//   - A tool_call with a matching tool_result (same ID) renders one line
//     whose status is derived from the result: "completed" when
//     Status == "completed", "error" (a.k.a. failed) when Status == "error".
//   - A tool_call without a matching result renders as its current status
//     (typically "started" or "running").
//   - A bare tool_result with no matching tool_call is dropped from the
//     pane — it's still observable via the event-details modal (Ctrl+E)
//     because the store keeps it. This avoids ghost "result" lines on the
//     timeline after coalescing.
//   - All other event types (delegation, plan, review) pass through
//     untouched.
//   - The visibility filter is applied per-event: hidden types are dropped
//     before the coalesce step, so the filter indicator's count remains
//     accurate.
//
// Expected:
//   - events is the ordered list of events (oldest first).
//   - visibleTypes maps each SwarmEventType to its visibility; missing keys
//     are treated as hidden.
//
// Returns:
//   - An ordered slice of pre-styled body strings, one per surviving event
//     row. Callers further truncate/style these when rendering.
//
// Side effects:
//   - None.
func coalesceToolCalls(events []streaming.SwarmEvent, visibleTypes map[streaming.SwarmEventType]bool) []string {
	// First pass: index tool_result events by ID so tool_call rows can pair
	// with them in O(1). A second tool_result for the same ID would shadow
	// the first; the latest one wins, which matches how providers emit
	// status updates.
	resultByID := make(map[string]streaming.SwarmEvent)
	for _, ev := range events {
		if ev.Type == streaming.EventToolResult {
			resultByID[ev.ID] = ev
		}
	}

	out := make([]string, 0, len(events))
	for _, ev := range events {
		if !visibleTypes[ev.Type] {
			continue
		}
		switch ev.Type {
		case streaming.EventToolResult:
			// Drop: the matching tool_call line (if any) already carries
			// the derived status. Orphan results are intentionally
			// suppressed from the pane; the event-details modal still
			// surfaces them.
			continue
		case streaming.EventToolCall:
			if result, ok := resultByID[ev.ID]; ok {
				// Pair found — derive the displayed status from the
				// result, so the coalesced line reflects completion or
				// failure, not the stale "started" from the call chunk.
				paired := ev
				paired.Status = deriveStatus(result)
				out = append(out, formatEvent(paired))
				continue
			}
			// No result yet — render the call with its current status
			// (started, running, etc.).
			out = append(out, formatEvent(ev))
		default:
			out = append(out, formatEvent(ev))
		}
	}
	return out
}

// deriveStatus extracts the user-facing status string to show on a
// coalesced tool_call line once its tool_result has arrived.
//
// Expected:
//   - result is the tool_result event paired with a tool_call.
//
// Returns:
//   - "error" when the result indicates failure (Status == "error" or the
//     is_error metadata flag is true); otherwise the result's Status
//     value, defaulting to "completed" when empty.
//
// Side effects:
//   - None.
func deriveStatus(result streaming.SwarmEvent) string {
	if result.Status == "error" {
		return "error"
	}
	if result.Metadata != nil {
		if flag, ok := result.Metadata["is_error"].(bool); ok && flag {
			return "error"
		}
	}
	if result.Status == "" {
		return "completed"
	}
	return result.Status
}
