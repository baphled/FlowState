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
		Expect(callEventContinuation{Result: result, ArgsUntouched: string(call.Arguments) == before, ToolMessages: messages[1:]}).To(Equal(callEventContinuation{
			Result: map[string]any{"n": float64(2)}, ArgsUntouched: true,
			ToolMessages: []provider.Message{
				{Role: "tool", ToolCallID: call.ID, Content: `{"tool":"echo","tool_call_id":"call-1","ok":true,"result":{"n":2}}`},
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
		callEventJournal(ctx, w, call)
		failing := &failingSink{inner: w, marker: "call", failures: map[string]error{tool.TypeToolCall: errors.New("disk full")}}
		sink, sinkErr := NewCallEventSink(failing, call)
		Expect(sinkErr).NotTo(HaveOccurred())
		l, err := callEventLoop(sink)
		Expect(err).NotTo(HaveOccurred())
		_, invokeErr := l.Invoke(ctx, "echo", json.RawMessage(`{ "n": 2 }`))
		events, _ := st.Replay(ctx, sid)
		_, stickyErr := sink.Append(ctx, "tool:echo", tool.TypeToolCall, map[string]any{"tool": "echo", "args": json.RawMessage(`{}`)})
		Expect(callEventBlocked{Invoke: invokeErr == nil || !strings.Contains(invokeErr.Error(), "disk full"), Results: toolEventTypes(events), Sticky: !errors.Is(stickyErr, ErrCallEventSticky)}).To(Equal(callEventBlocked{Invoke: false, Results: nil, Sticky: false}))
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

func toolEventTypes(events []store.RecordedEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}
