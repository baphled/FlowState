package zai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
)

// codingServer serves body at POST */v1/messages, capturing either
// the x-api-key header or raw request bodies.
func codingServer(body, capture string) (*httptest.Server, func() string) {
	var captured atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture == "x-api-key" {
			captured.Store(r.Header.Get("x-api-key"))
		} else {
			b, _ := io.ReadAll(r.Body)
			captured.Store(string(b))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	return srv, func() string { v, _ := captured.Load().(string); return v }
}

var _ = Describe("coding (Anthropic-compatible) route", func() {
	It("chats via the anthropic messages shape", func() {
		srv, _ := codingServer(`{"content":[{"type":"text","text":"hello"}]}`, "")
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding(), WithCodingRoute("anthropic"))
		resp, err := p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Content).To(Equal("hello"))
	})

	It("sends the anthropic request shape", func() {
		srv, bodies := codingServer(`{"content":[]}`, "")
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding(), WithCodingRoute("anthropic"))
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(bodies()).To(ContainSubstring(`"max_tokens"`))
	})

	It("sends x-api-key and anthropic-version headers", func() {
		srv, hdrs := codingServer(`{"content":[]}`, "x-api-key")
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding(), WithCodingRoute("anthropic"))
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(hdrs()).To(Equal("k"))
	})

	It("emits text deltas from content_block_delta events", func() {
		sse := "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"he\"}}\n" +
			"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"y\"}}\n" +
			"data: {\"type\":\"message_stop\"}\n\n"
		srv, _ := codingServer(sse, "")
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding(), WithCodingRoute("anthropic"))
		var got string
		for ev := range p.Stream(context.Background(), &provider.ChatRequest{}) {
			got += ev.Content
		}
		Expect(got).To(Equal("hey"))
	})

	It("stops streaming at message_stop", func() {
		srv, _ := codingServer("data: {\"type\":\"message_stop\"}\n\n", "")
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding(), WithCodingRoute("anthropic"))
		var n int
		for range p.Stream(context.Background(), &provider.ChatRequest{}) {
			n++
		}
		Expect(n).To(Equal(0))
	})

	It("uses the anthropic coding endpoint URL when no base URL is overridden", func() {
		var hitPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hitPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"content":[]}`)
		}))
		defer srv.Close()
		// Redirect the anthropic endpoint to the test server.
		old := codingBaseURL
		codingBaseURL = srv.URL + "/api/anthropic"
		defer func() { codingBaseURL = old }()
		p := New("k", WithCoding(), WithCodingRoute("anthropic"))
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(hitPath).To(Equal("/api/anthropic/v1/messages"))
	})

	DescribeTable("sends the resolved max_tokens on the anthropic coding route", func(in int64, want string) {
		srv, bodies := codingServer(`{"content":[]}`, "")
		defer srv.Close()
		p := New("k", WithBaseURL(srv.URL), WithCoding(), WithCodingRoute("anthropic"))
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{MaxTokens: in})
		Expect(bodies()).To(ContainSubstring(want))
	},
		Entry("unset uses the default", int64(0), `"max_tokens":8192`),
		Entry("negative uses the default", int64(-5), `"max_tokens":8192`),
		Entry("explicit small limit is sent as given", int64(20), `"max_tokens":20`),
		Entry("explicit limit above the cap is capped", int64(500000), `"max_tokens":131072`),
	)
})

var _ = Describe("ClampMaxTokens", func() {
	DescribeTable("resolves the limit", func(in, want int64) {
		Expect(ClampMaxTokens(in)).To(Equal(want))
	},
		Entry("unset becomes the default", int64(0), int64(MaxTokensDefault)),
		Entry("negative becomes the default", int64(-5), int64(MaxTokensDefault)),
		Entry("an explicit limit below the default is kept", int64(20), int64(20)),
		Entry("the smallest explicit limit is kept", int64(1), int64(1)),
		Entry("an in-range limit is kept", int64(9000), int64(9000)),
		Entry("the exact maximum is kept", int64(131072), int64(131072)),
		Entry("a limit above the maximum is capped", int64(131073), int64(131072)),
	)
})

var _ = Describe("WithCoding base URL", func() {
	It("honours an explicit base URL override over the coding endpoint", func() {
		var hitPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hitPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"content":[]}`)
		}))
		defer srv.Close()
		old := codingBaseURL
		codingBaseURL = "https://zai-invalid.example.invalid"
		defer func() { codingBaseURL = old }()
		p := New("k", WithBaseURL(srv.URL), WithCoding(), WithCodingRoute("anthropic"))
		_, _ = p.Chat(context.Background(), &provider.ChatRequest{})
		Expect(hitPath).To(Equal("/v1/messages"))
	})
})

// anthropicEvents streams one hello turn from handler on the anthropic route
// and returns every event.
func anthropicEvents(handler http.HandlerFunc, opts ...Option) []provider.StreamEvent {
	srv := httptest.NewServer(handler)
	defer srv.Close()
	all := append([]Option{WithBaseURL(srv.URL), WithCoding(), WithCodingRoute("anthropic")}, opts...)
	req := &provider.ChatRequest{Messages: []provider.Message{{Role: "user", Content: "hi"}}}
	var evs []provider.StreamEvent
	for ev := range New("k", all...).Stream(context.Background(), req) {
		evs = append(evs, ev)
	}
	return evs
}

// sseBody serves body as a 200 response.
func sseBody(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}
}

const textPar = "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"par\"}}\n\n"

