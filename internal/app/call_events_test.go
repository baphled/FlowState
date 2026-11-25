package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// callEventFixture opens a temp SQLite store with one session and a
// live event writer shared by the loop and the per-call adapter.
// Arrange-only helper: fixture failures panic so each spec keeps one
// behavioural expectation.
func callEventFixture() (*store.Store, string, *store.EventWriter) {
	st, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = st.Close() })
	sess, err := st.CreateSession(context.Background(), "call-events-1", "prov", "model", "harness", "sha")
	if err != nil {
		panic(err)
	}
	w, err := st.NewEventWriter(context.Background(), sess.ID)
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = w.Close() })
	return st, sess.ID, w
}

// callEventEchoLoop wires a real tool.Loop with the echo tool and one
// per-call adapter bound to call, sharing the session's writer.
func callEventEchoLoop(w *store.EventWriter, call provider.ToolCall) (tool.EventSink, *tool.Loop) {
	sink, err := NewCallEventSink(w, call)
	if err != nil {
		panic(err)
	}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewEcho()); err != nil {
		panic(err)
	}
	l, err := tool.NewLoop(registry, sink, []string{call.Name})
	if err != nil {
		panic(err)
	}
	return sink, l
}

// callEventJournal seeds the verified provider-side assistant call so
// the projected history recognises the correlated events.
func callEventJournal(ctx context.Context, w *store.EventWriter, call provider.ToolCall) {
	args := string(call.Arguments)
	_, err := w.Append(ctx, "user", TypeUserMessage, map[string]any{"content": "request"})
	if err != nil {
		panic(err)
	}
	_, err = w.Append(ctx, "engine", TypeAssistantMessage, map[string]any{
		"content":    "working",
		"tool_calls": []any{map[string]any{"id": call.ID, "name": call.Name, "arguments": json.RawMessage(args)}},
	})
	if err != nil {
		panic(err)
	}
}

// callEventEchoRegistry builds a registry with the echo tool.
func callEventEchoRegistry() *tool.Registry {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewEcho()); err != nil {
		panic(err)
	}
	return registry
}

// callEventLoop wires a real loop over sink allowing the echo tool.
func callEventLoop(sink tool.EventSink) (*tool.Loop, error) {
	return tool.NewLoop(callEventEchoRegistry(), sink, []string{"echo"})
}

// callEventRejection bundles a fail-closed rejection outcome.
type callEventRejection struct {
	Err       bool
	Persisted []store.RecordedEvent
}

// callEventBlocked bundles the blocked-effect outcome.
type callEventBlocked struct {
	Invoke  bool
	Results []string
	Sticky  bool
}

// callEventUnacknowledged bundles the sticky no-result outcome.
type callEventUnacknowledged struct {
	Invoked         bool
	Sticky          bool
	HistoryRejected bool
}

// callEventDenial bundles the denied feedback outcome.
type callEventDenial struct {
	Denied  bool
	Types   []string
	Payload string
}

// failingSink wraps a writer and fails appends whose payload contains
// the given marker key/value combination.
type failingSink struct {
	inner    tool.EventSink
	marker   string
	failures map[string]error
	appends  int
}

func (f *failingSink) Append(ctx context.Context, actor, typ string, payload any) (int64, error) {
	f.appends++
	if err := f.failures[typ]; err != nil {
		return -1, err
	}
	return f.inner.Append(ctx, actor, typ, payload)
}

