package provider

import (
	"context"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type contractStreamer struct{ events []StreamEvent }

func (*contractStreamer) Name() string     { return "synthetic" }
func (*contractStreamer) Models() []string { return []string{"synthetic"} }
func (*contractStreamer) Chat(context.Context, *ChatRequest) (*ChatResponse, error) {
	return &ChatResponse{}, nil
}
func (f *contractStreamer) Stream(context.Context, *ChatRequest) <-chan StreamEvent {
	out := make(chan StreamEvent, len(f.events))
	for _, event := range f.events {
		out <- event
	}
	close(out)
	return out
}

var _ = Describe("provider tool contract", func() {
	It("preserves ordered text and complete tool calls through the streaming port", func() {
		events := []StreamEvent{{Kind: StreamEventText, Content: "checking"}, {Kind: StreamEventToolCall, ToolCall: &ToolCall{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"key":"x"}`)}}, {Kind: StreamEventFinish, FinishReason: "tool_calls"}}
		var port Streamer = &contractStreamer{events: events}
		validator := &StreamValidator{}
		var got []StreamEvent
		for event := range port.Stream(context.Background(), &ChatRequest{}) {
			err := validator.Accept(event)
			Expect(err).NotTo(HaveOccurred())
			got = append(got, event)
		}
		Expect(got).To(Equal(events))
	})
	It("round trips a result correlated with its assistant call", func() {
		req := ChatRequest{Messages: []Message{{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{}`)}}}, {Role: "tool", Content: "found", ToolCallID: "call_1"}}}
		encoded, err := json.Marshal(req)
		Expect(err).NotTo(HaveOccurred())
		var decoded ChatRequest
		err = json.Unmarshal(encoded, &decoded)
		Expect(err).NotTo(HaveOccurred())
		err = decoded.Validate()
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded.Messages[1].ToolCallID).To(Equal(req.Messages[0].ToolCalls[0].ID))
	})
	DescribeTable("rejects malformed call IDs", func(id string) {
		call := ToolCall{ID: id, Name: "lookup", Arguments: json.RawMessage(`{}`)}
		Expect(call.Validate()).NotTo(Succeed())
	}, Entry("empty", ""), Entry("whitespace", "call 1"), Entry("punctuation", "call/1"))
	DescribeTable("rejects invalid argument objects", func(args string) {
		call := ToolCall{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(args)}
		Expect(call.Validate()).NotTo(Succeed())
	}, Entry("truncated", `{"x":`), Entry("array", `[]`), Entry("null", `null`), Entry("absent", ""))
	It("rejects duplicate call IDs across streamed events", func() {
		validator := &StreamValidator{}
		event := StreamEvent{Kind: StreamEventToolCall, ToolCall: &ToolCall{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{}`)}}
		err := validator.Accept(event)
		Expect(err).NotTo(HaveOccurred())
		Expect(validator.Accept(event)).NotTo(Succeed())
	})
	It("rejects duplicate call IDs across history messages", func() {
		message := Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{}`)}}}
		req := ChatRequest{Messages: []Message{message, message}}
		Expect(req.Validate()).NotTo(Succeed())
	})
	It("rejects a result without a preceding call", func() {
		req := ChatRequest{Messages: []Message{{Role: "tool", ToolCallID: "unknown"}}}
		Expect(req.Validate()).NotTo(Succeed())
	})
	It("accepts legacy content events", func() {
		validator := &StreamValidator{}
		Expect(validator.Accept(StreamEvent{Content: "legacy"})).To(Succeed())
	})
})

var _ = Describe("provider validation boundaries", func() {
	DescribeTable("rejects malformed stream envelopes", func(event StreamEvent) {
		validator := &StreamValidator{}
		Expect(validator.Accept(event)).NotTo(Succeed())
	}, Entry("unknown kind", StreamEvent{Kind: "unknown"}),
		Entry("missing call", StreamEvent{Kind: StreamEventToolCall}),
		Entry("text mixed with call", StreamEvent{Kind: StreamEventToolCall, Content: "mixed", ToolCall: &ToolCall{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{}`)}}),
		Entry("missing finish reason", StreamEvent{Kind: StreamEventFinish}),
		Entry("missing error", StreamEvent{Kind: StreamEventError}),
		Entry("untyped structured call", StreamEvent{ToolCall: &ToolCall{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{}`)}}))
	It("rejects events after a finish", func() {
		validator := &StreamValidator{}
		err := validator.Accept(StreamEvent{Kind: StreamEventFinish, FinishReason: "stop"})
		Expect(err).NotTo(HaveOccurred())
		Expect(validator.Accept(StreamEvent{Content: "late"})).NotTo(Succeed())
	})

	It("accepts a structured tool definition", func() {
		req := ChatRequest{Tools: []ToolDefinition{{Name: "fs.read", Parameters: json.RawMessage(`{"type":"object"}`)}}}
		Expect(req.Validate()).To(Succeed())
	})
	It("rejects duplicate tool definitions", func() {
		tool := ToolDefinition{Name: "lookup", Parameters: json.RawMessage(`{}`)}
		req := ChatRequest{Tools: []ToolDefinition{tool, tool}}
		Expect(req.Validate()).NotTo(Succeed())
	})
	It("rejects malformed tool parameter schemas", func() {
		req := ChatRequest{Tools: []ToolDefinition{{Name: "lookup", Parameters: json.RawMessage(`[]`)}}}
		Expect(req.Validate()).NotTo(Succeed())
	})
	It("rejects repeated tool results", func() {
		req := ChatRequest{Messages: []Message{{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{}`)}}}, {Role: "tool", ToolCallID: "call_1"}, {Role: "tool", ToolCallID: "call_1"}}}
		Expect(req.Validate()).NotTo(Succeed())
	})
})

var _ = DescribeTable("namespaced tools", func(name string) {
	call := ToolCall{ID: "call_1", Name: name, Arguments: json.RawMessage(`{}`)}
	Expect(call.Validate()).To(Succeed())
}, Entry("read", "fs.read"), Entry("write", "fs.write"), Entry("edit", "fs.edit"), Entry("execute", "exec.run"))
