package zai

import (
	"context"
	"encoding/json"

	"github.com/baphled/flowstate/internal/provider"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("anthropic wire tool calls cut by the output cap", func() {
	block := func(index int, id, args string) string {
		start := `data: {"type":"content_block_start","index":` + string(rune('0'+index)) + `,"content_block":{"type":"tool_use","id":"` + id + `","name":"read","input":{}}}` + "\n"
		delta, _ := json.Marshal(args)
		part := `data: {"type":"content_block_delta","index":` + string(rune('0'+index)) + `,"delta":{"type":"input_json_delta","partial_json":` + string(delta) + `}}` + "\n"
		stop := `data: {"type":"content_block_stop","index":` + string(rune('0'+index)) + `}` + "\n"
		return start + part + stop
	}
	end := func(reason string) string {
		return `data: {"type":"message_delta","delta":{"stop_reason":"` + reason + `"}}` + "\n" + `data: {"type":"message_stop"}` + "\n"
	}
	stream := func(sse string) []provider.StreamEvent {
		srv, _ := codingServer(sse, "")
		DeferCleanup(srv.Close)
		p := New("k", WithBaseURL(srv.URL), WithCoding(), WithCodingRoute("anthropic"), WithAnthropicFinish(), WithAnthropicTools())
		var events []provider.StreamEvent
		for ev := range p.Stream(context.Background(), toolRequest()) {
			events = append(events, ev)
		}
		return events
	}
	call := func(id, args string) provider.StreamEvent {
		return provider.StreamEvent{Kind: provider.StreamEventToolCall, ToolCall: &provider.ToolCall{ID: id, Name: "read", Arguments: json.RawMessage(args)}}
	}
	fin := func(reason string) provider.StreamEvent {
		return provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: reason}
	}
	DescribeTable("drops the partial call and delivers the length finish", func(sse string, want []provider.StreamEvent) {
		Expect(stream(sse)).To(Equal(want))
	},
		Entry("one partial call", block(0, "a", `{"path":"x`)+end("max_tokens"), []provider.StreamEvent{fin("length")}),
		Entry("complete call then a partial one", block(0, "a", `{"p":1}`)+block(1, "b", `{"p":`)+end("max_tokens"),
			[]provider.StreamEvent{call("a", `{"p":1}`), fin("length")}),
		Entry("partial call whose block never stopped", block(0, "a", `{"p":1}`)+`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"b","name":"read","input":{}}}`+"\n"+end("max_tokens"),
			[]provider.StreamEvent{call("a", `{"p":1}`), fin("length")}),
		Entry("a complete call after a partial one is dropped", block(0, "a", `{"p":`)+block(1, "b", `{"p":1}`)+end("max_tokens"),
			[]provider.StreamEvent{fin("length")}),
		Entry("complete empty object", block(0, "a", `{}`)+end("max_tokens"), []provider.StreamEvent{call("a", `{}`), fin("length")}),
	)
	DescribeTable("keeps the protocol error", func(sse string) {
		events := stream(sse)
		Expect(events[len(events)-1].Err).To(MatchError(ContainSubstring("tool arguments must be a JSON object")))
	},
		Entry("cut call with a tool_use stop", block(0, "a", `{"path":"x`)+end("tool_use")),
		Entry("cut call with an end_turn stop", block(0, "a", `{"path":"x`)+end("end_turn")),
		Entry("complete non-object with max_tokens", block(0, "a", `[1]`)+end("max_tokens")),
		Entry("corrupt arguments with max_tokens", block(0, "a", `{bad`)+end("max_tokens")),
		Entry("two objects with max_tokens", block(0, "a", `{"a":1}{"b":2}`)+end("max_tokens")),
		Entry("cut scalar with max_tokens", block(0, "a", `nul`)+end("max_tokens")),
		Entry("trailing garbage with max_tokens", block(0, "a", `{"a":1} x`)+end("max_tokens")),
	)
})
