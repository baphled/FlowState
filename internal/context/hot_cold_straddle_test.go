// Package context — H1 regression coverage for HotColdSplitter's
// tool-group straddle behaviour.
//
// findColdBoundary rounds the naive hot-tail start inward toward the
// cold region whenever it lands inside a tool-call group: the whole
// group is promoted to cold rather than split. This is the ADR - Tool-
// Call Atomicity in Context Compaction invariant.
//
// The existing Ginkgo coverage asserts "no mid-group split" loosely
// (either fully hot or fully cold) but never pins down the rounding
// direction or the resulting shrink of the effective hot window. H1
// adds targeted table-driven cases so a refactor that accidentally
// flips rounding direction — or that promotes a straddling group to
// hot instead of cold — fails loudly with a named scenario.
package context_test

import (
	"strings"
	"testing"

	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// straddleCase describes one boundary scenario for the table-driven
// test. The invariants named by each case mirror the bullets in the
// H1 brief.
type straddleCase struct {
	name string
	// input is the transcript handed to Split.
	input []provider.Message
	// hotTailSize is the HotColdSplitterOptions value for this case.
	hotTailSize int
	// wantColdRecords is the expected number of ColdRecords in the
	// result — the unit count that rolled into cold.
	wantColdRecords int
	// wantHotMessageCount is the expected length of HotMessages. A
	// tool group of N messages collapses to 1 placeholder when cold;
	// solo units of M cold messages collapse to M placeholders.
	wantHotMessageCount int
	// wantHotToolGroupIntact is true when we expect the hot tail to
	// still contain a well-formed assistant+tool pair/triple rather
	// than a torn fragment.
	wantHotToolGroupIntact bool
}

// assistantToolCall builds an assistant message declaring N parallel
// tool calls with ids t1..tN. Content is padded so the message
// comfortably exceeds the micro-compactor token threshold used
// below (5).
func assistantToolCall(n int) provider.Message {
	calls := make([]provider.ToolCall, 0, n)
	for i := 1; i <= n; i++ {
		calls = append(calls, provider.ToolCall{ID: toolID(i), Name: "n"})
	}
	return provider.Message{
		Role:      "assistant",
		ToolCalls: calls,
		Content:   "calling " + strings.Repeat("x ", 60),
	}
}

// straddleToolResult builds a tool-result message matching an id produced by
// assistantToolCall.
func straddleToolResult(id string) provider.Message {
	return provider.Message{
		Role:      "tool",
		Content:   id + " result " + strings.Repeat("y ", 60),
		ToolCalls: []provider.ToolCall{{ID: id}},
	}
}

func toolID(i int) string {
	switch i {
	case 1:
		return "t1"
	case 2:
		return "t2"
	case 3:
		return "t3"
	}
	return "tN"
}

// bigSolo builds a solo message large enough to sit above the
// compaction threshold.
func bigSolo(role, marker string) provider.Message {
	return provider.Message{
		Role:    role,
		Content: marker + " " + strings.Repeat("word ", 60),
	}
}

// countPlaceholders returns the number of HotMessages whose content
// begins with the splitter's placeholder prefix. See spillUnit.
func countPlaceholders(msgs []provider.Message) int {
	n := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Content, "[compacted: ") {
			n++
		}
	}
	return n
}

// containsIntactToolGroup returns true when msgs contains an
// assistant message with ToolCalls followed by exactly len(ToolCalls)
// tool-role results for the same ids. A torn group (assistant without
// matching results, or tool-result without preceding assistant) returns
// false.
func containsIntactToolGroup(msgs []provider.Message) bool {
	for i := range msgs {
		if msgs[i].Role != "assistant" || len(msgs[i].ToolCalls) == 0 {
			continue
		}
		want := len(msgs[i].ToolCalls)
		end := i + 1 + want
		if end > len(msgs) {
			return false
		}
		for j := i + 1; j < end; j++ {
			if msgs[j].Role != "tool" {
				return false
			}
		}
		return true
	}
	return false
}

