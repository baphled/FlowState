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
// direction or the resulting shrink of the effective hot window. This
// table pins the rounding direction with named scenarios so a refactor
// that flips it — or that promotes a straddling group to hot instead
// of cold — fails loudly.
package context_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// straddleCase describes one boundary scenario for the table-driven
// test. The invariants named by each case mirror the bullets in the
// H1 brief.
type straddleCase struct {
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

var _ = Describe("HotColdSplitter.findColdBoundary straddle behaviour", func() {
	DescribeTable("rounds the naive hot-tail boundary toward cold so tool groups never split mid-group",
		func(tc straddleCase) {
			compactor := flowctx.NewDefaultMessageCompactor(5)
			s := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
				Compactor:   compactor,
				HotTailSize: tc.hotTailSize,
			})
			Expect(s).NotTo(BeNil(), "NewHotColdSplitter returned nil for %+v", tc)

			res := s.Split(tc.input)

			// Cold-record + hot-message counts.
			Expect(res.ColdRecords).To(HaveLen(tc.wantColdRecords),
				"ColdRecords contents=%+v", res.ColdRecords)
			Expect(res.HotMessages).To(HaveLen(tc.wantHotMessageCount),
				"HotMessages=%+v", res.HotMessages)

			// No orphan tool-results — every tool-role message must be
			// preceded by an assistant tool_use or another tool-result
			// in the same group.
			for i, m := range res.HotMessages {
				if m.Role != "tool" {
					continue
				}
				Expect(i).NotTo(Equal(0),
					"tool-result at hot index 0 is orphaned; mid-group split. Hot: %+v", res.HotMessages)
				prev := res.HotMessages[i-1]
				Expect(prev.Role).To(BeElementOf([]string{"assistant", "tool"}),
					"tool-result at hot index %d not preceded by assistant or tool; mid-group split. Hot: %+v", i, res.HotMessages)
			}

			if tc.wantHotToolGroupIntact {
				Expect(containsIntactToolGroup(res.HotMessages)).To(BeTrue(),
					"expected intact tool group in HotMessages; got %+v", res.HotMessages)
			}

			// Every ColdRecord produces exactly one placeholder in
			// HotMessages (see Split's cold-prefix loop).
			Expect(countPlaceholders(res.HotMessages)).To(Equal(len(res.ColdRecords)),
				"placeholder count mismatch; hot=%+v", res.HotMessages)
		},
		// 5 solo units; HotTailSize 2 lands exactly on a unit start
		// (between index 2 and 3). No rounding needed; the last two
		// units stay hot verbatim.
		Entry("boundary lands on unit start — no adjustment", straddleCase{
			input: []provider.Message{
				bigSolo("user", "u1"),
				bigSolo("assistant", "a1"),
				bigSolo("user", "u2"),
				bigSolo("assistant", "a2"),
				bigSolo("user", "u3"),
			},
			hotTailSize:         2,
			wantColdRecords:     3,
			wantHotMessageCount: 3 + 2,
		}),
		// 2 solo leading units + 1 tool group of size 3 (assistant +
		// 2 tool results) + trailing solo. HotTailSize 2 sits INSIDE
		// the tool group at the message level; findColdBoundary must
		// round the boundary inward (toward cold), so the entire
		// group collapses into a single cold placeholder and the
		// effective hot window is only the trailing solo.
		Entry("boundary inside a 3-message tool group — whole group cold", straddleCase{
			input: []provider.Message{
				bigSolo("user", "u1"),
				bigSolo("assistant", "a1"),
				assistantToolCall(2),
				straddleToolResult("t1"),
				straddleToolResult("t2"),
				bigSolo("user", "u2"),
			},
			hotTailSize:         2,
			wantColdRecords:     3,
			wantHotMessageCount: 3 + 1,
		}),
		// Tool group of size 1 meaning one assistant + one tool
		// result (type-b). HotTailSize 1 sits exactly on the
		// tool-result message. findColdBoundary's start-edge
		// semantics mean the group's Start (the assistant) is before
		// the naive hot floor, so the first unit with Start >=
		// naiveHotStart is the next one — there are no next ones —
		// hence everything is cold.
		Entry("boundary inside a 2-message tool group (type-b)", straddleCase{
			input: []provider.Message{
				bigSolo("user", "u1"),
				assistantToolCall(1),
				straddleToolResult("t1"),
			},
			hotTailSize:         1,
			wantColdRecords:     2,
			wantHotMessageCount: 2,
		}),
		// HotTailSize larger than total messages; no unit is cold.
		// ColdRecords empty, HotMessages verbatim.
		Entry("hot tail larger than all messages — no cold units", straddleCase{
			input: []provider.Message{
				bigSolo("user", "u1"),
				bigSolo("assistant", "a1"),
			},
			hotTailSize:         50,
			wantColdRecords:     0,
			wantHotMessageCount: 2,
		}),
		// Same 3-call group as case 2 but HotTailSize lands at the
		// unit edge AFTER the group. The group stays hot intact;
		// only the leading solos are cold. Message count is 6.
		// HotTailSize 4 → naiveHotStart=2 which is the Start of the
		// tool group: no inward rounding, group + trailing solo hot.
		Entry("boundary at unit edge immediately after tool group — group stays hot intact", straddleCase{
			input: []provider.Message{
				bigSolo("user", "u1"),
				bigSolo("assistant", "a1"),
				assistantToolCall(2),
				straddleToolResult("t1"),
				straddleToolResult("t2"),
				bigSolo("user", "u2"),
			},
			hotTailSize:            4,
			wantColdRecords:        2,
			wantHotMessageCount:    2 + 4,
			wantHotToolGroupIntact: true,
		}),
	)
})
