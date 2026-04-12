package conformance_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
)

// sseToolCallFixture defines a canned SSE response for a provider shape that
// exercises the tool-call streaming path.
type sseToolCallFixture struct {
	// name identifies the provider shape (e.g. "standard", "github-copilot", "zai").
	name string
	// description explains what makes this shape distinct.
	description string
	// sseChunks is the ordered list of JSON payloads sent as SSE data frames.
	sseChunks []string
}

// standardToolCallFixture: the standard OpenAI shape where the SDK's
// JustFinishedToolCall fires normally (separate delta + finish_reason chunks).
var standardToolCallFixture = sseToolCallFixture{
	name:        "standard",
	description: "tool_calls delta and finish_reason arrive in separate chunks (happy path for JustFinishedToolCall)",
	sseChunks: []string{
		`{"id":"chatcmpl-std","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_std","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"London\"}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-std","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	},
}

// githubCopilotToolCallFixture: github-copilot combines the final tool_calls
// delta with finish_reason in the same chunk, so JustFinishedToolCall never
// fires and flushAccumulatedToolCalls is the emitter.
var githubCopilotToolCallFixture = sseToolCallFixture{
	name:        "github-copilot",
	description: "final tool_calls delta combined with finish_reason in one chunk (flush path)",
	sseChunks: []string{
		`{"id":"chatcmpl-copilot","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_copilot","type":"function","function":{"name":"delegate","arguments":""}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-copilot","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"agent\":\"Explore\"}"}}]},"finish_reason":"tool_calls"}]}`,
	},
}

// zaiToolCallFixture: zai emits an empty content field alongside tool_calls on
// every chunk, which confused earlier accumulator logic.
var zaiToolCallFixture = sseToolCallFixture{
	name:        "zai",
	description: "every chunk carries empty content alongside tool_calls (zai shape)",
	sseChunks: []string{
		`{"id":"chatcmpl-zai","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"","tool_calls":[{"index":0,"id":"call_zai","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-zai","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"","tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"x.txt\"}"}}]},"finish_reason":"tool_calls"}]}`,
	},
}

// expectedToolCall holds the expected values for a fixture's tool call.
type expectedToolCall struct {
	id   string
	name string
	args map[string]any
}

var fixtureExpected = map[string]expectedToolCall{
	"standard":       {id: "call_std", name: "get_weather", args: map[string]any{"city": "London"}},
	"github-copilot": {id: "call_copilot", name: "delegate", args: map[string]any{"agent": "Explore"}},
	"zai":            {id: "call_zai", name: "read_file", args: map[string]any{"path": "x.txt"}},
}

// serveSSE creates an httptest.Server that serves the given SSE chunks followed
// by the [DONE] sentinel.
func serveSSE(chunks []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// collectChunks drains a StreamChunk channel into a slice.
func collectChunks(ch <-chan provider.StreamChunk) []provider.StreamChunk {
	var chunks []provider.StreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}
	return chunks
}

// toolCallChunks filters a slice to only chunks with ToolCall != nil.
func toolCallChunks(chunks []provider.StreamChunk) []provider.StreamChunk {
	var result []provider.StreamChunk
	for i := range chunks {
		if chunks[i].ToolCall != nil {
			result = append(result, chunks[i])
		}
	}
	return result
}

var _ = Describe("OpenAI-compat streaming conformance", func() {
	var server *httptest.Server

	AfterEach(func() {
		if server != nil {
			server.Close()
		}
	})

	// Spec 1 & 2 & 4: tool-call chunk shape, EventType tagging, and fields populated.
	DescribeTable("tool-call chunk shape (spec 1, 2, 4)",
		func(fixture sseToolCallFixture) {
			expected := fixtureExpected[fixture.name]

			server = serveSSE(fixture.sseChunks)
			client := openaiAPI.NewClient(
				option.WithAPIKey("test-key"),
				option.WithBaseURL(server.URL),
			)
			params := openaicompat.BuildParams(provider.ChatRequest{
				Model:    "gpt-4o",
				Messages: []provider.Message{{Role: "user", Content: "conform"}},
			})
			ch := openaicompat.RunStream(context.Background(), client, params, "conformance-test")
			chunks := collectChunks(ch)
			tcChunks := toolCallChunks(chunks)

			// Spec 1: ToolCall != nil (the shape the engine gates on).
			Expect(tcChunks).To(HaveLen(1),
				"exactly one tool-call chunk expected for fixture %q", fixture.name)

			tc := tcChunks[0]

			// Spec 2: EventType == "tool_call".
			Expect(tc.EventType).To(Equal("tool_call"),
				"tool-call chunk must carry EventType=\"tool_call\" so the engine dispatches it (fixture %q)", fixture.name)

			// Spec 4: ID, Name, and Arguments all non-empty.
			Expect(tc.ToolCall.ID).NotTo(BeEmpty(),
				"ToolCall.ID must be populated (fixture %q)", fixture.name)
			Expect(tc.ToolCall.Name).NotTo(BeEmpty(),
				"ToolCall.Name must be populated (fixture %q)", fixture.name)
			Expect(tc.ToolCall.Arguments).NotTo(BeEmpty(),
				"ToolCall.Arguments must be non-empty (fixture %q)", fixture.name)

			// Verify exact expected values.
			Expect(tc.ToolCall.ID).To(Equal(expected.id))
			Expect(tc.ToolCall.Name).To(Equal(expected.name))
			for k, v := range expected.args {
				Expect(tc.ToolCall.Arguments).To(HaveKeyWithValue(k, v))
			}
		},
		Entry("standard OpenAI shape", standardToolCallFixture),
		Entry("github-copilot shape (flush path)", githubCopilotToolCallFixture),
		Entry("zai shape (empty content alongside tool_calls)", zaiToolCallFixture),
	)

	// Spec 3: error classification.
	DescribeTable("error classification (spec 3)",
		func(statusCode int, expectedType provider.ErrorType, expectRetriable bool) {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(statusCode)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]interface{}{
						"message": "synthetic error for conformance",
						"type":    "server_error",
					},
				})
			}))
			client := openaiAPI.NewClient(
				option.WithAPIKey("test-key"),
				option.WithBaseURL(server.URL),
				option.WithMaxRetries(0),
			)
			params := openaicompat.BuildParams(provider.ChatRequest{
				Model:    "gpt-4o",
				Messages: []provider.Message{{Role: "user", Content: "error"}},
			})
			ch := openaicompat.RunStream(context.Background(), client, params, "conformance-test")
			chunks := collectChunks(ch)

			Expect(chunks).NotTo(BeEmpty(), "at least one chunk expected on error path")
			lastChunk := chunks[len(chunks)-1]
			Expect(lastChunk.Error).To(HaveOccurred(), "error chunk must carry an error")
			Expect(lastChunk.Done).To(BeTrue(), "error chunk must signal done")

			var provErr *provider.Error
			Expect(errors.As(lastChunk.Error, &provErr)).To(BeTrue(),
				"stream error must unwrap to *provider.Error for engine retry classification")
			Expect(provErr.ErrorType).To(Equal(expectedType),
				"error type must match expected classification")
			Expect(provErr.HTTPStatus).To(Equal(statusCode),
				"HTTP status must be preserved")
			Expect(provErr.IsRetriable).To(Equal(expectRetriable),
				"retriability must match expected value")
			Expect(provErr.Provider).To(Equal("conformance-test"),
				"provider name must match the providerName passed to RunStream")
		},
		Entry("429 rate limit", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, true),
		Entry("500 server error", http.StatusInternalServerError, provider.ErrorTypeServerError, true),
		Entry("502 bad gateway", http.StatusBadGateway, provider.ErrorTypeServerError, true),
		Entry("503 service unavailable", http.StatusServiceUnavailable, provider.ErrorTypeServerError, true),
	)

	// Spec 5: done sentinel.
	DescribeTable("done sentinel (spec 5)",
		func(fixture sseToolCallFixture) {
			server = serveSSE(fixture.sseChunks)
			client := openaiAPI.NewClient(
				option.WithAPIKey("test-key"),
				option.WithBaseURL(server.URL),
			)
			params := openaicompat.BuildParams(provider.ChatRequest{
				Model:    "gpt-4o",
				Messages: []provider.Message{{Role: "user", Content: "done?"}},
			})
			ch := openaicompat.RunStream(context.Background(), client, params, "conformance-test")
			chunks := collectChunks(ch)

			Expect(chunks).NotTo(BeEmpty(), "stream must emit at least one chunk")
			lastChunk := chunks[len(chunks)-1]
			Expect(lastChunk.Done).To(BeTrue(),
				"stream must terminate with Done=true (fixture %q)", fixture.name)
		},
		Entry("standard shape", standardToolCallFixture),
		Entry("github-copilot shape", githubCopilotToolCallFixture),
		Entry("zai shape", zaiToolCallFixture),
	)

	// Spec 5 (text path): done sentinel for a plain text stream with no tool calls.
	It("terminates with Done=true for a text-only stream", func() {
		server = serveSSE([]string{
			`{"id":"chatcmpl-txt","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-txt","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		})
		client := openaiAPI.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		)
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		ch := openaicompat.RunStream(context.Background(), client, params, "conformance-test")
		chunks := collectChunks(ch)

		Expect(chunks).NotTo(BeEmpty())
		lastChunk := chunks[len(chunks)-1]
		Expect(lastChunk.Done).To(BeTrue(), "text stream must terminate with Done=true")
	})
})
