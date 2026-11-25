package zai_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/zai"
)

func TestZai(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Zai Provider Suite")
}

func newTestServer(handler http.HandlerFunc) (*httptest.Server, *zai.Provider) {
	srv := httptest.NewServer(handler)
	DeferCleanup(srv.Close)
	p := zai.New("test-key", zai.WithBaseURL(srv.URL))
	return srv, p
}

// collectStream drains a stream into its concatenated content, panicking on
// any event error so fixture failures surface loudly during arrangement.
func collectStream(p *zai.Provider, req *provider.ChatRequest) string {
	var sb strings.Builder
	for ev := range p.Stream(context.Background(), req) {
		if ev.Err != nil {
			panic(ev.Err)
		}
		sb.WriteString(ev.Content)
	}
	return sb.String()
}

// firstStreamErr returns the first error observed on a stream, or nil.
func firstStreamErr(p *zai.Provider, req *provider.ChatRequest) error {
	var gotErr error
	for ev := range p.Stream(context.Background(), req) {
		if ev.Err != nil {
			gotErr = ev.Err
		}
	}
	return gotErr
}

func chat(p *zai.Provider) (*provider.ChatResponse, error) {
	return p.Chat(context.Background(), &provider.ChatRequest{Model: "glm-5.3"})
}

var _ = Describe("Zai provider", func() {
	It("rejects construction from env when the api key is missing", func() {
		GinkgoT().Setenv("ZAI_API_KEY", "")
		_, err := zai.NewFromEnv()
		Expect(err).To(MatchError("zai: ZAI_API_KEY is not set"))
	})

	It("constructs from env and reports no error, satisfying the provider port", func() {
		GinkgoT().Setenv("ZAI_API_KEY", "k")
		p, err := zai.NewFromEnv()
		Expect(err).NotTo(HaveOccurred())
		var _ provider.Provider = p
		Expect(p).NotTo(BeNil())
	})

	It("reports the z.ai identity", func() {
		_, p := newTestServer(func(http.ResponseWriter, *http.Request) {})
		var _ provider.Provider = p
		Expect(p.Name()).To(Equal("z.ai"))
	})

	It("exposes a non-empty model list", func() {
		_, p := newTestServer(func(http.ResponseWriter, *http.Request) {})
		var _ provider.Provider = p
		Expect(p.Models()).NotTo(BeEmpty())
	})

	Context("sends auth, model and body to chat completions and parses content", func() {
		var observedContent string
		var observedAuthorization string
		var observedCompletionPath string
		var observedModelBody string
		BeforeEach(func() {
			var gotAuth, gotPathOK, gotBodyOK string
			_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				if strings.HasSuffix(r.URL.Path, "/chat/completions") {
					gotPathOK = "path-ok"
				}
				b, _ := io.ReadAll(r.Body)
				if strings.Contains(string(b), `"model":"glm-5.3"`) {
					gotBodyOK = "body-ok"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":      "c1",
					"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "hello there"}}},
				})
			})
			resp, err := p.Chat(context.Background(), &provider.ChatRequest{
				Model:    "glm-5.3",
				Messages: []provider.Message{{Role: "user", Content: "hi"}},
			})
			Expect(err).NotTo(HaveOccurred())
			observedContent = resp.Content
			observedAuthorization = gotAuth
			observedCompletionPath = gotPathOK
			observedModelBody = gotBodyOK
		})
		It("returns assistant content", func() { Expect(observedContent).To(Equal("hello there")) })
		It("sends the bearer authorization header", func() { Expect(observedAuthorization).To(Equal("Bearer test-key")) })
		It("uses the chat completions endpoint", func() { Expect(observedCompletionPath).To(Equal("path-ok")) })
		It("sends the requested model", func() { Expect(observedModelBody).To(Equal("body-ok")) })
	})

	Context("wraps api failures in the typed APIError and the unauthorized sentinel", func() {
		var observedIsAPIError bool
		var observedIsUnauthorized bool
		BeforeEach(func() {
			_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"code":"1002","message":"invalid api key"}}`))
			})
			_, err := chat(p)
			var apiErr *zai.APIError
			observedIsAPIError = errors.As(err, &apiErr)
			observedIsUnauthorized = errors.Is(err, zai.ErrUnauthorized)
		})
		It("returns a typed API error", func() { Expect(observedIsAPIError).To(BeTrue()) })
		It("wraps the unauthorized sentinel", func() { Expect(observedIsUnauthorized).To(BeTrue()) })
	})

	Context("populates APIError status, code and message from the api response", func() {
		var observedStatus int
		var observedCode string
		var observedMessage string
		BeforeEach(func() {
			_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"code":"1002","message":"invalid api key"}}`))
			})
			_, err := chat(p)
			var apiErr *zai.APIError
			if !errors.As(err, &apiErr) {
				panic("expected *zai.APIError")
			}
			observedStatus = apiErr.Status
			observedCode = apiErr.Code
			observedMessage = apiErr.Message
		})
		It("records the HTTP status", func() { Expect(observedStatus).To(Equal(http.StatusUnauthorized)) })
		It("records the API error code", func() { Expect(observedCode).To(Equal("1002")) })
		It("records the API error message", func() { Expect(observedMessage).To(Equal("invalid api key")) })
	})

	Context("does not silently retry failed chat calls", func() {
		var observedFailed bool
		var observedCallCount int
		BeforeEach(func() {
			calls := 0
			_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"boom"}}`))
			})
			_, err := chat(p)
			observedFailed = err != nil
			observedCallCount = calls
		})
		It("returns an error", func() { Expect(observedFailed).To(BeTrue()) })
		It("makes exactly one HTTP request", func() { Expect(observedCallCount).To(Equal(1)) })
	})

	Context("requests event streaming when streaming chat deltas", func() {
		var observedAcceptsEvents bool
		var observedContent string
		BeforeEach(func() {
			var accept string
			_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
				accept = r.Header.Get("Accept")
				w.Header().Set("Content-Type", "text/event-stream")
				fl := w.(http.Flusher)
				for _, chunk := range []string{
					`data: {"choices":[{"delta":{"content":"he"}}]}`,
					`data: {"choices":[{"delta":{"content":"llo"}}]}`,
					`data: [DONE]`,
				} {
					_, _ = w.Write([]byte(chunk + "\n\n"))
					fl.Flush()
				}
			})
			content := collectStream(p, &provider.ChatRequest{Model: "glm-5.3"})
			observedAcceptsEvents = strings.Contains(accept, "text/event-stream")
			observedContent = content
		})
		It("requests server sent events", func() { Expect(observedAcceptsEvents).To(BeTrue()) })
		It("joins the streamed content", func() { Expect(observedContent).To(Equal("hello")) })
	})

	It("surfaces typed errors from a failed stream", func() {
		_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"1302","message":"rate limited"}}`))
		})
		gotErr := firstStreamErr(p, &provider.ChatRequest{Model: "glm-5.3"})
		var apiErr *zai.APIError
		if !errors.As(gotErr, &apiErr) {
			panic("expected *zai.APIError from stream")
		}
		Expect(apiErr.Status).To(Equal(http.StatusTooManyRequests))
	})

	It("stops streaming when the context is cancelled", func() {
		release := make(chan struct{})
		_, p := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			<-release
		})
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		done := make(chan struct{})
		go func() {
			for range p.Stream(ctx, &provider.ChatRequest{Model: "glm-5.3"}) {
			}
			close(done)
		}()
		cancel()
		Eventually(done).WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(BeClosed())
		close(release)
	})
})

