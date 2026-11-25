package zai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
)

// usageServe serves body to every request and returns a provider on opts.
func usageServe(body string, opts ...Option) *Provider {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	DeferCleanup(srv.Close)
	return New("k", append([]Option{WithBaseURL(srv.URL), WithFinishEvents()}, opts...)...)
}

func usageStream(p *Provider, req *provider.ChatRequest) []provider.StreamEvent {
	var events []provider.StreamEvent
	for ev := range p.Stream(context.Background(), req) {
		events = append(events, ev)
	}
	return events
}

func streamErr(events []provider.StreamEvent) error {
	for _, ev := range events {
		if ev.Err != nil {
			return ev.Err
		}
	}
	return nil
}

const (
	stopChunk  = `data: {"choices":[{"delta":{"content":"hi"},"finish_reason":"stop"}]}` + "\n"
	usageChunk = `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,` +
		`"prompt_tokens_details":{"cached_tokens":30}}}` + "\n"
	anthropicStart = `data: {"type":"message_start","message":{"usage":{"input_tokens":25,` +
		`"cache_creation_input_tokens":7,"cache_read_input_tokens":11,"output_tokens":1}}}` + "\n\n"
	anthropicDelta = `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42}}` + "\n\n"
	anthropicStop  = `data: {"type":"message_stop"}` + "\n\n"
)

