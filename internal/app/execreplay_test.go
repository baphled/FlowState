package app

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// logEvent builds a recorded event with a JSON payload.
func logEvent(typ string, payload map[string]any) store.RecordedEvent {
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return store.RecordedEvent{Type: typ, PayloadJSON: string(encoded)}
}

func taskEvent(task string) store.RecordedEvent {
	return logEvent(session.TypeTurnStart, map[string]any{"task": task})
}

func saidEvent(content string) store.RecordedEvent {
	return logEvent(TypeAssistantMessage, map[string]any{"content": content})
}

func callEvent(id string) store.RecordedEvent {
	return logEvent(tool.TypeToolCall, map[string]any{"tool_call_id": id, "tool": "echo", "args": map[string]any{"message": id}})
}

func resultEvent(id string) store.RecordedEvent {
	return logEvent(tool.TypeToolResult, map[string]any{"tool_call_id": id, "tool": "echo", "ok": true, "result": "out-" + id})
}

// wantCall is the provider call callEvent(id) rebuilds to.
func wantCall(id string) provider.ToolCall {
	return provider.ToolCall{ID: id, Name: "echo", Arguments: json.RawMessage(`{"message":"` + id + `"}`)}
}

func wantResult(id string) provider.Message {
	return provider.Message{Role: "tool", ToolCallID: id, Content: `"out-` + id + `"`}
}

func userMsg(content string) provider.Message {
	return provider.Message{Role: "user", Content: content}
}

func assistantMsg(content string, ids ...string) provider.Message {
	m := provider.Message{Role: "assistant", Content: content}
	for _, id := range ids {
		m.ToolCalls = append(m.ToolCalls, wantCall(id))
	}
	return m
}

// assistantTexts lists the content of every assistant message.
func assistantTexts(messages []provider.Message) []string {
	out := []string{}
	for _, m := range messages {
		if m.Role == "assistant" {
			out = append(out, m.Content)
		}
	}
	return out
}