var _ = Describe("max_tokens passthrough", func() {
	It("sends max_tokens in the request body", func() {
		var got float64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				panic(err)
			}
			var req struct {
				MaxTokens float64 `json:"max_tokens"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				panic(err)
			}
			got = req.MaxTokens
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		}))
		DeferCleanup(srv.Close)
		p := zai.New("k", zai.WithBaseURL(srv.URL))
		for range p.Stream(context.Background(), &provider.ChatRequest{Model: "glm-5.3", MaxTokens: 42}) {
		}
		Expect(got).To(Equal(42.0))
	})
	DescribeTable("sends the resolved max_tokens on the general route", func(in int64, want float64) {
		var got float64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var req struct {
				MaxTokens float64 `json:"max_tokens"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				panic(err)
			}
			got = req.MaxTokens
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		}))
		DeferCleanup(srv.Close)
		p := zai.New("k", zai.WithBaseURL(srv.URL))
		for range p.Stream(context.Background(), &provider.ChatRequest{Model: "glm-5.3", MaxTokens: in}) {
		}
		Expect(got).To(Equal(want))
	},
		Entry("unset uses the default", int64(0), 8192.0),
		Entry("negative uses the default", int64(-5), 8192.0),
		Entry("explicit small limit is sent as given", int64(20), 20.0),
		Entry("explicit limit above the cap is capped", int64(500000), 131072.0),
		Entry("lower boundary 1 is sent as given", int64(1), 1.0),
		Entry("upper boundary 131072 is sent as given", int64(131072), 131072.0),
	)
})

