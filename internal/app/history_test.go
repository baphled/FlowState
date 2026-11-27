package app

import (
	"cmp"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

func historyRow(typ, payload string) store.RecordedEvent {
	actor := "engine"
	if typ == "user.message" {
		actor = "user"
	} else if strings.HasPrefix(typ, "tool.") {
		actor = "tool:echo"
	}
	return store.RecordedEvent{Type: typ, Actor: actor, PayloadJSON: payload}
}

func historyBatch() []store.RecordedEvent {
	return []store.RecordedEvent{
		historyRow("user.message", `{"content":"request"}`),
		historyRow("assistant.message", `{"content":"working","tool_calls":[{"id":"c","name":"echo","arguments":{"n":9007199254740993}}]}`),
	}
}

func historyStart() store.RecordedEvent {
	return historyRow("tool.call", `{"tool":"echo","tool_call_id":"c","args":{ "n":9007199254740993 }}`)
}

func historyResult() store.RecordedEvent {
	return historyRow("tool.result", `{"tool":"echo","tool_call_id":"c","ok":true,"result":null}`)
}

var _ = Describe("Bounded session history", func() {
	var limits HistoryLimits
	BeforeEach(func() {
		limits = HistoryLimits{MaxEvents: 100, MaxMessages: 100, MaxBytes: 100000, MaxResultBytes: 10000}
	})

	It("preserves an independently specified two-round conversation in durable result order", func() {
		events := []store.RecordedEvent{
			historyRow("turn.start", `{"task":"request"}`), historyRow("user.message", `{"content":"request"}`),
			historyRow("assistant.delta", `{"delta":"checking"}`),
			historyRow("assistant.message", `{"content":"checking","tool_calls":[{"id":"a","name":"echo","arguments":{}},{"id":"b","name":"echo","arguments":{"n":9007199254740993}}]}`),
			historyRow("tool.call", `{"tool":"echo","tool_call_id":"b","args":{ "n":9007199254740993 }}`),
			historyRow("tool.result", `{"tool":"echo","tool_call_id":"b","ok":true,"result":9007199254740993}`),
			historyRow("tool.result", `{"tool":"echo","tool_call_id":"a","ok":false,"error":{"code":"tool_denied","message":"denied"}}`),
			historyRow("user.message", `{"content":"again"}`), historyRow("assistant.message", `{"content":"working","tool_calls":[{"id":"c","name":"echo","arguments":{"n":9007199254740993}}]}`),
			historyStart(), historyResult(), historyRow("assistant.message", `{"content":"done"}`), historyRow("turn.end", `{}`),
		}
		messages, err := replaySessionHistory(events, limits)
		Expect(err).NotTo(HaveOccurred())
		Expect(messages).To(Equal([]provider.Message{
			{Role: "user", Content: "request"},
			{Role: "assistant", Content: "checking", ToolCalls: []provider.ToolCall{{ID: "a", Name: "echo", Arguments: json.RawMessage(`{}`)}, {ID: "b", Name: "echo", Arguments: json.RawMessage(`{"n":9007199254740993}`)}}},
			{Role: "tool", ToolCallID: "b", Content: `{"tool":"echo","tool_call_id":"b","ok":true,"result":9007199254740993}`},
			{Role: "tool", ToolCallID: "a", Content: `{"tool":"echo","tool_call_id":"a","ok":false,"error":{"code":"tool_denied","message":"denied"}}`},
			{Role: "user", Content: "again"}, {Role: "assistant", Content: "working", ToolCalls: []provider.ToolCall{{ID: "c", Name: "echo", Arguments: json.RawMessage(`{"n":9007199254740993}`)}}},
			{Role: "tool", ToolCallID: "c", Content: `{"tool":"echo","tool_call_id":"c","ok":true,"result":null}`}, {Role: "assistant", Content: "done"},
		}))
	})

	DescribeTable("rejects ambiguous or malformed correlated histories", func(tail []store.RecordedEvent) {
		_, err := replaySessionHistory(append(historyBatch(), tail...), limits)
		Expect(err).To(HaveOccurred())
	},
		Entry("pending announcement", []store.RecordedEvent{}), Entry("pending execution", []store.RecordedEvent{historyStart()}),
		Entry("success without start", []store.RecordedEvent{historyResult()}), Entry("duplicate start", []store.RecordedEvent{historyStart(), historyStart(), historyResult()}),
		Entry("duplicate result", []store.RecordedEvent{historyStart(), historyResult(), historyResult()}),
		Entry("ID reused after completion", []store.RecordedEvent{historyStart(), historyResult(), historyBatch()[1], historyStart(), historyResult()}),
		Entry("new user", []store.RecordedEvent{historyRow("user.message", `{"content":"next"}`), historyStart(), historyResult()}),
		Entry("new assistant", []store.RecordedEvent{historyRow("assistant.message", `{"content":"next"}`), historyStart(), historyResult()}),
		Entry("manual event during pending batch", []store.RecordedEvent{historyRow("tool.call", `{"tool":"echo","args":{}}`), historyStart(), historyResult()}),
		Entry("null ID", []store.RecordedEvent{historyRow("tool.result", `{"tool_call_id":null}`), historyStart(), historyResult()}),
		Entry("empty ID", []store.RecordedEvent{historyRow("tool.call", `{"tool_call_id":""}`), historyStart(), historyResult()}),
		Entry("wrong ID", []store.RecordedEvent{historyRow("tool.call", `{"tool":"echo","tool_call_id":"other","args":{}}`), historyStart(), historyResult()}),
		Entry("wrong name", []store.RecordedEvent{historyRow("tool.call", `{"tool":"fs.read","tool_call_id":"c","args":{"n":9007199254740993}}`), historyResult()}),
		Entry("wrong actor", []store.RecordedEvent{{Type: "tool.call", Actor: "engine", PayloadJSON: historyStart().PayloadJSON}, historyResult()}),
		Entry("changed numeric arguments", []store.RecordedEvent{historyRow("tool.call", `{"tool":"echo","tool_call_id":"c","args":{"n":9007199254740992}}`), historyResult()}),
		Entry("wrong result tool", []store.RecordedEvent{historyStart(), historyRow("tool.result", `{"tool":"fs.read","tool_call_id":"c","ok":true,"result":null}`)}),
		Entry("wrong result actor", []store.RecordedEvent{historyStart(), {Type: "tool.result", Actor: "user", PayloadJSON: historyResult().PayloadJSON}}),
		Entry("ordinary error without start", []store.RecordedEvent{historyRow("tool.result", `{"tool":"echo","tool_call_id":"c","ok":false,"error":{"code":"tool_error","message":"failed"}}`)}),
		Entry("malformed success", []store.RecordedEvent{historyStart(), historyRow("tool.result", `{"tool":"echo","tool_call_id":"c","ok":true}`)}),
		Entry("contradictory result", []store.RecordedEvent{historyStart(), historyRow("tool.result", `{"tool":"echo","tool_call_id":"c","ok":true,"result":null,"error":{}}`)}),
		Entry("terminal does not resolve calls", []store.RecordedEvent{historyRow("session.cancelled", `{}`)}))

	It("accepts a started call whose arguments differ from the announcement only in key order and escaping", func() {
		events := []store.RecordedEvent{
			historyRow("user.message", `{"content":"request"}`),
			historyRow("assistant.message", `{"content":"w","tool_calls":[{"id":"c","name":"echo","arguments":{"b":"a<b","a":1}}]}`),
			historyRow("tool.call", `{"tool":"echo","tool_call_id":"c","args":{"a":1,"b":"a\u003cb"}}`),
			historyResult(),
		}
		messages, err := replaySessionHistory(events, limits)
		if err != nil {
			panic(err)
		}
		Expect(messages).To(HaveLen(3))
	})

	DescribeTable("rejects invalid independent journal rows", func(events []store.RecordedEvent) {
		_, err := replaySessionHistory(events, limits)
		Expect(err).To(HaveOccurred())
	}, Entry("orphan result", []store.RecordedEvent{historyResult()}), Entry("orphan start", []store.RecordedEvent{historyStart()}),
		Entry("missing content", []store.RecordedEvent{historyRow("user.message", `{}`)}), Entry("null message", []store.RecordedEvent{historyRow("assistant.message", `null`)}),
		Entry("duplicate announcement IDs", []store.RecordedEvent{historyRow("assistant.message", `{"content":"","tool_calls":[{"id":"c","name":"echo","arguments":{"n":9007199254740993}},{"id":"c","name":"echo","arguments":{"n":9007199254740993}}]}`), historyStart(), historyResult()}),
		Entry("finalised delta from a tool", []store.RecordedEvent{{Type: "assistant.delta", Actor: "tool:untrusted", PayloadJSON: `{"delta":"partial"}`}, historyRow("assistant.message", `{"content":"final"}`)}),
		Entry("finalised delta from an empty provider", []store.RecordedEvent{{Type: "assistant.delta", Actor: "provider:", PayloadJSON: `{"delta":"partial"}`}, historyRow("assistant.message", `{"content":"final"}`)}),
		Entry("unfinished stream", []store.RecordedEvent{historyRow("assistant.delta", `{"delta":"partial"}`)}),
		Entry("unknown event", []store.RecordedEvent{historyRow("future.message", `{}`)}),
		Entry("hook context", []store.RecordedEvent{historyRow("hook.context", `{"content":"x"}`)}),
		Entry("unknown hook type", []store.RecordedEvent{historyRow("hook.future", `{"content":"x"}`)}))

	It("rejects session.unlocked with ErrInvalidHistory (ADR-011 pins today's behaviour)", func() {
		_, err := replaySessionHistory([]store.RecordedEvent{historyRow(session.TypeSessionUnlocked, `{"forced":true}`)}, limits)
		Expect(err).To(MatchError(ErrInvalidHistory))
	})

	It("returns no partial history on rejection", func() {
		messages, _ := replaySessionHistory(append(historyBatch(), historyResult()), limits)
		Expect(messages).To(BeNil())
	})
	It("requires resolved calls even when the provider validator accepts pending calls", func() {
		err := (provider.ChatRequest{Messages: []provider.Message{{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "c", Name: "echo", Arguments: json.RawMessage(`{}`)}}}}}).Validate()
		Expect(err).NotTo(HaveOccurred())
		_, err = replaySessionHistory(historyBatch(), limits)
		Expect(err).To(MatchError(ErrInvalidHistory))
	})
	It("ignores bootstrap and uncorrelated manual events without synthesising messages", func() {
		messages, err := replaySessionHistory([]store.RecordedEvent{historyRow("turn.start", `{"task":"request"}`), historyRow("tool.call", `{"tool":"echo","args":{}}`), historyRow("tool.result", `{"tool":"echo","ok":true,"result":null}`)}, limits)
		Expect(err).NotTo(HaveOccurred())
		Expect(messages).To(BeEmpty())
	})
	It("serializes an empty conversation within its exact two-byte cap", func() {
		limits.MaxBytes = 2
		messages, err := replaySessionHistory(nil, limits)
		Expect(err).NotTo(HaveOccurred())
		encoded, err := json.Marshal(messages)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(Equal("[]"))
	})
	It("keeps the conversation across a non-terminal overflow provider.error", func() {
		marker := historyRow("provider.error", `{"error":"x","reason":"context_overflow","terminal":false}`)
		messages, err := replaySessionHistory([]store.RecordedEvent{historyRow("user.message", `{"content":"request"}`), marker, historyRow("assistant.message", `{"content":"answer"}`)}, limits)
		Expect(err).NotTo(HaveOccurred())
		Expect(messages).To(Equal([]provider.Message{{Role: "user", Content: "request"}, {Role: "assistant", Content: "answer"}}))
	})
	DescribeTable("preserves legacy text across current metadata rows", func(typ string) {
		messages, err := replaySessionHistory([]store.RecordedEvent{historyRow("user.message", `{"content":"request"}`), historyRow("assistant.message", `{"content":"answer"}`), historyRow(typ, `{}`)}, limits)
		Expect(err).NotTo(HaveOccurred())
		Expect(messages).To(Equal([]provider.Message{{Role: "user", Content: "request"}, {Role: "assistant", Content: "answer"}}))
	}, Entry("start", "turn.start"), Entry("end", "turn.end"), Entry("error", "provider.error"),
		Entry("pause", "session.paused"), Entry("resume", "session.resumed"), Entry("cancel", "session.cancelled"), Entry("checkpoint", "turn.checkpoint"),
		Entry("hooks loaded", "hooks.loaded"), Entry("hook invoked", "hook.invoked"), Entry("hook result", "hook.result"))

	const hash = "abababababababababababababababababababababababababababababababab" // the sha256 of the file every skillCall frame and skillLoaded record carry
	// skillCall is an announced, started and answered skill.load call of the skill name: its tool.result is the payload tail given, or a success over its frame.
	skillCall := func(name, tail string) []store.RecordedEvent {
		actor := store.RecordedEvent{Type: "tool.call", Actor: "tool:skill.load", PayloadJSON: `{"tool":"skill.load","tool_call_id":"k","args":{"name":"` + name + `"}}`}
		frame := `<<<skill name="` + name + `" sha256="` + hash + `" file="">>>` + "\n"
		result := store.RecordedEvent{Type: "tool.result", Actor: "tool:skill.load", PayloadJSON: `{"tool":"skill.load","tool_call_id":"k",` +
			cmp.Or(tail, `"ok":true,"result":`+string(must(json.Marshal(frame)))) + `}`}
		return []store.RecordedEvent{historyRow("assistant.message", `{"content":"working","tool_calls":[{"id":"k","name":"skill.load","arguments":{"name":"`+name+`"}}]}`), actor, result}
	}
	// skillLoaded is the skill.loaded record of the skill name for the call id, under hash, or the payload given.
	skillLoaded := func(name, id, extra string) store.RecordedEvent {
		payload := `{"name":"` + name + `","file":"","sha256":"` + hash + `","path":"` + name + `/SKILL.md","bytes":10,"tool_call_id":"` + id + `"}`
		if extra != "" {
			payload = extra
		}
		return store.RecordedEvent{Type: "skill.loaded", Actor: "engine", PayloadJSON: payload}
	}

	It("accepts skill.loaded after its completed skill.load call and adds no message", func() {
		base := append(historyBatch()[:1], skillCall("tdd", "")...)
		with := append(base, skillLoaded("tdd", "k", "")) // written past base's end, which stays intact
		Expect(must(replaySessionHistory(with, limits))).To(Equal(must(replaySessionHistory(base, limits))))
	})

	It("accepts skill.loaded after the tool.after hooks of its call", func() {
		events := append(append(historyBatch()[:1], skillCall("tdd", "")...), historyRow("hook.invoked", `{"id":"after"}`), historyRow("hook.result", `{"id":"after"}`), skillLoaded("tdd", "k", ""))
		Expect(must(replaySessionHistory(events, limits))).To(HaveLen(3))
	})

	DescribeTable("accepts skill.loaded for a skill named after a Windows device word (such a directory loads on Unix)",
		func(name string) {
			events := append(append(historyBatch()[:1], skillCall(name, "")...), skillLoaded(name, "k", ""))
			Expect(must(replaySessionHistory(events, limits))).To(HaveLen(3))
		},
		Entry("nul", "nul"), Entry("aux", "aux"), Entry("con", "con"), Entry("prn", "prn"), Entry("com1", "com1"), Entry("lpt9", "lpt9"))

	DescribeTable("rejects a skill.loaded that is not the record of its completed skill.load call", func(events []store.RecordedEvent) {
		_, err := replaySessionHistory(events, limits)
		Expect(err).To(MatchError(ErrInvalidHistory))
	},
		Entry("stray, with no call", []store.RecordedEvent{skillLoaded("tdd", "k", "")}),
		Entry("after another tool's completed call", []store.RecordedEvent{historyBatch()[0], historyBatch()[1], historyStart(), historyResult(), skillLoaded("tdd", "c", "")}),
		Entry("after a failed load", append(append(historyBatch()[:1], skillCall("tdd", `"ok":false,"error":{"code":"skill_refused","message":"no"}`)...), skillLoaded("tdd", "k", ""))),
		Entry("before the result", []store.RecordedEvent{historyBatch()[0], historyBatch()[1], skillCall("tdd", "")[1], skillLoaded("tdd", "k", "")}),
		Entry("for another call id", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "other", ""))),
		Entry("from an actor other than the engine", func() []store.RecordedEvent {
			events := append(historyBatch()[:1], skillCall("tdd", "")...)
			last := skillLoaded("tdd", "k", "")
			last.Actor = "user"
			return append(events, last)
		}()),
		Entry("twice for one call", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", ""), skillLoaded("tdd", "k", ""))),
		Entry("after the conversation moved on", append(append(historyBatch()[:1], skillCall("tdd", "")...), historyRow("assistant.message", `{"content":"done"}`), skillLoaded("tdd", "k", ""))),
		Entry("with no name", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"file":"","sha256":"`+hash+`","path":"tdd/SKILL.md","bytes":10,"tool_call_id":"k"}`))),
		Entry("with no hash", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"name":"tdd","file":"","path":"tdd/SKILL.md","bytes":10,"tool_call_id":"k"}`))),
		Entry("with no path", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"name":"tdd","file":"","sha256":"`+hash+`","bytes":10,"tool_call_id":"k"}`))),
		Entry("with zero bytes", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"name":"tdd","file":"","sha256":"`+hash+`","path":"tdd/SKILL.md","bytes":0,"tool_call_id":"k"}`))),
		Entry("with a hash that is not hex", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"name":"tdd","file":"","sha256":"`+strings.Repeat("zz", 32)+`","path":"tdd/SKILL.md","bytes":10,"tool_call_id":"k"}`))),
		Entry("with an uppercase hash", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"name":"tdd","file":"","sha256":"`+strings.ToUpper(hash)+`","path":"tdd/SKILL.md","bytes":10,"tool_call_id":"k"}`))),
		Entry("with more bytes than a skill file may hold", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"name":"tdd","file":"","sha256":"`+hash+`","path":"tdd/SKILL.md","bytes":99999999,"tool_call_id":"k"}`))),
		Entry("with an unknown field", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"name":"tdd","file":"","sha256":"`+hash+`","path":"tdd/SKILL.md","bytes":10,"tool_call_id":"k","root":"/home/u"}`))),
		Entry("with a path outside the skills root", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"name":"..","file":"","sha256":"`+hash+`","path":"../../etc/passwd","bytes":10,"tool_call_id":"k"}`))),
		Entry("with a path under another name than the skill's", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"name":"tdd","file":"","sha256":"`+hash+`","path":"other/SKILL.md","bytes":10,"tool_call_id":"k"}`))),
		Entry("with a name outside the loader's rule", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"name":"Tdd","file":"","sha256":"`+hash+`","path":"Tdd/SKILL.md","bytes":10,"tool_call_id":"k"}`))),
		Entry("whose path names another file than its file field", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"name":"tdd","file":"reference/guide.md","sha256":"`+hash+`","path":"tdd/SKILL.md","bytes":10,"tool_call_id":"k"}`))),
		Entry("whose path names a file though none is recorded", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"name":"tdd","file":"","sha256":"`+hash+`","path":"tdd/reference/guide.md","bytes":10,"tool_call_id":"k"}`))),
		Entry("with no call id", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"name":"tdd","file":"","sha256":"`+hash+`","path":"tdd/SKILL.md","bytes":10}`))),
		Entry("whose frame carries another hash than the record", append(append(historyBatch()[:1], skillCall("tdd", `"ok":true,"result":`+string(must(json.Marshal(`<<<skill name="tdd" sha256="`+strings.Repeat("cd", 32)+`" file="">>>`+"\n"))))...), skillLoaded("tdd", "k", ""))),
		Entry("whose frame names another skill than the record", append(append(historyBatch()[:1], skillCall("other", "")...), skillLoaded("tdd", "k", ""))),
		Entry("whose frame names another file than the record", append(append(historyBatch()[:1], skillCall("tdd", "")...), skillLoaded("tdd", "k", `{"name":"tdd","file":"reference/guide.md","sha256":"`+hash+`","path":"tdd/reference/guide.md","bytes":10,"tool_call_id":"k"}`))),
		Entry("after a result that is not a frame", append(append(historyBatch()[:1], skillCall("tdd", `"ok":true,"result":"hello"`)...), skillLoaded("tdd", "k", ""))))
	It("projects a hook denial as the shared error text, as the rebuild does", func() {
		events := []store.RecordedEvent{
			historyRow("user.message", `{"content":"request"}`),
			historyRow("assistant.message", `{"content":"working","tool_calls":[{"id":"c","name":"echo","arguments":{}}]}`),
			historyRow("tool.call", `{"tool":"echo","tool_call_id":"c","args":{}}`),
			historyRow("tool.result", `{"tool":"echo","tool_call_id":"c","ok":false,"error":{"code":"hook_denied","message":"denied by hook gate-1"}}`),
		}
		messages, err := replaySessionHistory(events, limits)
		if err != nil {
			panic(err)
		}
		Expect(messages[2]).To(Equal(rebuildHistory(events)[2]))
	})
	It("projects a hook denial as the fixed form", func() {
		messages, err := replaySessionHistory([]store.RecordedEvent{
			historyRow("assistant.message", `{"content":"working","tool_calls":[{"id":"c","name":"echo","arguments":{}}]}`),
			historyRow("tool.call", `{"tool":"echo","tool_call_id":"c","args":{}}`),
			historyRow("tool.result", `{"tool":"echo","tool_call_id":"c","ok":false,"error":{"code":"hook_denied","message":"denied by hook gate-1"}}`),
		}, limits)
		if err != nil {
			panic(err)
		}
		Expect(messages[1].Content).To(Equal("error: denied by hook gate-1"))
	})
	It("holds a hook denial to MaxResultBytes like any result", func() {
		limits.MaxResultBytes = 10
		_, err := replaySessionHistory([]store.RecordedEvent{
			historyRow("assistant.message", `{"content":"working","tool_calls":[{"id":"c","name":"echo","arguments":{}}]}`),
			historyRow("tool.call", `{"tool":"echo","tool_call_id":"c","args":{}}`),
			historyRow("tool.result", `{"tool":"echo","tool_call_id":"c","ok":false,"error":{"code":"hook_denied","message":"denied by hook gate-1"}}`),
		}, limits)
		Expect(err).To(MatchError(ErrHistoryLimit))
	})
	It("passes a hook denial that fits MaxResultBytes exactly", func() {
		limits.MaxResultBytes = len("error: denied by hook gate-1")
		messages, err := replaySessionHistory([]store.RecordedEvent{
			historyRow("assistant.message", `{"content":"working","tool_calls":[{"id":"c","name":"echo","arguments":{}}]}`),
			historyRow("tool.call", `{"tool":"echo","tool_call_id":"c","args":{}}`),
			historyRow("tool.result", `{"tool":"echo","tool_call_id":"c","ok":false,"error":{"code":"hook_denied","message":"denied by hook gate-1"}}`),
		}, limits)
		Expect(err).NotTo(HaveOccurred())
		Expect(messages[1].Content).To(Equal("error: denied by hook gate-1"))
	})
	DescribeTable("enforces positive hard limits", func(capacity HistoryLimits) {
		_, err := replaySessionHistory(historyBatch(), capacity)
		Expect(err).To(MatchError(ErrHistoryLimit))
	}, Entry("events", HistoryLimits{0, 10, 1000, 100}), Entry("messages", HistoryLimits{10, 0, 1000, 100}),
		Entry("bytes", HistoryLimits{10, 10, -1, 100}), Entry("results", HistoryLimits{10, 10, 1000, 0}))
	DescribeTable("counts exact serialized history limits", func(over bool) {
		messages := []provider.Message{{Role: "user", Content: "\"世界\n"}}
		encoded, err := json.Marshal(messages)
		Expect(err).NotTo(HaveOccurred())
		limits.MaxBytes = len(encoded)
		limits.MaxEvents, limits.MaxMessages = 1, 1
		if over {
			limits.MaxBytes--
		}
		_, err = replaySessionHistory([]store.RecordedEvent{historyRow("user.message", `{"content":"\"世界\n"}`)}, limits)
		Expect(err != nil).To(Equal(over))
	}, Entry("at cap", false), Entry("one byte over", true))
	DescribeTable("does not silently prune capped events or messages", func(eventCap bool) {
		limits.MaxMessages = 1
		if eventCap {
			limits.MaxEvents = 1
		}
		_, err := replaySessionHistory([]store.RecordedEvent{historyRow("user.message", `{"content":"a"}`), historyRow("assistant.message", `{"content":"b"}`)}, limits)
		Expect(err).To(MatchError(ErrHistoryLimit))
	}, Entry("events", true), Entry("messages", false))
	It("preserves UTF8 characters in a bounded result preview", func() {
		content, err := toolResultContent(json.RawMessage(`{"世界":"`+strings.Repeat("世界", 100)+`"}`), 80)
		Expect(err).NotTo(HaveOccurred())
		var envelope historyResultPreview
		err = json.Unmarshal([]byte(content), &envelope)
		Expect(err).NotTo(HaveOccurred())
		Expect(envelope.Preview).To(ContainSubstring("世界"))
	})
	DescribeTable("rejects invalid result bytes", func(raw string) {
		_, err := toolResultContent(json.RawMessage(raw), 100)
		Expect(err).To(MatchError(ErrInvalidHistory))
	}, Entry("JSON", `{"broken":`), Entry("UTF8", "{\"value\":\"\xff\"}"))

	Context("result projection and persisted loading", func() {
		var st *store.Store
		var sessionID string
		var before []store.RecordedEvent
		BeforeEach(func() {
			var err error
			st, err = store.Open(filepath.Join(GinkgoT().TempDir(), "events.db"))
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(st.Close)
			sess, err := session.Create(context.Background(), st, "fake", "fake", "native", "sha", "request")
			Expect(err).NotTo(HaveOccurred())
			sessionID = sess.ID
			writer, err := st.NewEventWriter(context.Background(), sessionID)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(writer.Close)
			rows := append(historyBatch(), historyStart(), historyRow("tool.result", `{"tool":"echo","tool_call_id":"c","ok":false,"error":{"code":"tool_error","message":"`+strings.Repeat(`世界\"\\\n`, 100)+`"}}`))
			for _, row := range rows {
				_, err = writer.Append(context.Background(), row.Actor, row.Type, json.RawMessage(row.PayloadJSON))
				Expect(err).NotTo(HaveOccurred())
			}
			before, err = st.Replay(context.Background(), sessionID)
			Expect(err).NotTo(HaveOccurred())
		})
		It("loads fitting durable result bytes exactly", func() {
			messages, err := LoadSessionHistory(context.Background(), st, sessionID, limits)
			Expect(err).NotTo(HaveOccurred())
			Expect(messages[2].Content).To(Equal(before[len(before)-1].PayloadJSON))
		})
		DescribeTable("projects bounded result content", func(valid func(string) bool) {
			limits.MaxResultBytes = 100
			messages, err := LoadSessionHistory(context.Background(), st, sessionID, limits)
			Expect(err).NotTo(HaveOccurred())
			Expect(valid(messages[2].Content)).To(BeTrue())
		}, Entry("valid JSON", func(content string) bool { return json.Valid([]byte(content)) }),
			Entry("valid UTF8", utf8.ValidString), Entry("actual byte cap", func(content string) bool { return len(content) <= 100 }))
		It("identifies truncation and the original durable byte count", func() {
			limits.MaxResultBytes = 100
			messages, err := LoadSessionHistory(context.Background(), st, sessionID, limits)
			Expect(err).NotTo(HaveOccurred())
			var envelope struct {
				Truncated     bool `json:"truncated"`
				OriginalBytes int  `json:"original_bytes"`
			}
			err = json.Unmarshal([]byte(messages[2].Content), &envelope)
			Expect(err).NotTo(HaveOccurred())
			Expect(envelope).To(Equal(struct {
				Truncated     bool `json:"truncated"`
				OriginalBytes int  `json:"original_bytes"`
			}{true, len(before[len(before)-1].PayloadJSON)}))
		})
		It("leaves full journal rows and hashes unchanged", func() {
			limits.MaxResultBytes = 100
			_, err := LoadSessionHistory(context.Background(), st, sessionID, limits)
			Expect(err).NotTo(HaveOccurred())
			after, err := st.Replay(context.Background(), sessionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(after).To(Equal(before))
		})
		It("rejects an envelope budget too small for metadata", func() {
			limits.MaxResultBytes = 1
			_, err := LoadSessionHistory(context.Background(), st, sessionID, limits)
			Expect(err).To(MatchError(ErrHistoryLimit))
		})
		It("propagates existing replay failures", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := LoadSessionHistory(ctx, st, sessionID, limits)
			Expect(err).To(MatchError(context.Canceled))
		})
	})
})
