package app

import (
	"math"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// cutDelegate is a delegate tool.call, left without a result unless a resultEvent follows.
func cutDelegate(id string) store.RecordedEvent {
	return logEvent(tool.TypeToolCall, map[string]any{"tool_call_id": id, "tool": "delegate", "args": map[string]any{}})
}

// cutTurn is one assistant turn with one answered call: 5 + 20 + 8 bytes for "said" of 5 bytes.
func cutTurn(id string) []store.RecordedEvent {
	return []store.RecordedEvent{saidEvent("said-" + id), callEvent(id), resultEvent(id)}
}

// cutLog numbers the events and joins turns: a task, then each part in order.
func cutLog(parts ...[]store.RecordedEvent) []store.RecordedEvent {
	events := []store.RecordedEvent{taskEvent("task")}
	for _, p := range parts {
		events = append(events, p...)
	}
	return numbered(events...)
}

func cutOne(e store.RecordedEvent) []store.RecordedEvent { return []store.RecordedEvent{e} }

var _ = Describe("ChooseCut", func() {
	// bytes per cutTurn("cN"): said-cN 7, call 4+16=20, result 8 = 35 bytes (about 9 tokens).
	DescribeTable("chooses the cut",
		func(events []store.RecordedEvent, keep int, want int64) {
			Expect(ChooseCut(events, keep)).To(Equal(want))
		},
		Entry("keeps the last turn", cutLog(cutTurn("c1"), cutTurn("c2"), cutTurn("c3")), 8, int64(7)),
		Entry("moves a point on a tool.result to the assistant.message of its group",
			cutLog(cutTurn("c1"), cutTurn("c2"), cutTurn("c3")), 1, int64(7)),
		Entry("moves a point on a tool.call to the assistant.message of its group",
			cutLog(cutTurn("c1"), cutTurn("c2"), cutTurn("c3")), 3, int64(7)),
		Entry("cuts earlier when more is kept", cutLog(cutTurn("c1"), cutTurn("c2"), cutTurn("c3")), 12, int64(4)),
		Entry("cuts at a later turn.start", cutLog(cutTurn("c1"), cutOne(taskEvent("task2")), cutTurn("c2")), 9, int64(4)),
		Entry("keeps the calls of a parallel delegate turn with their results", cutLog(cutTurn("c0"),
			cutOne(saidEvent("go")), cutOne(cutDelegate("d1")), cutOne(cutDelegate("d2")), cutOne(resultEvent("d1")),
			cutOne(resultEvent("d2")), cutTurn("c1")), 12, int64(4)),
		Entry("never lands on a late crash answer", cutLog(cutOne(saidEvent("go")), cutOne(cutDelegate("d1")), cutTurn("c2"), cutTurn("c3"),
			cutOne(resultEvent("d1"))), 1, int64(6)),
		Entry("skips a dangling call that is not a delegate", cutLog(cutOne(saidEvent("one")), cutOne(callEvent("c1")), cutTurn("c2"),
			cutTurn("c3")), 1, int64(6)),
		Entry("keeps a pending delegate call in the kept tail", cutLog(cutTurn("c1"), cutOne(saidEvent("go")), cutOne(cutDelegate("d1")),
			cutTurn("c2"), cutTurn("c3")), 1, int64(4)),
		Entry("is strictly after the previous compaction", cutLog(cutTurn("c1"), cutTurn("c2"), cutTurn("c3"),
			cutOne(compactedEvent(4, "SUM")), cutTurn("c4")), 1, int64(11)),
	)

	DescribeTable("finds no cut",
		func(events []store.RecordedEvent, keep int) {
			_, err := ChooseCut(events, keep)
			Expect(err).To(MatchError(ErrNoCut))
		},
		Entry("in one turn", cutLog(cutTurn("c1")), 1),
		Entry("when more is kept than the history holds", cutLog(cutTurn("c1"), cutTurn("c2"), cutTurn("c3")), 1000),
		Entry("before a pending delegate call of the first turn", cutLog(cutOne(saidEvent("go")), cutOne(cutDelegate("d1")),
			cutTurn("c2")), 1),
		Entry("at the previous compaction's first_kept_seq", cutLog(cutTurn("c1"), cutTurn("c2"), cutTurn("c3"),
			cutOne(compactedEvent(4, "SUM"))), 16),
		Entry("in a log of a task alone", cutLog(), 0),
		Entry("when the keep size is the largest int", cutLog(cutTurn("c1"), cutTurn("c2"), cutTurn("c3")), math.MaxInt),
		Entry("when the keep size overflows as bytes", cutLog(cutTurn("c1"), cutTurn("c2"), cutTurn("c3")), math.MaxInt/4+1),
	)

	It("refuses a log holding an invalid compaction", func() {
		_, err := ChooseCut(cutLog(cutTurn("c1"), cutOne(compactedEvent(99, "SUM"))), 1)
		Expect(err).To(MatchError(ErrInvalidCompaction))
	})

	It("never cuts at seq 0", func() {
		cut, _ := ChooseCut(cutLog(cutTurn("c1"), cutTurn("c2")), 0)
		Expect(cut).To(BeNumerically(">=", 1))
	})

	It("estimates what the rebuild replays", func() {
		events := cutLog(cutTurn("c1"), cutOne(logEvent(TypeUserMessage, map[string]any{"content": "more"})), cutTurn("c2"))
		facts, total := newReplayFacts(events), 0
		for _, e := range events {
			total += replayedBytes(e, facts)
		}
		want := 0
		for _, m := range rebuildHistory(events) {
			want += len(m.Content)
			for _, c := range m.ToolCalls {
				want += len(c.Name) + len(c.Arguments)
			}
		}
		Expect(total).To(Equal(want))
	})

	It("counts no bytes for a truncated message the loop retried, as the rebuild", func() {
		marker := logEvent(TypeProviderError, map[string]any{"reason": ReasonOutputTruncated, "terminal": false})
		events := cutLog(cutTurn("c1"), cutOne(logEvent(TypeAssistantMessage, map[string]any{"content": "TRUNCATED"})),
			cutOne(marker), cutTurn("c2"))
		facts, total := newReplayFacts(events), 0
		for _, e := range events {
			total += replayedBytes(e, facts)
		}
		want := 0
		for _, m := range rebuildHistory(events) {
			want += len(m.Content)
			for _, c := range m.ToolCalls {
				want += len(c.Name) + len(c.Arguments)
			}
		}
		Expect(total).To(Equal(want))
	})

	It("leaves the truncated message out of the summariser's transcript", func() {
		marker := logEvent(TypeProviderError, map[string]any{"reason": ReasonOutputTruncated, "terminal": false})
		events := cutLog(cutTurn("c1"), cutOne(logEvent(TypeAssistantMessage, map[string]any{"content": "TRUNCATED"})),
			cutOne(marker), cutTurn("c2"))
		Expect(strings.Join(spanEntries(events, events), "")).NotTo(ContainSubstring("TRUNCATED"))
	})

	It("counts no bytes for a dangling call", func() {
		Expect(replayedBytes(callEvent("c1"), replayFacts{})).To(Equal(0))
	})

	It("clips nothing it does not name", func() {
		Expect(strings.Contains(clip("abc", 5), "truncated")).To(BeFalse())
	})
})