// TestHotColdSplitter_FindColdBoundary_Straddle is the H1 regression
// table. Each case pins an invariant about how findColdBoundary
// handles a naive hot-tail start that falls relative to tool-group
// edges.
func TestHotColdSplitter_FindColdBoundary_Straddle(t *testing.T) {
	t.Parallel()

	cases := []straddleCase{
		{
			// 5 solo units; HotTailSize 2 lands exactly on a unit
			// start (between index 2 and 3). No rounding needed; the
			// last two units stay hot verbatim.
			name: "boundary lands on unit start — no adjustment",
			input: []provider.Message{
				bigSolo("user", "u1"),
				bigSolo("assistant", "a1"),
				bigSolo("user", "u2"),
				bigSolo("assistant", "a2"),
				bigSolo("user", "u3"),
			},
			hotTailSize:         2,
			wantColdRecords:     3,
			wantHotMessageCount: 3 + 2, // 3 placeholders + 2 hot solos
		},
		{
			// 2 solo leading units + 1 tool group of size 3
			// (assistant + 2 tool results) + trailing solo.
			// HotTailSize 2 sits INSIDE the tool group at the
			// message level; findColdBoundary must round the
			// boundary inward (toward cold), so the entire group
			// collapses into a single cold placeholder and the
			// effective hot window is only the trailing solo.
			name: "boundary inside a 3-message tool group — whole group cold",
			input: []provider.Message{
				bigSolo("user", "u1"),
				bigSolo("assistant", "a1"),
				assistantToolCall(2), // group: [assistant, tool-t1, tool-t2]
				straddleToolResult("t1"),
				straddleToolResult("t2"),
				bigSolo("user", "u2"),
			},
			hotTailSize:     2, // message-level -> lands at index 4, inside the group
			wantColdRecords: 3, // u1 solo + a1 solo + 3-msg tool group
			// 3 placeholders (one per cold unit) + 1 hot solo.
			wantHotMessageCount: 3 + 1,
		},
		{
			// Tool group of size 1 meaning one assistant + one tool
			// result (type-b). HotTailSize 1 sits exactly on the
			// tool-result message. findColdBoundary's start-edge
			// semantics mean the group's Start (the assistant) is
			// before the naive hot floor, so the first unit with
			// Start >= naiveHotStart is the next one — there are
			// no next ones — hence everything is cold. This is the
			// edge case the brief flags: a straddle of a 1-call
			// group is still "no straddle" in the unit sense, but
			// the group gets rounded cold anyway, never torn.
			name: "boundary inside a 2-message tool group (type-b)",
			input: []provider.Message{
				bigSolo("user", "u1"),
				assistantToolCall(1), // [assistant, tool-t1]
				straddleToolResult("t1"),
			},
			hotTailSize:     1, // lands on the tool-result mid-group
			wantColdRecords: 2, // u1 + group
			// 2 placeholders, no hot solo survived.
			wantHotMessageCount: 2,
		},
		{
			// HotTailSize larger than total messages; no unit is
			// cold. ColdRecords empty, HotMessages verbatim.
			name: "hot tail larger than all messages — no cold units",
			input: []provider.Message{
				bigSolo("user", "u1"),
				bigSolo("assistant", "a1"),
			},
			hotTailSize:         50,
			wantColdRecords:     0,
			wantHotMessageCount: 2,
		},
		{
			// Same 3-call group as case 2 but HotTailSize lands at
			// the unit edge AFTER the group. The group stays hot
			// intact; only the leading solos are cold.
			name: "boundary at unit edge immediately after tool group — group stays hot intact",
			input: []provider.Message{
				bigSolo("user", "u1"),
				bigSolo("assistant", "a1"),
				assistantToolCall(2),
				straddleToolResult("t1"),
				straddleToolResult("t2"),
				bigSolo("user", "u2"),
			},
			// Message count is 6. HotTailSize 4 → naiveHotStart=2
			// which is the Start of the tool group: no inward
			// rounding, group + trailing solo hot.
			hotTailSize:            4,
			wantColdRecords:        2, // u1 + a1
			wantHotMessageCount:    2 + 4,
			wantHotToolGroupIntact: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runStraddleCase(t, tc)
		})
	}
}

// runStraddleCase executes one straddle scenario and reports all
// invariant violations. Extracted from the outer loop so the test
// function's cognitive complexity stays within the repo linter's
// ceiling and each assertion cluster has a named home.
func runStraddleCase(t *testing.T, tc straddleCase) {
	t.Helper()

	compactor := flowctx.NewDefaultMessageCompactor(5)
	s := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
		Compactor:   compactor,
		HotTailSize: tc.hotTailSize,
	})
	if s == nil {
		t.Fatalf("NewHotColdSplitter returned nil for %+v", tc)
	}

	res := s.Split(tc.input)

	assertStraddleCounts(t, tc, res)
	assertNoOrphanToolResults(t, res)
	if tc.wantHotToolGroupIntact && !containsIntactToolGroup(res.HotMessages) {
		t.Errorf("expected intact tool group in HotMessages; got %+v", res.HotMessages)
	}
	// Every ColdRecord produces exactly one placeholder in
	// HotMessages (see Split's cold-prefix loop).
	if got := countPlaceholders(res.HotMessages); got != len(res.ColdRecords) {
		t.Errorf("placeholder count %d != ColdRecords length %d; hot=%+v", got, len(res.ColdRecords), res.HotMessages)
	}
}

// assertStraddleCounts checks the cold-record and hot-message counts
// against the scenario's expected values.
func assertStraddleCounts(t *testing.T, tc straddleCase, res flowctx.SplitResult) {
	t.Helper()
	if len(res.ColdRecords) != tc.wantColdRecords {
		t.Errorf(
			"ColdRecords: got %d (contents=%+v), want %d",
			len(res.ColdRecords), res.ColdRecords, tc.wantColdRecords,
		)
	}
	if len(res.HotMessages) != tc.wantHotMessageCount {
		t.Errorf(
			"HotMessages length: got %d, want %d; messages=%+v",
			len(res.HotMessages), tc.wantHotMessageCount, res.HotMessages,
		)
	}
}

// assertNoOrphanToolResults walks the hot tail and fails when a
// tool-role message appears without a preceding assistant tool_use
// or prior tool-result in the same group. This catches mid-group
// splits regardless of the numeric count expectations.
func assertNoOrphanToolResults(t *testing.T, res flowctx.SplitResult) {
	t.Helper()
	for i, m := range res.HotMessages {
		if m.Role != "tool" {
			continue
		}
		if i == 0 {
			t.Errorf("tool-result at hot index 0 is orphaned; mid-group split. Hot: %+v", res.HotMessages)
			continue
		}
		prev := res.HotMessages[i-1]
		if prev.Role != "assistant" && prev.Role != "tool" {
			t.Errorf("tool-result at hot index %d not preceded by assistant or tool; mid-group split. Hot: %+v", i, res.HotMessages)
		}
	}
}
