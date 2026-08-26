package context

import "github.com/baphled/flowstate/internal/provider"

// UnitKind classifies a compactable unit.
//
// See ADR - Tool-Call Atomicity in Context Compaction for the full taxonomy.
// Every entry returned by walkUnits carries exactly one kind and describes a
// contiguous, indivisible range of provider.Message values.
type UnitKind int

const (
	// UnitSolo is a self-contained message with no cross-message dependency:
	// role "user", "system", or "assistant" with no tool_use blocks.
	UnitSolo UnitKind = iota
	// UnitToolGroup is an assistant message carrying one or more tool_use
	// blocks followed immediately by its matching tool-result messages. A
	// group of N parallel tool calls occupies N+1 consecutive messages.
	UnitToolGroup
)

// Unit describes a compactable unit as a half-open index range into the
// caller's message slice. Start is inclusive, End is exclusive.
//
// A single-pair tool call (type-b in the ADR) is represented by
// Kind=UnitToolGroup with End-Start == 2. A parallel fan-out of N calls
// (type-c) has End-Start == N+1. Solo units always satisfy End-Start == 1.
type Unit struct {
	// Kind classifies the unit.
	Kind UnitKind
	// Start is the inclusive starting index in the caller-provided slice.
	Start int
	// End is the exclusive ending index in the caller-provided slice.
	End int
}

// walkUnits partitions msgs into a sequence of compactable units preserving
// tool-call atomicity. It is the single authoritative source of unit
// boundaries for the context compression system.
//
// Expected:
//   - msgs is a slice of provider.Message values as they appear in the
//     canonical transcript (typically a copy — walkUnits never mutates).
//
// Returns:
//   - A slice of Unit values covering msgs end-to-end with no gaps and no
//     overlaps; the concatenation of (u.Start, u.End) equals [0, len(msgs)).
//   - nil when msgs is malformed in a way that would force a unit to be
//     split: an assistant message announces N tool_use blocks but fewer
//     than N trailing tool-result messages follow, OR a tool-result
//     message appears where one is not expected (no preceding assistant
//     tool_use turn to anchor it), OR a trailing tool-result carries a
//     zero or multi-id ToolCalls slice that cannot satisfy the pairing.
//
// Side effects:
//   - None. Pure function over the input slice. Does not mutate msgs.
//
// Callers must not compact across a nil return — the transcript is
// structurally invalid for the provider wire and must be surfaced to the
// caller as an unrecoverable error rather than silently reshaped.
//
// See ADR - Tool-Call Atomicity in Context Compaction for the invariant.
func walkUnits(msgs []provider.Message) []Unit {
	if len(msgs) == 0 {
		return []Unit{}
	}

	units := make([]Unit, 0, len(msgs))

	i := 0
	for i < len(msgs) {
		m := msgs[i]

		switch {
		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			end, ok := validateToolGroup(msgs, i)
			if !ok {
				return nil
			}
			units = append(units, Unit{Kind: UnitToolGroup, Start: i, End: end})
			i = end
		case m.Role == "tool":
			// A tool-role message outside a group is structurally orphaned.
			return nil
		default:
			units = append(units, Unit{Kind: UnitSolo, Start: i, End: i + 1})
			i++
		}
	}

	return units
}

// validateToolGroup verifies the assistant message at msgs[start] and its N
// trailing tool-result messages form a well-formed tool group. See the
// Compaction Atomicity Invariant in ADR - Tool-Call Atomicity in Context
// Compaction for the structural rules.
//
// Expected:
//   - msgs[start] is an assistant message whose ToolCalls slice is non-empty.
//   - start is in range [0, len(msgs)).
//
// Returns:
//   - The exclusive end index of the group and true when every declared
//     tool_use id is matched by exactly one trailing tool-result message.
//   - 0 and false when the group is truncated, contains a duplicate or
//     empty id, or any trailing message is not a single-id tool-result for
//     an outstanding declared id.
//
// Side effects:
//   - None. Pure function over msgs.
func validateToolGroup(msgs []provider.Message, start int) (int, bool) {
	calls := msgs[start].ToolCalls
	end := start + 1 + len(calls)
	if end > len(msgs) {
		return 0, false
	}

	declared := make(map[string]bool, len(calls))
	for _, tc := range calls {
		if tc.ID == "" || declared[tc.ID] {
			return 0, false
		}
		declared[tc.ID] = true
	}

	for j := start + 1; j < end; j++ {
		if !consumeToolResult(msgs[j], declared) {
			return 0, false
		}
	}
	// Loop invariant: every iteration deletes exactly one entry from
	// `declared`, and `!declared[id]` catches duplicates. After len(calls)
	// iterations declared is necessarily empty.

	return end, true
}

// consumeToolResult checks that msg is a tool-result for one of the ids in
// declared, and removes that id from the set on success.
//
// Expected:
//   - msg is the message to validate as a tool-result.
//   - declared is the live set of outstanding tool_use ids. May be mutated
//     on success; never mutated on failure.
//
// Returns:
//   - true when msg has Role "tool", carries exactly one ToolCall, and
//     that ToolCall's ID is currently in declared.
//   - false otherwise.
//
// Side effects:
//   - Removes the matched id from declared on success.
func consumeToolResult(msg provider.Message, declared map[string]bool) bool {
	if msg.Role != "tool" || len(msg.ToolCalls) != 1 {
		return false
	}
	id := msg.ToolCalls[0].ID
	if !declared[id] {
		return false
	}
	delete(declared, id)
	return true
}
