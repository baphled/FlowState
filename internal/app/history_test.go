package app

import (
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

	DescribeTable("rejects invalid independent journal rows", func(events []store.RecordedEvent) {
		_, err := replaySessionHistory(events, limits)
		Expect(err).To(HaveOccurred())
	}, Entry("orphan result", []store.RecordedEvent{historyResult()}), Entry("orphan start", []store.RecordedEvent{historyStart()}),
		Entry("missing content", []store.RecordedEvent{historyRow("user.message", `{}`)}), Entry("null message", []store.RecordedEvent{historyRow("assistant.message", `null`)}),
		Entry("duplicate announcement IDs", []store.RecordedEvent{historyRow("assistant.message", `{"content":"","tool_calls":[{"id":"c","name":"echo","arguments":{"n":9007199254740993}},{"id":"c","name":"echo","arguments":{"n":9007199254740993}}]}`), historyStart(), historyResult()}),
		Entry("finalised delta from a tool", []store.RecordedEvent{{Type: "assistant.delta", Actor: "tool:untrusted", PayloadJSON: `{"delta":"partial"}`}, historyRow("assistant.message", `{"content":"final"}`)}),
		Entry("finalised delta from an empty provider", []store.RecordedEvent{{Type: "assistant.delta", Actor: "provider:", PayloadJSON: `{"delta":"partial"}`}, historyRow("assistant.message", `{"content":"final"}`)}),
		Entry("unfinished stream", []store.RecordedEvent{historyRow("assistant.delta", `{"delta":"partial"}`)}),
		Entry("unknown event", []store.RecordedEvent{historyRow("future.message", `{}`)}))

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
	DescribeTable("preserves legacy text across current metadata rows", func(typ string) {
		messages, err := replaySessionHistory([]store.RecordedEvent{historyRow("user.message", `{"content":"request"}`), historyRow("assistant.message", `{"content":"answer"}`), historyRow(typ, `{}`)}, limits)
		Expect(err).NotTo(HaveOccurred())
		Expect(messages).To(Equal([]provider.Message{{Role: "user", Content: "request"}, {Role: "assistant", Content: "answer"}}))
	}, Entry("start", "turn.start"), Entry("end", "turn.end"), Entry("error", "provider.error"),
		Entry("pause", "session.paused"), Entry("resume", "session.resumed"), Entry("cancel", "session.cancelled"), Entry("checkpoint", "turn.checkpoint"))
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