var _ = Describe("general route stream finish reason", func() {
	const text = `data: {"choices":[{"delta":{"content":"hi"}}]}` + "\n"
	stream := func(sse string, opts ...zai.Option) []provider.StreamEvent {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, sse)
		}))
		DeferCleanup(srv.Close)
		p := zai.New("test-key", append([]zai.Option{zai.WithBaseURL(srv.URL), zai.WithFinishEvents()}, opts...)...)
		var events []provider.StreamEvent
		for ev := range p.Stream(context.Background(), &provider.ChatRequest{}) {
			events = append(events, ev)
		}
		return events
	}
	finishes := func(events []provider.StreamEvent) []provider.StreamEvent {
		var out []provider.StreamEvent
		for _, ev := range events {
			if ev.Kind == provider.StreamEventFinish {
				out = append(out, ev)
			}
		}
		return out
	}
	DescribeTable("maps the wire finish_reason",
		func(wire, want string) {
			events := stream(text + `data: {"choices":[{"delta":{},"finish_reason":"` + wire + `"}]}` + "\ndata: [DONE]\n")
			Expect(events[len(events)-1]).To(Equal(provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: want}))
		},
		Entry("stop", "stop", "stop"),
		Entry("length", "length", "length"),
		Entry("another value", "content_filter", "content_filter"),
	)
	DescribeTable("keeps a length reason sticky across choices and chunks",
		func(sse string) {
			events := stream(sse + "data: [DONE]\n")
			Expect(events[len(events)-1].FinishReason).To(Equal("length"))
		},
		Entry("choice 0 length then choice 1 stop in one chunk",
			`data: {"choices":[{"index":0,"delta":{},"finish_reason":"length"},{"index":1,"delta":{},"finish_reason":"stop"}]}`+"\n"),
		Entry("length then stop in later chunks",
			`data: {"choices":[{"delta":{},"finish_reason":"length"}]}`+"\n"+`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n"),
		Entry("stop then length in later chunks",
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n"+`data: {"choices":[{"delta":{},"finish_reason":"length"}]}`+"\n"),
	)
	It("yields one finish at [DONE] and all text when finish_reason precedes more content", func() {
		events := stream(`data: {"choices":[{"delta":{"content":"a"},"finish_reason":"length"}]}` + "\n" +
			`data: {"choices":[{"delta":{"content":"b"}}]}` + "\ndata: [DONE]\n")
		Expect(finishes(events)).To(Equal([]provider.StreamEvent{{Kind: provider.StreamEventFinish, FinishReason: "length"}}))
	})
	It("delivers all the text when finish_reason precedes more content", func() {
		events := stream(`data: {"choices":[{"delta":{"content":"a"},"finish_reason":"length"}]}` + "\n" +
			`data: {"choices":[{"delta":{"content":"b"}}]}` + "\ndata: [DONE]\n")
		var got string
		for _, ev := range events {
			got += ev.Content
		}
		Expect(got).To(Equal("ab"))
	})
	It("finishes with stop when every chunk has a null finish_reason", func() {
		events := stream(`data: {"choices":[{"delta":{"content":"a"},"finish_reason":null}]}` + "\n" +
			`data: {"choices":[{"delta":{"content":"b"},"finish_reason":null}]}` + "\ndata: [DONE]\n")
		Expect(events[len(events)-1].FinishReason).To(Equal("stop"))
	})
	It("ignores a usage-only trailing chunk and adds no event", func() {
		events := stream(text + `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n" +
			`data: {"choices":[],"usage":{"total_tokens":3}}` + "\ndata: [DONE]\n")
		Expect(events).To(HaveLen(2))
	})
	It("emits exactly one finish event when [DONE] is sent twice", func() {
		events := stream(text + "data: [DONE]\ndata: [DONE]\n")
		Expect(finishes(events)).To(HaveLen(1))
	})
	It("finishes with stop when [DONE] arrives without a finish_reason", func() {
		events := stream(text + "data: [DONE]\n")
		Expect(events[len(events)-1].FinishReason).To(Equal("stop"))
	})
	It("emits no finish event by default", func() {
		_, p := newTestServer(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, text+"data: [DONE]\n")
		})
		var kinds []provider.StreamEventKind
		for ev := range p.Stream(context.Background(), &provider.ChatRequest{}) {
			kinds = append(kinds, ev.Kind)
		}
		Expect(kinds).NotTo(ContainElement(provider.StreamEventFinish))
	})
	It("emits exactly one finish event", func() {
		events := stream(text + `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\ndata: [DONE]\n")
		Expect(finishes(events)).To(HaveLen(1))
	})
	It("emits no finish event after an error event", func() {
		Expect(finishes(stream(text + "data: {oops}\ndata: [DONE]\n"))).To(BeEmpty())
	})
	It("passes the provider stream validator", func() {
		validator := &provider.StreamValidator{}
		var rejected error
		for _, ev := range stream(text + `data: {"choices":[{"delta":{},"finish_reason":"length"}]}` + "\ndata: [DONE]\n") {
			if err := validator.Accept(ev); err != nil {
				rejected = err
			}
		}
		Expect(rejected).NotTo(HaveOccurred())
	})
})

// overflowRoutes are the three delivery routes of the adapter, each with the error body shape
// it decodes.
var overflowRoutes = map[string]func(base string) *zai.Provider{
	"plain": func(base string) *zai.Provider { return zai.New("k", zai.WithBaseURL(base)) },
	"coding": func(base string) *zai.Provider {
		return zai.New("k", zai.WithBaseURL(base), zai.WithCoding())
	},
	"anthropic": func(base string) *zai.Provider {
		return zai.New("k", zai.WithBaseURL(base), zai.WithCoding(), zai.WithCodingRoute("anthropic"))
	},
}

func overflowFailure(route string, status int, code, message string) error {
	body := fmt.Sprintf(`{"error":{"code":%q,"message":%q}}`, code, message)
	if route == "anthropic" {
		body = fmt.Sprintf(`{"type":"error","error":{"type":%q,"message":%q}}`, code, message)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	DeferCleanup(srv.Close)
	_, err := overflowRoutes[route](srv.URL).Chat(context.Background(), &provider.ChatRequest{})
	return err
}

var _ = Describe("Context overflow classification", func() {
	DescribeTable("a failure that is an overflow",
		func(route string, status int, code, message string) {
			err := overflowFailure(route, status, code, message)
			Expect(err).To(MatchError(provider.ErrContextOverflow))
		},
		Entry("the plain route, code 1261", "plain", 400, "1261", "Prompt exceeds max length"),
		Entry("the coding route, code 1261", "coding", 400, "1261", "Prompt exceeds max length"),
		Entry("the anthropic route, prompt is too long", "anthropic", 400, "invalid_request_error",
			"prompt is too long: 250000 tokens > 200000 maximum"),
		Entry("the coding route, an invalid_request_error that says prompt is too long", "coding", 400,
			"invalid_request_error", "prompt is too long: 9 tokens > 8 maximum"),
		Entry("the plain route, HTTP 413", "plain", 413, "", "too big"),
		Entry("the anthropic route, HTTP 413", "anthropic", 413, "request_too_large", "Request exceeds the maximum size"),
	)

	DescribeTable("a failure that is not an overflow",
		func(route string, status int, code, message string) {
			err := overflowFailure(route, status, code, message)
			Expect(err).NotTo(MatchError(provider.ErrContextOverflow))
		},
		Entry("another 400 on the plain route", "plain", 400, "1210", "invalid parameters"),
		Entry("another invalid_request_error on the anthropic route", "anthropic", 400, "invalid_request_error",
			"max_tokens must be positive"),
		Entry("a message that only contains prompt is too long", "anthropic", 400, "invalid_request_error",
			"your prompt is too long"),
		Entry("a broad limit message", "plain", 400, "1234", "rate limit exceeds the context"),
		Entry("code 1261 on a status that is not 400", "plain", 500, "1261", "Prompt exceeds max length"),
		Entry("a prompt is too long message on a 500", "anthropic", 500, "invalid_request_error", "prompt is too long"),
		Entry("a 429", "coding", 429, "1302", "rate limited"),
		Entry("a 500", "anthropic", 500, "api_error", "internal error"),
	)

	It("classifies a streamed failure the same way", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"1261","message":"Prompt exceeds max length"}}`))
		}))
		DeferCleanup(srv.Close)
		var got error
		for ev := range zai.New("k", zai.WithBaseURL(srv.URL), zai.WithCoding()).Stream(context.Background(), &provider.ChatRequest{}) {
			if ev.Err != nil {
				got = ev.Err
			}
		}
		Expect(got).To(MatchError(provider.ErrContextOverflow))
	})
})