var _ = Describe("provider usage on the OpenAI-style routes", func() {
	var plainReq, toolReq *provider.ChatRequest

	BeforeEach(func() { plainReq, toolReq = &provider.ChatRequest{}, toolRequest() })

	routes := func(body string) map[string]func() []provider.StreamEvent {
		return map[string]func() []provider.StreamEvent{
			"general": func() []provider.StreamEvent { return usageStream(usageServe(body), plainReq) },
			"coding":  func() []provider.StreamEvent { return usageStream(usageServe(body, WithCoding()), plainReq) },
			"coding with tools": func() []provider.StreamEvent {
				return usageStream(usageServe(body, WithCoding()), toolReq)
			},
		}
	}

	DescribeTable("a trailing usage chunk lands on the finish event, cache split from input",
		func(route string) {
			events := routes(stopChunk + usageChunk + done)[route]()
			Expect(events[len(events)-1].Usage).To(Equal(&provider.Usage{InputTokens: 70, OutputTokens: 20, CacheReadTokens: 30}))
		},
		Entry("general route", "general"),
		Entry("coding route", "coding"),
		Entry("coding route with tools", "coding with tools"),
	)

	DescribeTable("a stream without usage reports none, not zeros",
		func(route string) {
			events := routes(stopChunk + done)[route]()
			Expect(events[len(events)-1].Usage).To(BeNil())
		},
		Entry("general route", "general"),
		Entry("coding route", "coding"),
		Entry("coding route with tools", "coding with tools"),
	)

	DescribeTable("a null usage is not reported",
		func(route string) {
			events := routes(`data: {"choices":[{"delta":{"content":"hi"},"finish_reason":"stop"}],"usage":null}` + "\n" + done)[route]()
			Expect(events[len(events)-1].Usage).To(BeNil())
		},
		Entry("general route", "general"),
		Entry("coding route with tools", "coding with tools"),
	)

	DescribeTable("usage that no provider can report ends the stream with an error",
		func(route, usage string) {
			events := routes(stopChunk + `data: {"choices":[],"usage":` + usage + "}\n" + done)[route]()
			Expect(streamErr(events)).To(HaveOccurred())
		},
		Entry("negative prompt on the general route", "general", `{"prompt_tokens":-1,"completion_tokens":1}`),
		Entry("negative completion on the coding route", "coding", `{"prompt_tokens":1,"completion_tokens":-5}`),
		Entry("string count on the general route", "general", `{"prompt_tokens":"12","completion_tokens":1}`),
		Entry("string count on the coding route with tools", "coding with tools", `{"prompt_tokens":"12"}`),
		Entry("count beyond int64", "general", `{"prompt_tokens":99999999999999999999}`),
		Entry("count above the cap", "coding", `{"prompt_tokens":2000000000000}`),
		Entry("cached above prompt", "coding with tools", `{"prompt_tokens":5,"prompt_tokens_details":{"cached_tokens":6}}`),
	)

	DescribeTable("repeated usage chunks report the last value, not the sum or the first",
		func(route string) {
			second := `data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":1}}` + "\n"
			events := routes(stopChunk + second + usageChunk + done)[route]()
			Expect(events[len(events)-1].Usage).To(Equal(&provider.Usage{InputTokens: 70, OutputTokens: 20, CacheReadTokens: 30}))
		},
		Entry("general route", "general"),
		Entry("coding route", "coding"),
		Entry("coding route with tools", "coding with tools"),
	)

	DescribeTable("a null usage after a usage chunk keeps the earlier usage",
		func(route string) {
			null := `data: {"choices":[],"usage":null}` + "\n"
			events := routes(stopChunk + usageChunk + null + done)[route]()
			Expect(events[len(events)-1].Usage).To(Equal(&provider.Usage{InputTokens: 70, OutputTokens: 20, CacheReadTokens: 30}))
		},
		Entry("general route", "general"),
		Entry("coding route", "coding"),
		Entry("coding route with tools", "coding with tools"),
	)

	DescribeTable("a usage object with no known count is not reported",
		func(route, usage string) {
			events := routes(stopChunk + `data: {"choices":[],"usage":` + usage + "}\n" + done)[route]()
			Expect(events[len(events)-1].Usage).To(BeNil())
		},
		Entry("empty object on the general route", "general", `{}`),
		Entry("empty object on the coding route", "coding", `{}`),
		Entry("empty object on the coding route with tools", "coding with tools", `{}`),
		Entry("empty details on the general route", "general", `{"prompt_tokens_details":{}}`),
		Entry("unknown fields only on the coding route", "coding", `{"total_tokens":5}`),
	)

	DescribeTable("reasoning tokens inside completion_tokens are not added to the output",
		func(route string) {
			body := stopChunk + `data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":200,` +
				`"completion_tokens_details":{"reasoning_tokens":150}}}` + "\n" + done
			events := routes(body)[route]()
			Expect(events[len(events)-1].Usage.OutputTokens).To(Equal(int64(200)))
		},
		Entry("general route", "general"),
		Entry("coding route", "coding"),
		Entry("coding route with tools", "coding with tools"),
	)

	DescribeTable("a present zero is a reported zero",
		func(route string) {
			events := routes(stopChunk + `data: {"choices":[],"usage":{"completion_tokens":0}}` + "\n" + done)[route]()
			Expect(events[len(events)-1].Usage).To(Equal(&provider.Usage{}))
		},
		Entry("general route", "general"),
		Entry("coding route", "coding"),
		Entry("coding route with tools", "coding with tools"),
	)

	DescribeTable("a huge number literal gives a bounded error",
		func(route string) {
			huge := strings.Repeat("9", 200000)
			events := routes(stopChunk + `data: {"choices":[],"usage":{"prompt_tokens":` + huge + "}}\n" + done)[route]()
			Expect(len(streamErr(events).Error())).To(BeNumerically("<", 200))
		},
		Entry("general route", "general"),
		Entry("coding route", "coding"),
		Entry("coding route with tools", "coding with tools"),
	)

	DescribeTable("a non-streaming reply with an empty usage object reports none",
		func(opts []Option) {
			resp, err := usageServe(`{"choices":[{"message":{"content":"x"}}],"usage":{}}`, opts...).Chat(context.Background(), plainReq)
			if err != nil {
				panic(err)
			}
			Expect(resp.Usage).To(BeNil())
		},
		Entry("general route", []Option{}),
		Entry("coding route", []Option{WithCoding()}),
	)

	It("bounds the error for a huge literal in a non-streaming reply", func() {
		body := `{"choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":` + strings.Repeat("9", 200000) + "}}"
		_, err := usageServe(body).Chat(context.Background(), plainReq)
		Expect(len(err.Error())).To(BeNumerically("<", 200))
	})

	It("turns a malformed usage into a typed protocol error on the strict route", func() {
		events := routes(stopChunk + `data: {"choices":[],"usage":{"prompt_tokens":-1}}` + "\n" + done)["coding with tools"]()
		var typed *ToolProtocolError
		Expect(streamErr(events)).To(BeAssignableToTypeOf(typed))
	})

	It("emits no finish event, so no usage, unless finish events are on", func() {
		p := usageServe(stopChunk+usageChunk+done, WithCoding())
		p.anthropicFinish = false
		events := usageStream(p, plainReq)
		Expect(events).To(HaveLen(1))
	})

	DescribeTable("a non-streaming reply reports its usage",
		func(opts []Option) {
			body := `{"choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":9,"completion_tokens":4,` +
				`"prompt_tokens_details":{"cached_tokens":2}}}`
			resp, err := usageServe(body, opts...).Chat(context.Background(), plainReq)
			if err != nil {
				panic(err)
			}
			Expect(resp.Usage).To(Equal(&provider.Usage{InputTokens: 7, OutputTokens: 4, CacheReadTokens: 2}))
		},
		Entry("general route", []Option{}),
		Entry("coding route", []Option{WithCoding()}),
	)

	It("reports no usage for a non-streaming reply without one", func() {
		resp, err := usageServe(`{"choices":[{"message":{"content":"x"}}]}`).Chat(context.Background(), plainReq)
		if err != nil {
			panic(err)
		}
		Expect(resp.Usage).To(BeNil())
	})

	It("rejects a negative usage in a non-streaming reply", func() {
		_, err := usageServe(`{"choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":-2}}`).Chat(context.Background(), plainReq)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("provider usage on the anthropic route", func() {
	var route []Option
	var req *provider.ChatRequest

	BeforeEach(func() {
		route = []Option{WithCoding(), WithCodingRoute("anthropic")}
		req = &provider.ChatRequest{Messages: []provider.Message{{Role: "user", Content: "hi"}}}
	})

	finishOf := func(body string) provider.StreamEvent {
		events := usageStream(usageServe(body, route...), req)
		return events[len(events)-1]
	}

	It("sums message_start input and the cumulative message_delta output, mapping both cache counts", func() {
		Expect(finishOf(anthropicStart + anthropicDelta + anthropicStop).Usage).To(Equal(
			&provider.Usage{InputTokens: 25, OutputTokens: 42, CacheReadTokens: 11, CacheWriteTokens: 7}))
	})

	It("keeps the message_start output when no message_delta carries usage", func() {
		Expect(finishOf(anthropicStart + anthropicStop).Usage.OutputTokens).To(Equal(int64(1)))
	})

	It("lets a message_delta that repeats the input replace it", func() {
		delta := `data: {"type":"message_delta","delta":{},"usage":{"input_tokens":26,"output_tokens":42}}` + "\n\n"
		Expect(finishOf(anthropicStart + delta + anthropicStop).Usage.InputTokens).To(Equal(int64(26)))
	})

	It("reports none for a stream without usage, not zeros", func() {
		Expect(finishOf(textPar + anthropicStop).Usage).To(BeNil())
	})

	It("reports none for a null usage", func() {
		Expect(finishOf(`data: {"type":"message_delta","delta":{},"usage":null}` + "\n\n" + anthropicStop).Usage).To(BeNil())
	})

	It("reports a usage object of zeros as reported", func() {
		Expect(finishOf(`data: {"type":"message_delta","delta":{},"usage":{"output_tokens":0}}` + "\n\n" + anthropicStop).Usage).
			To(Equal(&provider.Usage{}))
	})

	It("reports none for an empty usage object on message_delta", func() {
		Expect(finishOf(`data: {"type":"message_delta","delta":{},"usage":{}}` + "\n\n" + anthropicStop).Usage).To(BeNil())
	})

	It("reports none for an empty usage object on message_start", func() {
		Expect(finishOf(`data: {"type":"message_start","message":{"usage":{}}}` + "\n\n" + anthropicStop).Usage).To(BeNil())
	})

	It("keeps the earlier usage when a message_delta carries an empty usage object", func() {
		Expect(finishOf(anthropicStart + `data: {"type":"message_delta","delta":{},"usage":{}}` + "\n\n" + anthropicStop).Usage).
			To(Equal(&provider.Usage{InputTokens: 25, OutputTokens: 1, CacheReadTokens: 11, CacheWriteTokens: 7}))
	})

	It("reports none for a non-streaming reply with an empty usage object", func() {
		resp, err := usageServe(`{"content":[],"usage":{}}`, route...).Chat(context.Background(), req)
		if err != nil {
			panic(err)
		}
		Expect(resp.Usage).To(BeNil())
	})

	It("never lets a message_delta of zero input and cache counts lower the message_start counts", func() {
		delta := `data: {"type":"message_delta","delta":{},"usage":{"input_tokens":0,"cache_creation_input_tokens":0,` +
			`"cache_read_input_tokens":0,"output_tokens":42}}` + "\n\n"
		Expect(finishOf(anthropicStart + delta + anthropicStop).Usage).To(Equal(
			&provider.Usage{InputTokens: 25, OutputTokens: 42, CacheReadTokens: 11, CacheWriteTokens: 7}))
	})

	It("never lets a message_delta of zero output lower the message_start output", func() {
		delta := `data: {"type":"message_delta","delta":{},"usage":{"output_tokens":0}}` + "\n\n"
		Expect(finishOf(anthropicStart + delta + anthropicStop).Usage.OutputTokens).To(Equal(int64(1)))
	})

	DescribeTable("a top-level error event whose message is a string still yields the stream error",
		func(finishEvents bool) {
			body := `data: {"type":"error","message":"boom"}` + "\n\n" + anthropicStop
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			DeferCleanup(srv.Close)
			opts := append([]Option{WithBaseURL(srv.URL)}, route...)
			if finishEvents {
				opts = append(opts, WithFinishEvents())
			}
			var events []provider.StreamEvent
			for ev := range New("k", opts...).Stream(context.Background(), req) {
				events = append(events, ev)
			}
			Expect(streamErr(events)).To(MatchError(ContainSubstring("zai: stream error")))
		},
		Entry("finish events on", true),
		Entry("finish events off", false),
	)

	It("treats a message_start whose message is a string as carrying no usage", func() {
		Expect(finishOf(`data: {"type":"message_start","message":"x"}` + "\n\n" + anthropicStop).Usage).To(BeNil())
	})

	It("bounds the error for a huge number literal in a usage object", func() {
		delta := `data: {"type":"message_delta","delta":{},"usage":{"output_tokens":` + strings.Repeat("9", 200000) + "}}\n\n"
		Expect(len(streamErr(usageStream(usageServe(anthropicStart+delta+anthropicStop, route...), req)).Error())).
			To(BeNumerically("<", 200))
	})

	DescribeTable("malformed usage ends the stream with an error",
		func(usage string) {
			body := anthropicStart + `data: {"type":"message_delta","delta":{},"usage":` + usage + "}\n\n" + anthropicStop
			Expect(streamErr(usageStream(usageServe(body, route...), req))).To(HaveOccurred())
		},
		Entry("negative output", `{"output_tokens":-1}`),
		Entry("string count", `{"output_tokens":"9"}`),
		Entry("not an object", `[1,2]`),
		Entry("count above the cap", `{"input_tokens":2000000000000}`),
		Entry("count beyond int64", `{"input_tokens":99999999999999999999}`),
	)

	It("rejects a malformed message_start usage", func() {
		body := `data: {"type":"message_start","message":{"usage":{"input_tokens":-4}}}` + "\n\n" + anthropicStop
		Expect(streamErr(usageStream(usageServe(body, route...), req))).To(HaveOccurred())
	})

	It("reports a non-streaming reply's usage", func() {
		body := `{"content":[{"type":"text","text":"x"}],"usage":{"input_tokens":5,"output_tokens":6,` +
			`"cache_read_input_tokens":2,"cache_creation_input_tokens":3}}`
		resp, err := usageServe(body, route...).Chat(context.Background(), req)
		if err != nil {
			panic(err)
		}
		Expect(resp.Usage).To(Equal(&provider.Usage{InputTokens: 5, OutputTokens: 6, CacheReadTokens: 2, CacheWriteTokens: 3}))
	})

	It("reports none for a non-streaming reply without usage", func() {
		resp, err := usageServe(`{"content":[]}`, route...).Chat(context.Background(), req)
		if err != nil {
			panic(err)
		}
		Expect(resp.Usage).To(BeNil())
	})

	It("rejects a negative usage in a non-streaming reply", func() {
		_, err := usageServe(`{"content":[],"usage":{"input_tokens":-1}}`, route...).Chat(context.Background(), req)
		Expect(err).To(HaveOccurred())
	})

	It("passes the stream validator with the usage-bearing finish event", func() {
		var v provider.StreamValidator
		var first error
		for _, ev := range usageStream(usageServe(textPar+anthropicStart+anthropicDelta+anthropicStop, route...), req) {
			if err := v.Accept(ev); err != nil && first == nil {
				first = err
			}
		}
		Expect(first).NotTo(HaveOccurred())
	})
})
