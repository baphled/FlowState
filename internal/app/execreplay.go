package app

import (
	"encoding/json"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// rebuildHistory rebuilds the provider history of a resumed or recovered run
// from its log, in the shape the live loop sends (see the differences below): the task (turn.start) and
// any user.message as user messages; per assistant.message ONE assistant
// message carrying its content and every correlated, answered tool.call of the
// turn, followed by the tool results in log order.
//
// A dangling (no result), uncorrelated (empty id) or result-only call is
// skipped, never orphaned. A tool.call with no assistant.message before it in
// its turn (a log written before the loop recorded one) becomes its own
// assistant message. An assistant message left with neither content nor calls
// (a turn whose only calls dangled) is dropped: it would send nothing.
// An assistant message a non-terminal output_truncated provider.error marker followed (the turn the
// output cap cut off, which the loop retried) is dropped wherever it sits, so a later rebuild never
// sends the truncated text back; replayFacts makes the compaction readers agree.
// session.steered events are not replayed because the live loop never injects
// them into its history. A history that would end on an assistant message
// without calls (a crash after its text, or a dangling call skipped) drops every
// such trailing message, however many: the live loop never sends one, which a
// provider treats as a prefill. The log
// is only read.
//
// Known differences from the live history, both older than this function: a
// denial is recorded without an id, so the rebuild drops the denied call and
// its result where the live history holds the call and a tool message
// "error: tool: tool not on session allowlist: ..."; and a failed call's tool
// message holds the recorded error JSON, not the live "error: tool: tool
// invocation failed: ..." text. A hook denial is not one of the differences: its tool message is
// hookDenialText, the form the live loop sends.
func rebuildHistory(events []store.RecordedEvent) []provider.Message {
	r := &historyRebuild{messages: []provider.Message{}, turn: -1, answered: answeredResultIDs(events)}
	for _, e := range events {
		r.accept(e)
	}
	kept := r.messages[:0:0]
	for _, m := range r.messages {
		if m.Role == "assistant" && m.Content == "" && len(m.ToolCalls) == 0 {
			continue
		}
		kept = append(kept, m)
	}
	for n := len(kept); n > 0 && kept[n-1].Role == "assistant" && len(kept[n-1].ToolCalls) == 0; n = len(kept) {
		kept = kept[:n-1] // the live loop never sends a history ending on an assistant message
	}
	return kept
}

func (r *historyRebuild) accept(e store.RecordedEvent) {
	switch e.Type {
	case session.TypeTurnStart:
		task := payloadFieldText(e.PayloadJSON, "task")
		if !r.hasTask {
			r.firstTask, r.hasTask = task, true
		}
		r.turn = -1
		r.messages = append(r.messages, provider.Message{Role: "user", Content: task})
	case TypeUserMessage:
		content := payloadFieldText(e.PayloadJSON, "content")
		if r.hasTask && content == r.firstTask {
			return // the text-turn path echoes the task it was started with, again after each pause
		}
		r.turn = -1
		r.messages = append(r.messages, provider.Message{Role: "user", Content: content})
	case TypeAssistantMessage:
		r.turn, r.bare = len(r.messages), false
		r.messages = append(r.messages, provider.Message{Role: "assistant", Content: payloadFieldText(e.PayloadJSON, "content")})
	case TypeProviderError:
		if retriedTruncation(e) {
			r.dropTruncated()
		}
	case tool.TypeToolCall:
		r.call(e)
	case tool.TypeToolResult:
		r.result(e)
	}
}

// retriedTruncation reports the non-terminal provider.error marker the loop writes when it retries a
// turn the output cap cut off: the assistant message that precedes it was not a good answer.
func retriedTruncation(e store.RecordedEvent) bool {
	p := payloadMap(e.PayloadJSON)
	terminal, ok := p["terminal"].(bool)
	return e.Type == TypeProviderError && p["reason"] == ReasonOutputTruncated && ok && !terminal
}

// dropTruncated removes the assistant message the retry marker just followed: the last message, with
// no calls, of its turn. A marker with no such message (a log of another shape) drops nothing.
func (r *historyRebuild) dropTruncated() {
	last := len(r.messages) - 1
	if r.turn < 0 || r.turn != last || r.bare || len(r.messages[last].ToolCalls) > 0 {
		return
	}
	r.messages, r.turn = r.messages[:last], -1
}

func (r *historyRebuild) call(e store.RecordedEvent) {
	id := payloadFieldText(e.PayloadJSON, "tool_call_id")
	if id == "" || !r.answered[id] {
		return // uncorrelated or dangling call: skip, never orphan
	}
	call := provider.ToolCall{
		ID:        id,
		Name:      payloadFieldText(e.PayloadJSON, "tool"),
		Arguments: json.RawMessage(payloadFieldRaw(e.PayloadJSON, "args")),
	}
	if r.turn < 0 {
		r.turn, r.bare = len(r.messages), true
		r.messages = append(r.messages, provider.Message{Role: "assistant"})
	}
	r.messages[r.turn].ToolCalls = append(r.messages[r.turn].ToolCalls, call)
}

func (r *historyRebuild) result(e store.RecordedEvent) {
	id := payloadFieldText(e.PayloadJSON, "tool_call_id")
	if id == "" {
		return // result-only denial: skip, never orphan
	}
	if r.bare {
		r.turn, r.bare = -1, false // a log without assistant.message: one message per call, as before
	}
	r.messages = append(r.messages, provider.Message{Role: "tool", ToolCallID: id, Content: resultText(e.PayloadJSON)})
}
