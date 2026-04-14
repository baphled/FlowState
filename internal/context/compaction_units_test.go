package context_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// solo constructs a role-only provider.Message for walker input.
func solo(role, content string) provider.Message {
	return provider.Message{Role: role, Content: content}
}

// asstCalls constructs an assistant message announcing the given tool-call ids.
func asstCalls(ids ...string) provider.Message {
	calls := make([]provider.ToolCall, 0, len(ids))
	for _, id := range ids {
		calls = append(calls, provider.ToolCall{ID: id, Name: "noop"})
	}
	return provider.Message{Role: "assistant", ToolCalls: calls}
}

// toolResult constructs a tool-role result message keyed to a single id.
func toolResult(id, content string) provider.Message {
	return provider.Message{
		Role:      "tool",
		Content:   content,
		ToolCalls: []provider.ToolCall{{ID: id}},
	}
}

var _ = Describe("walkUnits", func() {
	// Exhaustive cases from ADR - Tool-Call Atomicity in Context Compaction,
	// Implementation Notes §Unit tests.

	type walkCase struct {
		name     string
		msgs     []provider.Message
		expected []flowctx.Unit
		// nilExpected indicates a malformed case: walkUnits must return nil.
		nilExpected bool
	}

	cases := []walkCase{
		{
			name:     "empty input returns empty (non-nil) slice",
			msgs:     nil,
			expected: []flowctx.Unit{},
		},
		{
			name: "solo runs only — user/system/assistant without tools",
			msgs: []provider.Message{
				solo("user", "hi"),
				solo("assistant", "hello"),
				solo("system", "note"),
			},
			expected: []flowctx.Unit{
				{Kind: flowctx.UnitSolo, Start: 0, End: 1},
				{Kind: flowctx.UnitSolo, Start: 1, End: 2},
				{Kind: flowctx.UnitSolo, Start: 2, End: 3},
			},
		},
		{
			name: "single tool pair — one tool_use + one tool_result",
			msgs: []provider.Message{
				asstCalls("t1"),
				toolResult("t1", "ok"),
			},
			expected: []flowctx.Unit{
				{Kind: flowctx.UnitToolGroup, Start: 0, End: 2},
			},
		},
		{
			name: "2-way parallel fan-out",
			msgs: []provider.Message{
				asstCalls("a", "b"),
				toolResult("a", "A"),
				toolResult("b", "B"),
			},
			expected: []flowctx.Unit{
				{Kind: flowctx.UnitToolGroup, Start: 0, End: 3},
			},
		},
		{
			name: "8-way parallel fan-out",
			msgs: []provider.Message{
				asstCalls("i1", "i2", "i3", "i4", "i5", "i6", "i7", "i8"),
				toolResult("i1", "r1"),
				toolResult("i2", "r2"),
				toolResult("i3", "r3"),
				toolResult("i4", "r4"),
				toolResult("i5", "r5"),
				toolResult("i6", "r6"),
				toolResult("i7", "r7"),
				toolResult("i8", "r8"),
			},
			expected: []flowctx.Unit{
				{Kind: flowctx.UnitToolGroup, Start: 0, End: 9},
			},
		},
		{
			name: "two adjacent pairs",
			msgs: []provider.Message{
				asstCalls("x"),
				toolResult("x", "X"),
				asstCalls("y"),
				toolResult("y", "Y"),
			},
			expected: []flowctx.Unit{
				{Kind: flowctx.UnitToolGroup, Start: 0, End: 2},
				{Kind: flowctx.UnitToolGroup, Start: 2, End: 4},
			},
		},
		{
			name: "pair followed by solos",
			msgs: []provider.Message{
				asstCalls("p"),
				toolResult("p", "P"),
				solo("assistant", "afterword"),
				solo("user", "follow-up"),
			},
			expected: []flowctx.Unit{
				{Kind: flowctx.UnitToolGroup, Start: 0, End: 2},
				{Kind: flowctx.UnitSolo, Start: 2, End: 3},
				{Kind: flowctx.UnitSolo, Start: 3, End: 4},
			},
		},
		{
			name: "solos followed by pair",
			msgs: []provider.Message{
				solo("user", "request"),
				solo("assistant", "thinking"),
				asstCalls("z"),
				toolResult("z", "Z"),
			},
			expected: []flowctx.Unit{
				{Kind: flowctx.UnitSolo, Start: 0, End: 1},
				{Kind: flowctx.UnitSolo, Start: 1, End: 2},
				{Kind: flowctx.UnitToolGroup, Start: 2, End: 4},
			},
		},
		{
			name: "solos interleaved with multiple groups",
			msgs: []provider.Message{
				solo("user", "u1"),
				asstCalls("g1a", "g1b"),
				toolResult("g1a", "A"),
				toolResult("g1b", "B"),
				solo("assistant", "reflecting"),
				asstCalls("g2"),
				toolResult("g2", "Z"),
				solo("user", "thanks"),
			},
			expected: []flowctx.Unit{
				{Kind: flowctx.UnitSolo, Start: 0, End: 1},
				{Kind: flowctx.UnitToolGroup, Start: 1, End: 4},
				{Kind: flowctx.UnitSolo, Start: 4, End: 5},
				{Kind: flowctx.UnitToolGroup, Start: 5, End: 7},
				{Kind: flowctx.UnitSolo, Start: 7, End: 8},
			},
		},
		// Malformed cases — the walker must refuse (return nil).
		{
			name: "malformed: truncated tool group (missing result)",
			msgs: []provider.Message{
				asstCalls("t1", "t2"),
				toolResult("t1", "only"),
			},
			nilExpected: true,
		},
		{
			name: "malformed: orphan tool-result with no preceding tool_use",
			msgs: []provider.Message{
				solo("user", "hi"),
				toolResult("x", "dangling"),
			},
			nilExpected: true,
		},
		{
			name: "malformed: tool-result id does not match declared tool_use",
			msgs: []provider.Message{
				asstCalls("expected"),
				toolResult("wrong-id", "oops"),
			},
			nilExpected: true,
		},
		{
			name: "malformed: duplicate declared tool_use ids",
			msgs: []provider.Message{
				asstCalls("dup", "dup"),
				toolResult("dup", "one"),
				toolResult("dup", "two"),
			},
			nilExpected: true,
		},
		{
			name: "malformed: empty tool_use id",
			msgs: []provider.Message{
				{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: ""}}},
				toolResult("", "empty"),
			},
			nilExpected: true,
		},
		{
			name: "malformed: tool-result carries multiple ids (single-id convention required)",
			msgs: []provider.Message{
				asstCalls("a", "b"),
				{Role: "tool", Content: "A+B", ToolCalls: []provider.ToolCall{{ID: "a"}, {ID: "b"}}},
				toolResult("b", "B"),
			},
			nilExpected: true,
		},
		{
			name: "malformed: tool-result carries zero ids",
			msgs: []provider.Message{
				asstCalls("a"),
				{Role: "tool", Content: "anonymous", ToolCalls: nil},
			},
			nilExpected: true,
		},
		{
			name: "malformed: non-tool role where tool-result required",
			msgs: []provider.Message{
				asstCalls("t"),
				solo("user", "interrupts the group"),
			},
			nilExpected: true,
		},
	}

	for _, tc := range cases {
		It(tc.name, func() {
			got := flowctx.ExportedWalkUnits(tc.msgs)
			if tc.nilExpected {
				Expect(got).To(BeNil())
				return
			}
			Expect(got).To(Equal(tc.expected))
		})
	}

	Describe("invariant: units cover input end-to-end", func() {
		It("concatenation of (Start,End) covers [0,len(msgs)) with no gaps", func() {
			msgs := []provider.Message{
				solo("user", "u"),
				asstCalls("q"),
				toolResult("q", "R"),
				solo("user", "v"),
			}
			units := flowctx.ExportedWalkUnits(msgs)
			Expect(units).NotTo(BeNil())

			expected := 0
			for _, u := range units {
				Expect(u.Start).To(Equal(expected))
				expected = u.End
			}
			Expect(expected).To(Equal(len(msgs)))
		})
	})

	Describe("invariant: walkUnits does not mutate input", func() {
		It("leaves the caller's slice element-equal after the call", func() {
			msgs := []provider.Message{
				solo("user", "original"),
				asstCalls("k"),
				toolResult("k", "kept"),
			}
			before := make([]provider.Message, len(msgs))
			copy(before, msgs)

			_ = flowctx.ExportedWalkUnits(msgs)

			Expect(msgs).To(Equal(before))
		})
	})
})