var _ = Describe("Per-call correlated event sink", func() {
	var (
		ctx  context.Context
		call provider.ToolCall
	)

	// callEventContinuation bundles the correlated outcome into one
	// typed structural assertion so each spec keeps one behavioural
	// expectation.
	type callEventContinuation struct {
		Result        any
		ArgsUntouched bool
		ToolMessages  []provider.Message
	}

	BeforeEach(func() {
		ctx = context.Background()
		call = provider.ToolCall{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{ "n": 2 }`)}
	})

	It("correlates an acknowledged echo call and result through the real loop and replays a runnable continuation", func() {
		st, sid, w := callEventFixture()
		callEventJournal(ctx, w, call)
		before := string(call.Arguments)
		_, l := callEventEchoLoop(w, call)
		result, invokeErr := l.Invoke(ctx, call.Name, json.RawMessage(`{ "n": 2 }`))
		Expect(invokeErr).NotTo(HaveOccurred())
		messages, loadErr := LoadSessionHistory(ctx, st, sid, HistoryLimits{MaxEvents: 100, MaxMessages: 100, MaxBytes: 100000, MaxResultBytes: 10000})
		Expect(loadErr).NotTo(HaveOccurred())
		Expect(callEventContinuation{Result: result, ArgsUntouched: string(call.Arguments) == before, ToolMessages: messages[2:]}).To(Equal(callEventContinuation{
			Result: map[string]any{"n": float64(2)}, ArgsUntouched: true,
			ToolMessages: []provider.Message{
				{Role: "tool", ToolCallID: call.ID, Content: `{"ok":true,"result":{"n":2},"tool":"echo","tool_call_id":"call-1"}`},
			},
		}))
	})

	It("rejects an invalid call identity before any event is persisted", func() {
		st, sid, w := callEventFixture()
		invalid := provider.ToolCall{ID: "bad id!", Name: "echo", Arguments: json.RawMessage(`{}`)}
		_, invalidErr := NewCallEventSink(w, invalid)
		events, _ := st.Replay(ctx, sid)
		Expect(callEventRejection{Err: errors.Is(invalidErr, ErrInvalidCallEventCall), Persisted: events}).To(Equal(callEventRejection{Err: true, Persisted: nil}))
	})

	It("rejects a mismatched tool name before persistence", func() {
		st, sid, w := callEventFixture()
		sink, setupErr := NewCallEventSink(w, call)
		Expect(setupErr).NotTo(HaveOccurred())
		_, mismatchErr := sink.Append(ctx, "tool:other", tool.TypeToolCall, map[string]any{"tool": "other", "args": json.RawMessage(`{}`)})
		events, _ := st.Replay(ctx, sid)
		Expect(callEventRejection{Err: errors.Is(mismatchErr, ErrCallEventMismatch), Persisted: events}).To(Equal(callEventRejection{Err: true, Persisted: nil}))
	})

	It("blocks the tool effect when the pre-call append fails", func() {
		st, sid, w := callEventFixture()
		failing := &failingSink{inner: w, marker: "call", failures: map[string]error{tool.TypeToolCall: errors.New("disk full")}}
		sink, sinkErr := NewCallEventSink(failing, call)
		Expect(sinkErr).NotTo(HaveOccurred())
		l, err := callEventLoop(sink)
		Expect(err).NotTo(HaveOccurred())
		_, invokeErr := l.Invoke(ctx, "echo", json.RawMessage(`{ "n": 2 }`))
		events, _ := st.Replay(ctx, sid)
		_, stickyErr := sink.Append(ctx, "tool:echo", tool.TypeToolCall, map[string]any{"tool": "echo", "args": json.RawMessage(`{}`)})
		Expect(callEventBlocked{Invoke: invokeErr == nil || !strings.Contains(invokeErr.Error(), "disk full"), Results: toolEventTypes(events), Sticky: !errors.Is(stickyErr, ErrCallEventSticky)}).To(Equal(callEventBlocked{Invoke: false, Results: []string{}, Sticky: false}))
	})

	It("keeps a post-result append failure sticky and exposes no acknowledged result", func() {
		st, sid, w := callEventFixture()
		callEventJournal(ctx, w, call)
		failing := &failingSink{inner: w, marker: "result", failures: map[string]error{tool.TypeToolResult: errors.New("late failure")}}
		sink, sinkErr := NewCallEventSink(failing, call)
		Expect(sinkErr).NotTo(HaveOccurred())
		l, err := callEventLoop(sink)
		Expect(err).NotTo(HaveOccurred())
		_, invokeErr := l.Invoke(ctx, "echo", json.RawMessage(`{ "n": 2 }`))
		_, stickyErr := sink.Append(ctx, "tool:echo", tool.TypeToolResult, map[string]any{"tool": "echo", "ok": true})
		_, histErr := LoadSessionHistory(ctx, st, sid, HistoryLimits{MaxEvents: 100, MaxMessages: 100, MaxBytes: 100000, MaxResultBytes: 10000})
		Expect(callEventUnacknowledged{Invoked: invokeErr == nil, Sticky: !errors.Is(stickyErr, ErrCallEventSticky), HistoryRejected: !errors.Is(histErr, ErrInvalidHistory)}).To(Equal(callEventUnacknowledged{}))
	})

	It("records denied result-only feedback without synthesising a call start", func() {
		st, sid, w := callEventFixture()
		deniedLoop, err := tool.NewLoop(callEventEchoRegistry(), w, nil)
		Expect(err).NotTo(HaveOccurred())
		_, invokeErr := deniedLoop.Invoke(ctx, "echo", json.RawMessage(`{}`))
		events, replayErr := st.Replay(ctx, sid)
		Expect(replayErr).NotTo(HaveOccurred())
		Expect(callEventDenial{Denied: errors.Is(invokeErr, tool.ErrDenied), Types: toolEventTypes(events), Payload: events[0].PayloadJSON}).To(Equal(callEventDenial{Denied: true, Types: []string{tool.TypeToolResult}, Payload: `{"error":{"code":"tool_denied","message":"tool \"echo\" is not on the session allowlist"},"ok":false,"tool":"echo"}`}))
	})

	It("rejects a repeated start for the same correlated call", func() {
		_, _, w := callEventFixture()
		sink, setupErr := NewCallEventSink(w, call)
		Expect(setupErr).NotTo(HaveOccurred())
		_, startErr := sink.Append(ctx, "tool:echo", tool.TypeToolCall, map[string]any{"tool": "echo", "args": json.RawMessage(`{ "n": 2 }`)})
		Expect(startErr).NotTo(HaveOccurred())
		_, repeatErr := sink.Append(ctx, "tool:echo", tool.TypeToolCall, map[string]any{"tool": "echo", "args": json.RawMessage(`{ "n": 2 }`)})
		Expect(errors.Is(repeatErr, ErrCallEventOrder)).To(BeTrue())
	})

	It("rejects a result before its call start", func() {
		_, _, w := callEventFixture()
		sink, setupErr := NewCallEventSink(w, call)
		Expect(setupErr).NotTo(HaveOccurred())
		_, resultErr := sink.Append(ctx, "tool:echo", tool.TypeToolResult, map[string]any{"tool": "echo", "ok": true, "result": 1})
		Expect(errors.Is(resultErr, ErrCallEventOrder)).To(BeTrue())
	})

	It("rejects mismatched correlated arguments before persistence", func() {
		_, _, w := callEventFixture()
		sink, setupErr := NewCallEventSink(w, call)
		Expect(setupErr).NotTo(HaveOccurred())
		_, argsErr := sink.Append(ctx, "tool:echo", tool.TypeToolCall, map[string]any{"tool": "echo", "args": json.RawMessage(`{"n":3}`)})
		Expect(errors.Is(argsErr, ErrCallEventMismatch)).To(BeTrue())
	})
})

// callEventAppendArgs binds a sink to a call carrying callArgs and appends
// the tool.call start with the loop-side args value, returning the error.
func callEventAppendArgs(callArgs string, loopArgs any) error {
	_, _, w := callEventFixture()
	sink, err := NewCallEventSink(w, provider.ToolCall{ID: "call-1", Name: "echo", Arguments: json.RawMessage(callArgs)})
	if err != nil {
		panic(err)
	}
	_, appendErr := sink.Append(context.Background(), "tool:echo", tool.TypeToolCall, map[string]any{"tool": "echo", "args": loopArgs})
	return appendErr
}

var _ = Describe("Semantic call argument comparison", func() {
	DescribeTable("accepts arguments that are semantically equal", func(callArgs, loopArgs string) {
		Expect(callEventAppendArgs(callArgs, json.RawMessage(loopArgs))).To(Succeed())
	},
		Entry("less-than sign", `{"task":"width < 1"}`, `{"task":"width < 1"}`),
		Entry("greater-than sign", `{"task":"a > b"}`, `{"task":"a > b"}`),
		Entry("ampersand", `{"task":"a & b"}`, `{"task":"a & b"}`),
		Entry("Go source text", `{"content":"if a < b && c > d { x := &y }"}`, `{"content":"if a < b && c > d { x := &y }"}`),
		Entry("unsorted keys", `{"task":"x","agent":"y"}`, `{"agent":"y","task":"x"}`),
		Entry("extra whitespace and newlines", "{\n  \"n\" :  2\n}", `{"n":2}`),
		Entry("unicode escape against literal", `{"s":"\u00e9"}`, `{"s":"é"}`),
		Entry("HTML escape against literal", `{"s":"\u003c"}`, `{"s":"<"}`),
		Entry("big integer", `{"n":9007199254740993}`, `{ "n": 9007199254740993 }`),
		Entry("nested objects and arrays", `{"a":[1,{"z":1,"b":[true,null]}],"c":{}}`, `{"c":{},"a":[1,{"b":[true,null],"z":1}]}`),
		Entry("empty object", `{}`, ` { } `),
		Entry("escaped quotes", `{"s":"say \"hi\""}`, `{"s":"say \u0022hi\u0022"}`))

	DescribeTable("rejects arguments that genuinely differ", func(callArgs, loopArgs string) {
		Expect(callEventAppendArgs(callArgs, json.RawMessage(loopArgs))).To(MatchError(ErrCallEventMismatch))
	},
		Entry("changed value", `{"task":"x"}`, `{"task":"y"}`),
		Entry("extra key", `{"task":"x"}`, `{"task":"x","agent":"y"}`),
		Entry("missing key", `{"task":"x","agent":"y"}`, `{"task":"x"}`),
		Entry("changed big integer", `{"n":9007199254740993}`, `{"n":9007199254740992}`),
		Entry("changed comparison character", `{"s":"a < b"}`, `{"s":"a > b"}`),
		Entry("trailing data", `{"n":1}`, `{"n":1} {"n":1}`))

	It("rejects arguments that are not valid JSON before any event", func() {
		_, _, w := callEventFixture()
		_, err := NewCallEventSink(w, provider.ToolCall{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{"n":`)})
		Expect(err).To(MatchError(ErrInvalidCallEventCall))
	})

	It("rejects arguments with trailing data before any event", func() {
		_, _, w := callEventFixture()
		_, err := NewCallEventSink(w, provider.ToolCall{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{} {}`)})
		Expect(err).To(MatchError(ErrInvalidCallEventCall))
	})

	DescribeTable("rejects a call whose meaning depends on the decoder", func(callArgs string) {
		_, _, w := callEventFixture()
		_, err := NewCallEventSink(w, provider.ToolCall{ID: "call-1", Name: "echo", Arguments: json.RawMessage(callArgs)})
		Expect(err).To(MatchError(ErrInvalidCallEventCall))
	},
		Entry("an exact duplicate key", `{"a":1,"a":2}`),
		Entry("keys equal under case folding", `{"path":"safe.txt","Path":"../escape"}`),
		Entry("a Kelvin sign key against k", "{\"k\":1,\"\u212a\":2}"),
		Entry("a collision inside a nested object", `{"x":[{"a":1,"A":2}]}`),
		Entry("invalid UTF-8 in a string", "{\"s\":\"\xff\"}"))

	It("accepts the same key in two sibling objects and after an array", func() {
		Expect(callEventAppendArgs(`{"a":{"k":1},"b":{"k":2},"c":[{"k":1},{"k":2}]}`, json.RawMessage(`{"c":[{"k":1},{"k":2}],"b":{"k":2},"a":{"k":1}}`))).To(Succeed())
	})

	It("rejects a loop value whose case-colliding keys differ in document order from the call", func() {
		Expect(callEventAppendArgs(`{"a":1}`, json.RawMessage(`{"Path":"../escape","path":"safe.txt"}`))).To(MatchError(ErrCallEventMismatch))
	})

	It("accepts a loop value built from a Go map whose keys are unsorted in the model bytes", func() {
		Expect(callEventAppendArgs(`{"task":"x<y","agent":"y"}`, map[string]any{"agent": "y", "task": "x<y"})).To(Succeed())
	})

	It("runs a call carrying Go source through the real loop and records the call and result events", func() {
		st, sid, w := callEventFixture()
		code := `{"path":"a.go","content":"if a < b && c > d { p := &x }"}`
		goCall := provider.ToolCall{ID: "call-1", Name: "echo", Arguments: json.RawMessage(code)}
		_, l := callEventEchoLoop(w, goCall)
		_, invokeErr := l.Invoke(context.Background(), "echo", json.RawMessage(code))
		if invokeErr != nil {
			panic(invokeErr)
		}
		events, _ := st.Replay(context.Background(), sid)
		Expect(toolEventTypes(events)).To(Equal([]string{tool.TypeToolCall, tool.TypeToolResult}))
	})

	It("stores the arguments the loop passed without losing information", func() {
		st, sid, w := callEventFixture()
		code := `{"n":9007199254740993,"s":"é <&> \"q\"","a":[{"k":null}]}`
		goCall := provider.ToolCall{ID: "call-1", Name: "echo", Arguments: json.RawMessage(code)}
		_, l := callEventEchoLoop(w, goCall)
		if _, err := l.Invoke(context.Background(), "echo", json.RawMessage(code)); err != nil {
			panic(err)
		}
		events, _ := st.Replay(context.Background(), sid)
		var payload struct {
			Args json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal([]byte(events[0].PayloadJSON), &payload); err != nil {
			panic(err)
		}
		stored := string(payload.Args)
		want, _ := canonicalJSON([]byte(code))
		got, _ := canonicalJSON([]byte(stored))
		Expect(got).To(Equal(want))
	})
})

func toolEventTypes(events []store.RecordedEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}