var _ = Describe("anthropic route stream termination", func() {
	var events []provider.StreamEvent
	last := func() provider.StreamEvent { return events[len(events)-1] }

	Describe("an SSE error event", func() {
		BeforeEach(func() {
			events = anthropicEvents(sseBody(textPar +
				"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n" +
				"data: {\"type\":\"message_stop\"}\n\n"))
		})

		It("keeps the text sent before it", func() {
			Expect(events[0].Content).To(Equal("par"))
		})

		It("ends the stream with a terminal error event", func() {
			Expect(last().Kind).To(Equal(provider.StreamEventError))
		})

		It("carries the error type", func() {
			Expect(last().Err).To(MatchError(ContainSubstring("overloaded_error")))
		})

		It("carries the error message", func() {
			Expect(last().Err).To(MatchError(ContainSubstring("Overloaded")))
		})

		It("emits nothing after the error", func() {
			Expect(events).To(HaveLen(2))
		})
	})

	It("turns a body read error into a terminal error", func() {
		events = anthropicEvents(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100000")
			_, _ = io.WriteString(w, textPar)
		})
		Expect(last().Err).To(MatchError(ContainSubstring("read stream")))
	})

	DescribeTable("a stream that ends before message_stop is a terminal error",
		func(body string) {
			events = anthropicEvents(sseBody(body))
			Expect(last().Err).To(MatchError(ErrStreamEnded))
		},
		Entry("after text", textPar),
		Entry("with an empty body", ""),
		Entry("with an HTML body", "<html><body>gateway</body></html>"),
		Entry("with a final line lacking a newline", "data: {\"type\":\"ping\"}"),
	)

	It("ignores ping and unknown event types", func() {
		events = anthropicEvents(sseBody("data: {\"type\":\"ping\"}\n\ndata: {\"type\":\"future_event\"}\n\n" +
			textPar + "data: {\"type\":\"message_stop\"}\n\n"))
		Expect(events).To(HaveLen(1))
	})

	It("emits no finish event by default, as the z.ai route always did", func() {
		events = anthropicEvents(sseBody(textPar + "data: {\"type\":\"message_stop\"}\n\n"))
		Expect(last().Kind).NotTo(Equal(provider.StreamEventFinish))
	})

	It("emits a finish event when WithFinishEvents is set", func() {
		events = anthropicEvents(sseBody(textPar+"data: {\"type\":\"message_stop\"}\n\n"), WithFinishEvents())
		Expect(last().Kind).To(Equal(provider.StreamEventFinish))
	})

	It("emits no finish event for a truncated stream when finish events are on", func() {
		events = anthropicEvents(sseBody(textPar), WithAnthropicFinish())
		Expect(last().Kind).To(Equal(provider.StreamEventError))
	})

	DescribeTable("the finish reason maps the stop_reason",
		func(delta, want string) {
			events = anthropicEvents(sseBody(textPar+delta+"data: {\"type\":\"message_stop\"}\n\n"), WithAnthropicFinish())
			Expect(last().FinishReason).To(Equal(want))
		},
		Entry("end_turn", "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n", "stop"),
		Entry("stop_sequence", "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"stop_sequence\"}}\n\n", "stop"),
		Entry("max_tokens", "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\n", "length"),
		Entry("another value", "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"refusal\"}}\n\n", "refusal"),
		Entry("no message_delta", "", "stop"),
	)

	It("marks the finish event as a finish", func() {
		events = anthropicEvents(sseBody(textPar+"data: {\"type\":\"message_stop\"}\n\n"), WithAnthropicFinish())
		Expect(last().Kind).To(Equal(provider.StreamEventFinish))
	})
})

var _ = Describe("WithAnthropicSystem", func() {
	var (
		bodies func() string
		opts   []Option
		msgs   []provider.Message
	)
	field := func(name string) any {
		var m map[string]any
		if err := json.Unmarshal([]byte(bodies()), &m); err != nil {
			panic(err)
		}
		return m[name]
	}
	chat := func() error {
		srv, b := codingServer(`{"content":[]}`, "")
		DeferCleanup(srv.Close)
		bodies = b
		all := append([]Option{WithBaseURL(srv.URL), WithCoding(), WithCodingRoute("anthropic")}, opts...)
		_, err := New("k", all...).Chat(context.Background(), &provider.ChatRequest{Messages: msgs})
		return err
	}

	BeforeEach(func() {
		opts = nil
		msgs = []provider.Message{
			{Role: "system", Content: "one"}, {Role: "user", Content: "hi"}, {Role: "system", Content: "two"}}
	})

	Describe("by default", func() {
		BeforeEach(func() { _ = chat() })

		It("sends no top-level system field", func() {
			Expect(field("system")).To(BeNil())
		})

		It("leaves system messages in messages", func() {
			Expect(field("messages")).To(HaveLen(3))
		})
	})

	Describe("when on", func() {
		BeforeEach(func() {
			opts = []Option{WithAnthropicSystem()}
			_ = chat()
		})

		It("joins the system messages in order with a blank line", func() {
			Expect(field("system")).To(Equal("one\n\ntwo"))
		})

		It("removes them from messages", func() {
			Expect(field("messages")).To(HaveLen(1))
		})
	})

	Describe("with a system-only request", func() {
		var calls int32
		var err error

		BeforeEach(func() {
			calls = 0
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				atomic.AddInt32(&calls, 1)
			}))
			DeferCleanup(srv.Close)
			req := &provider.ChatRequest{Messages: []provider.Message{{Role: "system", Content: "only"}}}
			p := New("k", WithBaseURL(srv.URL), WithCoding(), WithCodingRoute("anthropic"), WithAnthropicSystem())
			_, err = p.Chat(context.Background(), req)
		})

		It("refuses it with a clear error", func() {
			Expect(err).To(MatchError(ErrSystemOnly))
		})

		It("sends no request", func() {
			Expect(atomic.LoadInt32(&calls)).To(BeZero())
		})
	})
})
