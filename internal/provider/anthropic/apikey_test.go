package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
	"github.com/baphled/flowstate/internal/provider/zai"
)

// capturedRequest is what the fake Anthropic API saw.
type capturedRequest struct {
	method string
	path   string
	header http.Header
	body   string
}

var _ = Describe("APIKey", func() {
	const key = "sk-ant-synthetic-test-key"
	const sse = "event: content_block_delta\n" +
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"he"}}` + "\n\n" +
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"llo"}}` + "\n\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	var (
		status int
		reply  string
		seen   chan capturedRequest
		srv    *httptest.Server
		p      *anthropic.APIKey
	)

	BeforeEach(func() {
		status = http.StatusOK
		reply = `{"content":[{"type":"text","text":"hello"}]}`
		seen = make(chan capturedRequest, 4)
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			seen <- capturedRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: string(b)}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, reply)
		}))
		DeferCleanup(srv.Close)
		p = anthropic.NewAPIKey(key, srv.URL, nil)
	})

	chat := func(req *provider.ChatRequest) (*provider.ChatResponse, error) {
		return p.Chat(context.Background(), req)
	}
	request := func(req *provider.ChatRequest) capturedRequest {
		if _, err := chat(req); err != nil {
			panic(err)
		}
		return <-seen
	}
	hello := func() *provider.ChatRequest {
		return &provider.ChatRequest{Messages: []provider.Message{{Role: "user", Content: "hi"}}}
	}
	bodyField := func(c capturedRequest, name string) any {
		var m map[string]any
		if err := json.Unmarshal([]byte(c.body), &m); err != nil {
			panic(err)
		}
		return m[name]
	}

	It("is named anthropic", func() {
		Expect(p.Name()).To(Equal("anthropic"))
	})

	It("lists the current Claude model ids", func() {
		Expect(p.Models()).To(Equal([]string{"claude-sonnet-5-5", "claude-haiku-5-5", "claude-opus-5-5"}))
	})

	It("posts to /v1/messages", func() {
		c := request(hello())
		Expect(c.method + " " + c.path).To(Equal("POST /v1/messages"))
	})

	It("sends the key as x-api-key", func() {
		Expect(request(hello()).header.Get("x-api-key")).To(Equal(key))
	})

	It("sends the anthropic-version header", func() {
		Expect(request(hello()).header.Get("anthropic-version")).To(Equal("2023-06-01"))
	})

	It("sets exactly the Anthropic and content headers beyond Go's defaults", func() {
		h := request(hello()).header
		for _, d := range []string{"User-Agent", "Accept-Encoding", "Content-Length"} {
			h.Del(d)
		}
		var names []string
		for k := range h {
			names = append(names, k)
		}
		sort.Strings(names)
		Expect(names).To(Equal([]string{"Anthropic-Version", "Content-Type", "X-Api-Key"}))
	})

	It("sends Go's default User-Agent, not a Claude Code identity", func() {
		Expect(request(hello()).header.Get("User-Agent")).To(HavePrefix("Go-http-client/"))
	})

	It("returns the reply text", func() {
		resp, err := chat(hello())
		if err != nil {
			panic(err)
		}
		Expect(resp.Content).To(Equal("hello"))
	})

	It("passes the request model through unchanged", func() {
		req := hello()
		req.Model = "claude-future-9"
		Expect(bodyField(request(req), "model")).To(Equal("claude-future-9"))
	})

	It("defaults the model when the request names none", func() {
		Expect(bodyField(request(hello()), "model")).To(Equal("claude-sonnet-5-5"))
	})

	It("sends max_tokens, defaulting to 8192", func() {
		Expect(bodyField(request(hello()), "max_tokens")).To(BeEquivalentTo(8192))
	})

	It("sends an explicit small max_tokens as given", func() {
		req := hello()
		req.MaxTokens = 20
		Expect(bodyField(request(req), "max_tokens")).To(BeEquivalentTo(20))
	})

	It("honours a larger max_tokens", func() {
		req := hello()
		req.MaxTokens = 20000
		Expect(bodyField(request(req), "max_tokens")).To(BeEquivalentTo(20000))
	})

	It("sends system messages as the top-level system field", func() {
		req := &provider.ChatRequest{Messages: []provider.Message{
			{Role: "system", Content: "be brief"}, {Role: "user", Content: "hi"}}}
		Expect(bodyField(request(req), "system")).To(Equal("be brief"))
	})

	It("keeps system messages out of the messages array", func() {
		req := &provider.ChatRequest{Messages: []provider.Message{
			{Role: "system", Content: "be brief"}, {Role: "user", Content: "hi"}}}
		Expect(bodyField(request(req), "messages")).To(HaveLen(1))
	})

	Describe("structured tools", func() {
		const sseTool = `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use",` +
			`"id":"toolu_01","name":"echo","input":{}}}` + "\n\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"mess"}}` + "\n\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"age\":\"hi\"}"}}` + "\n\n" +
			`data: {"type":"content_block_stop","index":0}` + "\n\n" +
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}` + "\n\n" +
			`data: {"type":"message_stop"}` + "\n\n"
		var echoCall provider.ToolCall
		BeforeEach(func() {
			echoCall = provider.ToolCall{ID: "toolu_01", Name: "echo", Arguments: json.RawMessage(`{"message":"hi"}`)}
		})
		toolRequest := func() *provider.ChatRequest {
			req := hello()
			req.Tools = []provider.ToolDefinition{{Name: "echo", Description: "Echo a message",
				Parameters: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}}}`)}}
			return req
		}
		historyRequest := func(results ...provider.Message) *provider.ChatRequest {
			req := toolRequest()
			req.Messages = append(req.Messages, provider.Message{Role: "assistant", Content: "calling",
				ToolCalls: []provider.ToolCall{echoCall, {ID: "toolu_02", Name: "echo", Arguments: json.RawMessage(`{}`)}}})
			req.Messages = append(req.Messages, results...)
			return req
		}
		bodyJSON := func(c capturedRequest, name string) string {
			b, err := json.Marshal(bodyField(c, name))
			if err != nil {
				panic(err)
			}
			return string(b)
		}
		streamTool := func(body string) []provider.StreamEvent {
			reply = body
			var events []provider.StreamEvent
			for ev := range p.Stream(context.Background(), toolRequest()) {
				events = append(events, ev)
			}
			return events
		}

		It("sends the tools with name, description and input_schema", func() {
			Expect(bodyJSON(request(toolRequest()), "tools")).To(MatchJSON(
				`[{"name":"echo","description":"Echo a message","input_schema":` +
					`{"type":"object","properties":{"message":{"type":"string"}}}}]`))
		})

		It("omits the tools field when the request has none", func() {
			Expect(bodyField(request(hello()), "tools")).To(BeNil())
		})

		It("returns a tool_use block as a tool call with its arguments intact", func() {
			reply = `{"content":[{"type":"tool_use","id":"toolu_01","name":"echo","input":{"message":"hi"}}],"stop_reason":"tool_use"}`
			resp, err := chat(toolRequest())
			if err != nil {
				panic(err)
			}
			Expect(resp.ToolCalls).To(Equal([]provider.ToolCall{echoCall}))
		})

		It("keeps the text that precedes a tool_use block", func() {
			reply = `{"content":[{"type":"text","text":"checking"},` +
				`{"type":"tool_use","id":"toolu_01","name":"echo","input":{}}]}`
			resp, err := chat(toolRequest())
			if err != nil {
				panic(err)
			}
			Expect(resp.Content).To(Equal("checking"))
		})

		It("rejects a tool_use block with an invalid id as a tool protocol error", func() {
			reply = `{"content":[{"type":"tool_use","id":"","name":"echo","input":{}}]}`
			_, err := chat(toolRequest())
			var protocol *zai.ToolProtocolError
			Expect(errors.As(err, &protocol)).To(BeTrue())
		})

		It("sends an assistant call as text then tool_use blocks", func() {
			Expect(bodyJSON(request(historyRequest()), "messages")).To(MatchJSON(`[
				{"role":"user","content":"hi"},
				{"role":"assistant","content":[{"type":"text","text":"calling"},
					{"type":"tool_use","id":"toolu_01","name":"echo","input":{"message":"hi"}},
					{"type":"tool_use","id":"toolu_02","name":"echo","input":{}}]}]`))
		})

		It("sends tool results as tool_result blocks of one user message", func() {
			req := historyRequest(
				provider.Message{Role: "tool", ToolCallID: "toolu_01", Content: `{"echo":"hi"}`},
				provider.Message{Role: "tool", ToolCallID: "toolu_02", Content: "error: nope"})
			Expect(bodyJSON(request(req), "messages")).To(MatchJSON(`[
				{"role":"user","content":"hi"},
				{"role":"assistant","content":[{"type":"text","text":"calling"},
					{"type":"tool_use","id":"toolu_01","name":"echo","input":{"message":"hi"}},
					{"type":"tool_use","id":"toolu_02","name":"echo","input":{}}]},
				{"role":"user","content":[
					{"type":"tool_result","tool_use_id":"toolu_01","content":"{\"echo\":\"hi\"}"},
					{"type":"tool_result","tool_use_id":"toolu_02","content":"error: nope"}]}]`))
		})

		It("assembles a streamed tool_use from partial JSON deltas into one tool call", func() {
			var calls []provider.ToolCall
			for _, ev := range streamTool(sseTool) {
				if ev.Kind == provider.StreamEventToolCall {
					calls = append(calls, *ev.ToolCall)
				}
			}
			Expect(calls).To(Equal([]provider.ToolCall{echoCall}))
		})

		It("gives a streamed tool_use with no input an empty object", func() {
			events := streamTool(`data: {"type":"content_block_start","index":1,"content_block":` +
				`{"type":"tool_use","id":"toolu_09","name":"echo","input":{}}}` + "\n\n" +
				`data: {"type":"content_block_stop","index":1}` + "\n\n" +
				`data: {"type":"message_stop"}` + "\n\n")
			Expect(string(events[0].ToolCall.Arguments)).To(Equal("{}"))
		})

		It("maps the tool_use stop reason to the tool_calls finish reason", func() {
			events := streamTool(sseTool)
			Expect(events[len(events)-1].FinishReason).To(Equal(provider.FinishToolCalls))
		})

		It("emits the tool call before the finish event", func() {
			events := streamTool(sseTool)
			Expect(events[0].Kind).To(Equal(provider.StreamEventToolCall))
		})

		It("passes a streamed tool turn through the stream validator", func() {
			var v provider.StreamValidator
			var first error
			for _, ev := range streamTool(sseTool) {
				if err := v.Accept(ev); err != nil && first == nil {
					first = err
				}
			}
			Expect(first).NotTo(HaveOccurred())
		})

		It("emits no tool call for a tool_use cut before its content_block_stop", func() {
			events := streamTool(strings.Split(sseTool, `data: {"type":"content_block_stop"`)[0])
			Expect(events).NotTo(ContainElement(HaveField("Kind", provider.StreamEventToolCall)))
		})

		It("ends a streamed tool_use with malformed input in a tool protocol error", func() {
			events := streamTool(strings.Replace(sseTool, `age\":\"hi\"}`, `age\":`, 1))
			var protocol *zai.ToolProtocolError
			Expect(errors.As(events[len(events)-1].Err, &protocol)).To(BeTrue())
		})

		Describe("tool names", func() {
			dotted := func(names ...string) *provider.ChatRequest {
				req := hello()
				for _, n := range names {
					req.Tools = append(req.Tools, provider.ToolDefinition{Name: n, Parameters: json.RawMessage(`{"type":"object"}`)})
				}
				return req
			}
			startStop := func(index int, id, name string) string {
				return `data: {"type":"content_block_start","index":` + strconv.Itoa(index) +
					`,"content_block":{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":{}}}` + "\n\n"
			}
			stopBlock := func(index int) string {
				return `data: {"type":"content_block_stop","index":` + strconv.Itoa(index) + `}` + "\n\n"
			}
			const stopMsg = `data: {"type":"message_stop"}` + "\n\n"
			lastErr := func(events []provider.StreamEvent) error { return events[len(events)-1].Err }
			protocolErr := func(err error) bool {
				var protocol *zai.ToolProtocolError
				return errors.As(err, &protocol)
			}

			It("sends a dotted tool name with an underscore", func() {
				Expect(bodyJSON(request(dotted("fs.read")), "tools")).To(ContainSubstring(`"name":"fs_read"`))
			})

			It("returns the dotted name for a wire name in a Chat reply", func() {
				reply = `{"content":[{"type":"tool_use","id":"toolu_01","name":"fs_read","input":{}}]}`
				resp, err := chat(dotted("fs.read"))
				if err != nil {
					panic(err)
				}
				Expect(resp.ToolCalls[0].Name).To(Equal("fs.read"))
			})

			It("returns the dotted name for a wire name in a streamed reply", func() {
				reply = startStop(0, "toolu_01", "fs_read") + stopBlock(0) + stopMsg
				events := []provider.StreamEvent{}
				for ev := range p.Stream(context.Background(), dotted("fs.read")) {
					events = append(events, ev)
				}
				Expect(events[0].ToolCall.Name).To(Equal("fs.read"))
			})

			It("sends a dotted history call name with an underscore", func() {
				req := dotted("exec.run")
				req.Messages = append(req.Messages, provider.Message{Role: "assistant",
					ToolCalls: []provider.ToolCall{{ID: "toolu_01", Name: "exec.run", Arguments: json.RawMessage(`{}`)}}})
				Expect(bodyJSON(request(req), "messages")).To(ContainSubstring(`"name":"exec_run"`))
			})

			It("refuses two tool names with one wire name in Chat", func() {
				_, err := chat(dotted("a.b", "a_b"))
				Expect(protocolErr(err)).To(BeTrue())
			})

			It("refuses two tool names with one wire name in Stream", func() {
				events := []provider.StreamEvent{}
				for ev := range p.Stream(context.Background(), dotted("a.b", "a_b")) {
					events = append(events, ev)
				}
				Expect(protocolErr(lastErr(events))).To(BeTrue())
			})

			It("sends no request for a colliding pair", func() {
				_, _ = chat(dotted("a.b", "a_b"))
				Expect(seen).To(BeEmpty())
			})

			It("refuses a history call whose wire name collides with a tool", func() {
				req := dotted("a_b")
				req.Messages = append(req.Messages, provider.Message{Role: "assistant",
					ToolCalls: []provider.ToolCall{{ID: "toolu_01", Name: "a.b", Arguments: json.RawMessage(`{}`)}}})
				_, err := chat(req)
				Expect(protocolErr(err)).To(BeTrue())
			})

			It("refuses an unknown wire name in a Chat reply", func() {
				reply = `{"content":[{"type":"tool_use","id":"toolu_01","name":"other","input":{}}]}`
				_, err := chat(dotted("fs.read"))
				Expect(protocolErr(err)).To(BeTrue())
			})

			It("refuses a neutral spelling in a Chat reply", func() {
				reply = `{"content":[{"type":"tool_use","id":"toolu_01","name":"fs.read","input":{}}]}`
				_, err := chat(dotted("fs.read"))
				Expect(protocolErr(err)).To(BeTrue())
			})

			It("refuses an unknown wire name in a streamed reply", func() {
				reply = startStop(0, "toolu_01", "other") + stopBlock(0) + stopMsg
				events := []provider.StreamEvent{}
				for ev := range p.Stream(context.Background(), dotted("fs.read")) {
					events = append(events, ev)
				}
				Expect(protocolErr(lastErr(events))).To(BeTrue())
			})

			It("ends a message with an unfinished tool_use in a tool protocol error", func() {
				events := streamTool(startStop(0, "toolu_01", "echo") +
					`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}` + "\n\n" + stopMsg)
				Expect(protocolErr(lastErr(events))).To(BeTrue())
			})

			It("lets a max_tokens message end with an unfinished tool_use without an error", func() {
				events := streamTool(startStop(0, "toolu_01", "echo") +
					`data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"}}` + "\n\n" + stopMsg)
				Expect(lastErr(events)).NotTo(HaveOccurred())
			})

			It("refuses a duplicate content block index in a stream", func() {
				events := streamTool(startStop(0, "toolu_01", "echo") + startStop(0, "toolu_02", "echo") + stopBlock(0) + stopMsg)
				Expect(protocolErr(lastErr(events))).To(BeTrue())
			})

			It("refuses a duplicate tool id in a stream", func() {
				events := streamTool(startStop(0, "toolu_01", "echo") + stopBlock(0) +
					startStop(1, "toolu_01", "echo") + stopBlock(1) + stopMsg)
				Expect(protocolErr(lastErr(events))).To(BeTrue())
			})

			It("refuses a duplicate tool id in a Chat reply", func() {
				reply = `{"content":[{"type":"tool_use","id":"toolu_01","name":"echo","input":{}},` +
					`{"type":"tool_use","id":"toolu_01","name":"echo","input":{}}]}`
				_, err := chat(toolRequest())
				Expect(protocolErr(err)).To(BeTrue())
			})

			It("refuses a streamed tool input above one mebibyte", func() {
				delta := func(fragment string) string {
					return `data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"` +
						fragment + `"}}` + "\n\n"
				}
				fill := strings.Repeat("x", 1<<19)
				events := streamTool(startStop(0, "toolu_01", "echo") + delta(`{\"a\":\"`+fill) + delta(fill) + delta(fill) +
					delta(`\"}`) + stopBlock(0) + stopMsg)
				Expect(protocolErr(lastErr(events))).To(BeTrue())
			})

			It("does not send whitespace-only assistant text as a text block", func() {
				req := toolRequest()
				req.Messages = append(req.Messages, provider.Message{Role: "assistant", Content: "\n\n", ToolCalls: []provider.ToolCall{echoCall}})
				Expect(bodyJSON(request(req), "messages")).NotTo(ContainSubstring(`"type":"text"`))
			})
		})

		It("never puts the key in a tool error", func() {
			reply = `{"content":[{"type":"tool_use","id":"` + key + `!","name":"echo","input":{}}]}`
			_, err := chat(toolRequest())
			Expect(err).To(MatchError(Not(ContainSubstring(key))))
		})
	})

	Describe("streaming", func() {
		var events []provider.StreamEvent

		BeforeEach(func() {
			reply = sse
			events = nil
			for ev := range p.Stream(context.Background(), hello()) {
				events = append(events, ev)
			}
		})

		It("streams text content events", func() {
			var text string
			for _, ev := range events {
				text += ev.Content
			}
			Expect(text).To(Equal("hello"))
		})

		It("asks for a streamed response", func() {
			Expect(bodyField(<-seen, "stream")).To(BeTrue())
		})

		It("ends with a finish event", func() {
			Expect(events[len(events)-1].Kind).To(Equal(provider.StreamEventFinish))
		})

		It("passes the stream validator", func() {
			var v provider.StreamValidator
			var first error
			for _, ev := range events {
				if err := v.Accept(ev); err != nil && first == nil {
					first = err
				}
			}
			Expect(first).NotTo(HaveOccurred())
		})
	})

	Describe("stream failures", func() {
		const part = `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"par"}}` + "\n\n"
		const stop = `data: {"type":"message_stop"}` + "\n\n"
		var events []provider.StreamEvent
		run := func(body string) {
			reply = body
			events = nil
			for ev := range p.Stream(context.Background(), hello()) {
				events = append(events, ev)
			}
		}
		last := func() provider.StreamEvent { return events[len(events)-1] }

		DescribeTable("each ends in a terminal error event and no finish event",
			func(body string) {
				run(body)
				Expect(last().Kind).To(Equal(provider.StreamEventError))
			},
			Entry("an SSE error event", part+`data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`+"\n\n"),
			Entry("a stream cut before message_stop", part),
			Entry("an empty body", ""),
			Entry("a non-event-stream body", "<html>bad gateway</html>"),
		)

		DescribeTable("none emits a finish event",
			func(body string) {
				run(body)
				Expect(events).NotTo(ContainElement(HaveField("Kind", provider.StreamEventFinish)))
			},
			Entry("an SSE error event", part+`data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`+"\n\n"),
			Entry("a stream cut before message_stop", part),
			Entry("an empty body", ""),
		)

		It("names the SSE error type and message under the anthropic prefix", func() {
			run(part + `data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}` + "\n\n")
			Expect(last().Err).To(MatchError("anthropic: stream error overloaded_error: Overloaded"))
		})

		It("redacts a key echoed in an SSE error event", func() {
			run(`data: {"type":"error","error":{"type":"` + key + `","message":"` + key + `"}}` + "\n\n")
			Expect(last().Err).To(MatchError(Not(ContainSubstring(key))))
		})

		It("says a truncated stream ended early", func() {
			run(part)
			Expect(last().Err).To(MatchError(ContainSubstring("stream ended early")))
		})

		It("keeps a truncated stream matchable as zai.ErrStreamEnded", func() {
			run(part)
			Expect(errors.Is(last().Err, zai.ErrStreamEnded)).To(BeTrue())
		})

		It("does not leak the inner zai prefix in a stream error", func() {
			run(part)
			Expect(strings.Contains(last().Err.Error(), "zai")).To(BeFalse())
		})

		DescribeTable("the finish reason follows stop_reason",
			func(stopReason, want string) {
				delta := ""
				if stopReason != "" {
					delta = `data: {"type":"message_delta","delta":{"stop_reason":"` + stopReason + `"}}` + "\n\n"
				}
				run(part + delta + stop)
				Expect(last().FinishReason).To(Equal(want))
			},
			Entry("end_turn", "end_turn", "stop"),
			Entry("stop_sequence", "stop_sequence", "stop"),
			Entry("max_tokens", "max_tokens", "length"),
			Entry("tool_use", "tool_use", "tool_calls"),
			Entry("another value", "refusal", "refusal"),
			Entry("absent", "", "stop"),
		)
	})

	DescribeTable("API errors",
		func(code int, typ, msg string) {
			status = code
			reply = `{"type":"error","error":{"type":"` + typ + `","message":"` + msg + `"}}`
			_, err := chat(hello())
			Expect(err).To(MatchError(ContainSubstring(typ + ": " + msg)))
		},
		Entry("401 authentication_error", 401, "authentication_error", "invalid x-api-key"),
		Entry("429 rate_limit_error", 429, "rate_limit_error", "slow down"),
		Entry("529 overloaded_error", 529, "overloaded_error", "Overloaded"),
	)

	It("surfaces a streamed API error as a terminal error event", func() {
		status = 529
		reply = `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`
		var last provider.StreamEvent
		for ev := range p.Stream(context.Background(), hello()) {
			last = ev
		}
		Expect(last.Err).To(MatchError(ContainSubstring("overloaded_error: Overloaded")))
	})

	It("redacts a key echoed in an error message", func() {
		status = 401
		reply = `{"error":{"type":"authentication_error","message":"bad key ` + key + `"}}`
		_, err := chat(hello())
		Expect(err).To(MatchError(Not(ContainSubstring(key))))
	})

	It("redacts a key echoed in an error type", func() {
		status = 401
		reply = `{"error":{"type":"` + key + `","message":"nope"}}`
		_, err := chat(hello())
		Expect(err).To(MatchError(Not(ContainSubstring(key))))
	})

	It("redacts a key that appears in a transport error", func() {
		srv.Close()
		_, err := anthropic.NewAPIKey(key, srv.URL+"/"+key, nil).Chat(context.Background(), hello())
		Expect(err).To(MatchError(Not(ContainSubstring(key))))
	})

	It("redacts a key that appears in a cancellation error", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := anthropic.NewAPIKey(key, srv.URL+"/"+key, nil).Chat(ctx, hello())
		Expect(err).To(MatchError(Not(ContainSubstring(key))))
	})

	It("keeps a cancellation matchable as context.Canceled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := p.Chat(ctx, hello())
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
	})

	It("does not leak the inner zai prefix in a cancellation error", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := p.Chat(ctx, hello())
		Expect(err).To(MatchError(Not(ContainSubstring("zai"))))
	})

	It("does not leak the inner zai prefix in a transport error", func() {
		srv.Close()
		_, err := chat(hello())
		Expect(err).To(MatchError(Not(ContainSubstring("zai"))))
	})

	It("keeps an authentication failure matchable as zai.ErrUnauthorized", func() {
		status = 401
		reply = `{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`
		_, err := chat(hello())
		Expect(errors.Is(err, zai.ErrUnauthorized)).To(BeTrue())
	})

	Describe("redirects", func() {
		var target atomic.Int32
		var err error

		BeforeEach(func() {
			target.Store(0)
			other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { target.Add(1) }))
			DeferCleanup(other.Close)
			first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, other.URL+"/v1/messages", http.StatusTemporaryRedirect)
			}))
			DeferCleanup(first.Close)
			_, err = anthropic.NewAPIKey(key, first.URL, nil).Chat(context.Background(), hello())
		})

		It("surfaces a redirect as an API error", func() {
			Expect(err).To(MatchError(ContainSubstring("api error 307")))
		})

		It("never sends a request to the redirect target", func() {
			Expect(target.Load()).To(BeZero())
		})
	})

	It("names the provider as anthropic in errors", func() {
		status = 401
		reply = `{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`
		_, err := chat(hello())
		Expect(err).To(MatchError(HavePrefix("anthropic: api error 401")))
	})

	It("defaults to the Anthropic API host", func() {
		Expect(anthropic.DefaultAPIBaseURL).To(Equal("https://api.anthropic.com"))
	})
})
