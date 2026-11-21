package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
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

	toolRequest := func() *provider.ChatRequest {
		req := hello()
		req.Tools = []provider.ToolDefinition{{Name: "t", Parameters: json.RawMessage(`{}`)}}
		return req
	}

	It("refuses structured tools with ErrUnsupportedTools, as the oauth route does", func() {
		_, err := chat(toolRequest())
		Expect(errors.Is(err, anthropic.ErrUnsupportedTools)).To(BeTrue())
	})

	It("refuses structured tools on a stream with ErrUnsupportedTools", func() {
		var last provider.StreamEvent
		for ev := range p.Stream(context.Background(), toolRequest()) {
			last = ev
		}
		Expect(errors.Is(last.Err, anthropic.ErrUnsupportedTools)).To(BeTrue())
	})

	It("sends no request for a rejected tool call", func() {
		req := hello()
		req.Tools = []provider.ToolDefinition{{Name: "t", Parameters: json.RawMessage(`{}`)}}
		_, _ = chat(req)
		Expect(seen).To(BeEmpty())
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
			Entry("another value", "tool_use", "tool_use"),
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
