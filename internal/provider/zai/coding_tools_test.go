package zai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	"github.com/baphled/flowstate/internal/provider"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func toolRequest() *provider.ChatRequest {
	return &provider.ChatRequest{Tools: []provider.ToolDefinition{{Name: "read", Description: "Read", Parameters: json.RawMessage(`{"type":"object"}`)}}}
}
func toolFixture(sse string) *Provider {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, sse) }))
	DeferCleanup(srv.Close)
	return New("k", WithCoding(), WithBaseURL(srv.URL))
}
func toolEvents(p *Provider, req *provider.ChatRequest) []provider.StreamEvent {
	var events []provider.StreamEvent
	for ev := range p.Stream(context.Background(), req) {
		events = append(events, ev)
	}
	return events
}

const firstCall = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"re","arguments":"{\"p\":"}},{"index":1,"id":"call_b","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}` + "\n"
const lastCall = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"ad","arguments":"1}"}}]},"finish_reason":"tool_calls"}]}` + "\n"
const done = "data: [DONE]\n"

var _ = Describe("coding structured tools", func() {
	It("encodes definitions and correlated history in OpenAI format", func() {
		captured := make(chan string, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				panic(err)
			}
			captured <- string(b)
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
		}))
		DeferCleanup(srv.Close)
		req := toolRequest()
		req.Model = "fixture"
		req.Messages = []provider.Message{{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "old", Name: "read", Arguments: json.RawMessage(`{}`)}}}, {Role: "tool", Content: "result", ToolCallID: "old"}}
		_, err := New("k", WithCoding(), WithBaseURL(srv.URL)).Chat(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		Expect(<-captured).To(MatchJSON(`{"model":"fixture","max_tokens":8192,"tools":[{"type":"function","function":{"name":"read","description":"Read","parameters":{"type":"object"}}}],"messages":[{"role":"assistant","content":"","tool_calls":[{"id":"old","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","content":"result","tool_call_id":"old"}]}`))
	})
	DescribeTable("rejects unsupported structured Anthropic requests before HTTP", func(stream bool) {
		var requests atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); _, _ = io.WriteString(w, `{}`) }))
		DeferCleanup(srv.Close)
		p := New("k", WithCoding(), WithCodingRoute("anthropic"), WithBaseURL(srv.URL))
		var err error
		if stream {
			for _, ev := range toolEvents(p, toolRequest()) {
				err = ev.Err
			}
		} else {
			_, err = p.Chat(context.Background(), toolRequest())
		}
		var typed *ToolProtocolError
		Expect(errors.As(err, &typed)).To(BeTrue())
	}, Entry("chat", false), Entry("stream", true))
	It("rejects unmatched result history before HTTP", func() {
		var requests atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1) }))
		DeferCleanup(srv.Close)
		req := toolRequest()
		req.Messages = []provider.Message{{Role: "tool", ToolCallID: "unknown"}}
		_, _ = New("k", WithCoding(), WithBaseURL(srv.URL)).Chat(context.Background(), req)
		Expect(requests.Load()).To(BeZero())
	})
	It("blocks HTTP for unsupported structured history", func() {
		var requests atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { requests.Add(1) }))
		DeferCleanup(srv.Close)
		req := &provider.ChatRequest{Messages: []provider.Message{{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "a", Name: "read", Arguments: json.RawMessage(`{}`)}}}}}
		_, _ = New("k", WithCoding(), WithCodingRoute("anthropic"), WithBaseURL(srv.URL)).Chat(context.Background(), req)
		Expect(requests.Load()).To(BeZero())
	})
	It("fails closed for nonstream tool responses", func() {
		_, err := toolFixture(`{"choices":[{"message":{"tool_calls":[{"id":"a"}]}}]}`).Chat(context.Background(), toolRequest())
		var typed *ToolProtocolError
		Expect(errors.As(err, &typed)).To(BeTrue())
	})
})