var _ = Describe("rebuildHistory", func() {
	It("rebuilds a two-turn log to the shape the live loop sends", func() {
		Expect(rebuildHistory([]store.RecordedEvent{
			taskEvent("task"), saidEvent("I will read it"), callEvent("c1"), resultEvent("c1"), saidEvent("done"),
		})).To(Equal([]provider.Message{userMsg("task"), assistantMsg("I will read it", "c1"), wantResult("c1")}))
	})
	It("joins the three calls of one turn into one assistant message, results in log order", func() {
		Expect(rebuildHistory([]store.RecordedEvent{
			taskEvent("task"), saidEvent("three"), callEvent("a"), resultEvent("a"), callEvent("b"), resultEvent("b"),
			callEvent("c"), resultEvent("c"),
		})).To(Equal([]provider.Message{userMsg("task"), assistantMsg("three", "a", "b", "c"), wantResult("a"), wantResult("b"), wantResult("c")}))
	})
	It("keeps a tool-only turn as an assistant message with calls and empty content", func() {
		Expect(rebuildHistory([]store.RecordedEvent{taskEvent("task"), saidEvent(""), callEvent("c1"), resultEvent("c1")})).
			To(Equal([]provider.Message{userMsg("task"), assistantMsg("", "c1"), wantResult("c1")}))
	})
	It("skips a dangling call and keeps the answered calls of its turn", func() {
		Expect(rebuildHistory([]store.RecordedEvent{
			taskEvent("task"), saidEvent("two"), callEvent("a"), resultEvent("a"), callEvent("lost"),
		})).To(Equal([]provider.Message{userMsg("task"), assistantMsg("two", "a"), wantResult("a")}))
	})
	It("drops a turn whose only call dangled and which said nothing", func() {
		Expect(rebuildHistory([]store.RecordedEvent{taskEvent("task"), saidEvent(""), callEvent("lost")})).
			To(Equal([]provider.Message{userMsg("task")}))
	})
	It("skips a call without an id and its result-only denial", func() {
		Expect(rebuildHistory([]store.RecordedEvent{
			taskEvent("task"), saidEvent("denied"), logEvent(tool.TypeToolCall, map[string]any{"tool": "fs.write"}),
			logEvent(tool.TypeToolResult, map[string]any{"tool": "fs.write", "error": "denied"}),
		})).To(Equal([]provider.Message{userMsg("task")}))
	})
	It("places a user message where the log has it", func() {
		Expect(rebuildHistory([]store.RecordedEvent{
			taskEvent("task"), saidEvent("first"), logEvent(TypeUserMessage, map[string]any{"content": "and then?"}), saidEvent("second"),
		})).To(Equal([]provider.Message{userMsg("task"), assistantMsg("first"), userMsg("and then?")}))
	})
	It("does not repeat the task a text turn echoes as a user message", func() {
		Expect(rebuildHistory([]store.RecordedEvent{
			taskEvent("task"), logEvent(TypeUserMessage, map[string]any{"content": "task"}), saidEvent("answer"),
		})).To(Equal([]provider.Message{userMsg("task")}))
	})
	It("does not replay a steering message, which the live loop never shows the model", func() {
		Expect(rebuildHistory([]store.RecordedEvent{
			taskEvent("task"), logEvent("session.steered", map[string]any{"body": "hurry"}), saidEvent("answer"),
		})).To(Equal([]provider.Message{userMsg("task")}))
	})
	It("rebuilds a log with no assistant.message to one assistant message per call, as before", func() {
		Expect(rebuildHistory([]store.RecordedEvent{
			taskEvent("task"), callEvent("a"), resultEvent("a"), callEvent("b"), resultEvent("b"),
		})).To(Equal([]provider.Message{userMsg("task"), assistantMsg("", "a"), wantResult("a"), assistantMsg("", "b"), wantResult("b")}))
	})
	It("does not repeat the task after a text turn paused and resumed twice", func() {
		echo := logEvent(TypeUserMessage, map[string]any{"content": "task"})
		Expect(rebuildHistory([]store.RecordedEvent{
			taskEvent("task"), echo, saidEvent("part"), logEvent("session.paused", nil), logEvent("session.resumed", nil), echo,
			saidEvent("more"), callEvent("c1"), resultEvent("c1"),
		})).To(Equal([]provider.Message{userMsg("task"), assistantMsg("part"), assistantMsg("more", "c1"), wantResult("c1")}))
	})
	It("keeps a user message that differs from the first task", func() {
		Expect(rebuildHistory([]store.RecordedEvent{
			taskEvent("task"), saidEvent("a"), callEvent("c1"), resultEvent("c1"), logEvent(TypeUserMessage, map[string]any{"content": "next"}),
		})).To(Equal([]provider.Message{userMsg("task"), assistantMsg("a", "c1"), wantResult("c1"), userMsg("next")}))
	})
	It("drops an empty assistant message that is not last", func() {
		Expect(rebuildHistory([]store.RecordedEvent{taskEvent("task"), saidEvent(""), callEvent("lost"), taskEvent("next")})).
			To(Equal([]provider.Message{userMsg("task"), userMsg("next")}))
	})
	It("drops every trailing assistant message that has no calls", func() {
		Expect(rebuildHistory([]store.RecordedEvent{taskEvent("task"), saidEvent("p"), saidEvent("q")})).
			To(Equal([]provider.Message{userMsg("task")}))
	})
	It("drops a trailing assistant message that has no calls", func() {
		Expect(rebuildHistory([]store.RecordedEvent{taskEvent("task"), saidEvent("answer")})).To(Equal([]provider.Message{userMsg("task")}))
	})
	It("pins a denial as dropped, where the live history holds the denied call and its error message", func() {
		Expect(rebuildHistory([]store.RecordedEvent{
			taskEvent("task"), saidEvent("try"), logEvent(tool.TypeToolCall, map[string]any{"tool": "fs.write"}),
			logEvent(tool.TypeToolResult, map[string]any{"tool": "fs.write", "error": "tool not on session allowlist: fs.write"}),
			saidEvent("next"), callEvent("c1"), resultEvent("c1"),
		})).To(Equal([]provider.Message{userMsg("task"), assistantMsg("try"), assistantMsg("next", "c1"), wantResult("c1")}))
	})
	It("pins a tool error as the recorded error JSON, not the live 'error: tool: ...' text", func() {
		Expect(rebuildHistory([]store.RecordedEvent{
			taskEvent("task"), saidEvent("go"), callEvent("c1"),
			logEvent(tool.TypeToolResult, map[string]any{"tool_call_id": "c1", "tool": "echo", "error": map[string]any{"code": "failed"}}),
		})[2].Content).To(Equal(`{"code":"failed"}`))
	})
	It("pins a late crash answer as two assistant messages, a known provider-validity limit (ADR-009 D8)", func() {
		// d1 is answered after a later turn's e1: the tool result is not adjacent to its call, which a
		// provider that requires adjacency may reject; the log is not reordered.
		Expect(rebuildHistory([]store.RecordedEvent{
			taskEvent("task"), saidEvent("a"), callEvent("d1"), saidEvent("b"), callEvent("e1"), resultEvent("e1"), resultEvent("d1"),
		})).To(Equal([]provider.Message{
			userMsg("task"), assistantMsg("a", "d1"), assistantMsg("b", "e1"), wantResult("e1"), wantResult("d1"),
		}))
	})
	DescribeTable("never ends a rebuilt history on an assistant message without calls",
		func(events []store.RecordedEvent) {
			history := rebuildHistory(events)
			last := history[len(history)-1]
			Expect(last.Role == "assistant" && len(last.ToolCalls) == 0).To(BeFalse())
		},
		Entry("a crash after assistant.message", []store.RecordedEvent{taskEvent("t"), saidEvent("x")}),
		Entry("a crash after assistant.message and a dangling call",
			[]store.RecordedEvent{taskEvent("t"), saidEvent("x"), callEvent("lost")}),
		Entry("a crash after a tool call answered and a later text",
			[]store.RecordedEvent{taskEvent("t"), saidEvent("x"), callEvent("c1"), resultEvent("c1"), saidEvent("y")}),
		Entry("a crash after a result, which ends in a tool message",
			[]store.RecordedEvent{taskEvent("t"), saidEvent("x"), callEvent("c1"), resultEvent("c1")}),
	)
	It("rebuilds an empty log to an empty history", func() {
		Expect(rebuildHistory(nil)).To(Equal([]provider.Message{}))
	})
})