// FuzzWalkUnitsRoundTrip verifies the walker never panics on arbitrary input
// and, when it returns a non-nil result, the units cover the whole range
// with no gaps and no overlaps.
func FuzzWalkUnitsRoundTrip(f *testing.F) {
	f.Add(0)
	f.Add(1)
	f.Add(4)
	f.Add(9)

	f.Fuzz(func(t *testing.T, n int) {
		msgs := buildFuzzMessages(n)

		units := flowctx.ExportedWalkUnits(msgs)
		if units == nil {
			return
		}
		assertUnitsCover(t, units, len(msgs))
	})
}

// buildFuzzMessages constructs a deterministic, well-formed message list
// from the fuzz seed n. The rotation guarantees only valid inputs so the
// fuzz target exercises the success path; malformed inputs are covered by
// the table-driven cases above.
func buildFuzzMessages(n int) []provider.Message {
	if n < 0 {
		n = -n
	}
	n %= 32

	msgs := make([]provider.Message, 0, n)
	for i := range n {
		switch i % 4 {
		case 0:
			msgs = append(msgs, provider.Message{Role: "user", Content: "u"})
		case 1:
			msgs = append(msgs, provider.Message{Role: "assistant", Content: "a"})
		case 2:
			id := "f" + string(rune('a'+i%26))
			msgs = append(msgs,
				provider.Message{
					Role:      "assistant",
					ToolCalls: []provider.ToolCall{{ID: id, Name: "n"}},
				},
				provider.Message{
					Role:      "tool",
					Content:   "r",
					ToolCalls: []provider.ToolCall{{ID: id}},
				},
			)
		case 3:
			msgs = append(msgs, provider.Message{Role: "system", Content: "s"})
		}
	}
	return msgs
}

// assertUnitsCover fails the test when units do not tile [0,total) exactly.
func assertUnitsCover(t *testing.T, units []flowctx.Unit, total int) {
	t.Helper()
	expected := 0
	for _, u := range units {
		if u.Start != expected {
			t.Fatalf("gap: unit.Start=%d expected=%d", u.Start, expected)
		}
		if u.End <= u.Start {
			t.Fatalf("degenerate unit: %+v", u)
		}
		expected = u.End
	}
	if expected != total {
		t.Fatalf("coverage: final End=%d len=%d", expected, total)
	}
}
