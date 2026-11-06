package zai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("coding structured SSE", func() {
	It("assembles interleaved calls before the finish event", func() {
		events := toolEvents(toolFixture(firstCall+lastCall+done), toolRequest())
		Expect(events).To(Equal([]provider.StreamEvent{{Kind: provider.StreamEventToolCall, ToolCall: &provider.ToolCall{ID: "call_a", Name: "read", Arguments: json.RawMessage(`{"p":1}`)}}, {Kind: provider.StreamEventToolCall, ToolCall: &provider.ToolCall{ID: "call_b", Name: "read", Arguments: json.RawMessage(`{}`)}}, {Kind: provider.StreamEventFinish, FinishReason: "tool_calls"}}))
	})
	DescribeTable("rejects malformed streams with a typed error", func(sse string) {
		events := toolEvents(toolFixture(sse), toolRequest())
		var typed *ToolProtocolError
		var last error
		for _, ev := range events {
			if ev.Err != nil {
				last = ev.Err
			}
		}
		Expect(errors.As(last, &typed)).To(BeTrue())
	},
		Entry("malformed chunk", "data: {oops}\n"+done),
		Entry("truncated transport", firstCall),
		Entry("missing done", firstCall+lastCall),
		Entry("missing finish", firstCall+done),
		Entry("invalid arguments", strings.Replace(firstCall+lastCall+done, `1}`, `1`, 1)),
		Entry("duplicate ID", strings.Replace(firstCall+lastCall+done, "call_b", "call_a", 1)),
		Entry("conflicting ID", firstCall+strings.Replace(lastCall, `"index":0,`, `"index":0,"id":"changed",`, 1)+done),
		Entry("unknown index", strings.Replace(firstCall+lastCall+done, `"index":1`, `"index":3`, 1)),
		Entry("unknown continuation", strings.Replace(firstCall+lastCall+done, `"index":0,"function"`, `"index":2,"function"`, 1)),
		Entry("truncated arguments", firstCall+`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n"+done),
	)
	It("rejects unsolicited calls on legacy requests", func() {
		events := toolEvents(toolFixture(firstCall+lastCall+done), &provider.ChatRequest{})
		var typed *ToolProtocolError
		Expect(errors.As(events[len(events)-1].Err, &typed)).To(BeTrue())
	})
	It("closes an unconsumed structured stream after cancellation", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		events := toolFixture(firstCall+lastCall+done).Stream(ctx, toolRequest())
		cancel()
		Eventually(events).WithTimeout(time.Second).WithPolling(time.Millisecond).Should(BeClosed())
	})
	It("emits text and finish for structured turns without tool calls", func() {
		events := toolEvents(toolFixture(`data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n"+done), toolRequest())
		Expect(events).To(Equal([]provider.StreamEvent{{Kind: provider.StreamEventText, Content: "ok"}, {Kind: provider.StreamEventFinish, FinishReason: "stop"}}))
	})

})