var _ = Describe("The history a loop sends and the history rebuilt from its log", func() {
	var ctx context.Context
	BeforeEach(func() { ctx = context.Background() })

	It("agree at the start of the third turn", func() {
		streamer := &execLoopStreamer{turns: [][]provider.StreamEvent{
			{provText("one"), provCall(echoCall("c1")), provCall(echoCall("c2")), provFinish()},
			{provText("two"), provCall(echoCall("c3")), provFinish()},
			{provText("three")},
		}}
		cfg := execLoopFixture(streamer)
		runExec(ctx, cfg)
		events, err := cfg.Store.Replay(ctx, cfg.Session.ID)
		if err != nil || len(streamer.reqs) != 3 {
			panic("the three-turn run must record its log and three requests")
		}
		messages, cut := 0, len(events)
		for i, e := range events {
			if messages == 2 && e.Type == TypeAssistantDelta {
				cut = i
				break
			}
			if e.Type == TypeAssistantMessage {
				messages++
			}
		}
		Expect(rebuildHistory(events[:cut])).To(Equal(streamer.reqs[2].Messages))
	})

	It("sends a run resumed after a budget stop the assistant text it said before", func() {
		first := &execLoopStreamer{turns: [][]provider.StreamEvent{
			{provText("thought one"), provCall(echoCall("c1")), provFinish()},
			{provText("thought two"), provCall(echoCall("c2")), provFinish()},
			{provText("never")},
		}}
		cfg := execLoopFixture(first)
		cfg.Pool, cfg.TurnBudget = NewBudgetPool(2), 1
		if out := runExec(ctx, cfg); !out.ErrBudget {
			panic("the first run must stop at its budget")
		}
		resume := &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("resumed")}}}
		cfg.Streamer, cfg.Pool = resume, NewBudgetPool(1)
		runExec(ctx, cfg)
		Expect(assistantTexts(resume.reqs[0].Messages)).To(Equal([]string{"thought one", "thought two"}))
	})
})
