package app

import (
	"errors"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// ErrNoCut reports that no eligible cut point exists: nothing new can be summarised
// without breaking a rule of ADR-010 (reason no_cut). Nothing is written.
var ErrNoCut = errors.New("no_cut: nothing new to compact")

// bytesPerToken is the ADR-010 estimate: four bytes make one token.
const bytesPerToken = 4

// ChooseCut returns the first_kept_seq of a compaction of events, the log of one session
// (events[i].Seq == i, as Store.Replay returns it). It walks back from the end of the log
// accumulating the estimated size of what the rebuild replays (bytes/4, see replayedBytes)
// until keepRecentTokens are kept, then takes the nearest eligible position at or before
// that point: an eligible position starts a message group, so it is a turn.start (seq >= 1) or
// the assistant.message of a turn, never a tool.call or tool.result. A cut that cannot be
// had there moves earlier, never later. The cut is also strictly greater than the previous
// valid compaction's first_kept_seq (a lower one makes the session unrunnable), never past
// the start of the turn that holds a delegate call still without a result (its children may be
// running), and, for the first compaction, leaves something to summarise beyond the original
// task, which the rebuild carries anyway. Otherwise it returns ErrNoCut; a log holding an invalid compaction returns that
// error. The log is only read.
func ChooseCut(events []store.RecordedEvent, keepRecentTokens int) (int64, error) {
	valid, _, err := scanCompactions(events)
	if err != nil {
		return 0, err
	}
	spanStart := int64(0)
	if n := len(valid); n > 0 {
		spanStart = valid[n-1].FirstKeptSeq
	}
	answered := answeredResultIDs(events)
	point, ok := keepPoint(events, answered, max(keepRecentTokens, 0)*bytesPerToken, int(spanStart))
	if !ok {
		return 0, ErrNoCut
	}
	for _, i := range unansweredDelegateCalls(events) {
		point = min(point, groupStart(events, i))
	}
	for i := point; i > int(spanStart) && i >= 1; i-- {
		if cutEligible(events[i]) && (spanStart > 0 || summarisable(events[:i], answered)) {
			return events[i].Seq, nil
		}
	}
	return 0, ErrNoCut
}

// keepPoint is the index where the bytes replayed from the end of the log first reach keep,
// looking no further back than from.
func keepPoint(events []store.RecordedEvent, answered map[string]bool, keep, from int) (int, bool) {
	kept := 0
	for i := len(events) - 1; i >= from; i-- {
		if kept += replayedBytes(events[i], answered); kept >= keep {
			return i, true
		}
	}
	return 0, false
}

// cutEligible reports whether the rebuild starts a message group at e.
func cutEligible(e store.RecordedEvent) bool {
	return e.Type == TypeAssistantMessage || (e.Type == session.TypeTurnStart && e.Seq >= 1)
}

// groupStart is the index of the message group that holds events[i]: the nearest eligible
// event at or before it, or 0.
func groupStart(events []store.RecordedEvent, i int) int {
	for ; i > 0 && !cutEligible(events[i]); i-- {
	}
	return i
}

// summarisable reports whether span holds anything the rebuild replays other than the log's
// first turn.start, the task the first message carries verbatim.
func summarisable(span []store.RecordedEvent, answered map[string]bool) bool {
	for _, e := range span {
		switch {
		case e.Type == session.TypeTurnStart && e.Seq == 0:
		case replayedType(e, answered):
			return true
		}
	}
	return false
}

// replayedType reports whether the rebuild turns e into message content: user and assistant
// text, the task, an answered call and any result with an id. A dangling or uncorrelated call
// is skipped by the rebuild, so it is neither summarised nor counted.
func replayedType(e store.RecordedEvent, answered map[string]bool) bool {
	id := payloadFieldText(e.PayloadJSON, "tool_call_id")
	switch e.Type {
	case session.TypeTurnStart, TypeUserMessage, TypeAssistantMessage:
		return true
	case tool.TypeToolCall:
		return id != "" && answered[id]
	case tool.TypeToolResult:
		return id != ""
	}
	return false
}

// replayedBytes is the size of what the rebuild sends for e: the content of a message, the
// name and arguments of a call, the result or error JSON of a result. It mirrors
// rebuildHistory (a spec checks the two agree) and, unlike it, does not drop the echo of the
// first task, so it can only overcount.
func replayedBytes(e store.RecordedEvent, answered map[string]bool) int {
	if !replayedType(e, answered) {
		return 0
	}
	switch e.Type {
	case session.TypeTurnStart:
		return len(payloadFieldText(e.PayloadJSON, "task"))
	case TypeUserMessage, TypeAssistantMessage:
		return len(payloadFieldText(e.PayloadJSON, "content"))
	case tool.TypeToolCall:
		return len(payloadFieldText(e.PayloadJSON, "tool")) + len(payloadFieldRaw(e.PayloadJSON, "args"))
	}
	return len(resultText(e.PayloadJSON))
}

// resultText is the content of the tool message the rebuild makes of a result payload.
func resultText(payloadJSON string) string {
	if res := payloadFieldRaw(payloadJSON, "result"); res != "null" {
		return res
	}
	return payloadFieldRaw(payloadJSON, "error")
}
