package conformance_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	anthropicSDKOption "github.com/anthropics/anthropic-sdk-go/option"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
)

// serveAnthropicSSE creates an httptest.Server that serves Anthropic-format SSE
// events. The Anthropic SSE format uses "event: <type>\ndata: <json>\n\n" lines.
func serveAnthropicSSE(events []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		for _, event := range events {
			fmt.Fprint(w, event)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
}

// anthropicToolCallEvents produces the SSE event sequence for a tool-call
// response from Anthropic (content_block_start + input_json_delta deltas + stop + message_stop).
func anthropicToolCallEvents(toolID, toolName, argsJSON string) []string {
	// Build the content_block_start and content_block_delta payloads using
	// json.Marshal to avoid gocritic sprintfQuotedString and gosec G115.
	blockStart, _ := json.Marshal(map[string]interface{}{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]interface{}{
			"type":  "tool_use",
			"id":    toolID,
			"name":  toolName,
			"input": map[string]interface{}{},
		},
	})
	blockDelta, _ := json.Marshal(map[string]interface{}{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]interface{}{
			"type":         "input_json_delta",
			"partial_json": argsJSON,
		},
	})
	return []string{
		"event: message_start\n" +
			`data: {"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-20250514","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}` + "\n\n",
		"event: content_block_start\n" +
			fmt.Sprintf("data: %s", blockStart) + "\n\n",
		"event: content_block_delta\n" +
			fmt.Sprintf("data: %s", blockDelta) + "\n\n",
		"event: content_block_stop\n" +
			`data: {"type":"content_block_stop","index":0}` + "\n\n",
		"event: message_delta\n" +
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":50}}` + "\n\n",
		"event: message_stop\n" +
			`data: {"type":"message_stop"}` + "\n\n",
	}
}

var _ = Describe("Anthropic streaming conformance", func() {
	var server *httptest.Server

	AfterEach(func() {
		if server != nil {
			server.Close()
		}
	})

	// Helper to create an Anthropic provider wired to a fake server.
	createProvider := func(serverURL string) *anthropic.Provider {
		p, err := anthropic.NewWithOptions("test-key-000000",
			anthropicSDKOption.WithBaseURL(serverURL),
			anthropicSDKOption.WithMaxRetries(0),
		)
		Expect(err).NotTo(HaveOccurred())
		return p
	}

	// Spec 1, 2, 4: tool-call chunk shape, EventType, and fields populated.
	It("emits tool-call chunks with correct shape, EventType, and populated fields", func() {
		events := anthropicToolCallEvents("toolu_01ABC", "skill_load", `{"name":"pre-action"}`)
		server = serveAnthropicSSE(events)
		p := createProvider(server.URL)

		ch, err := p.Stream(context.Background(), provider.ChatRequest{
			Model:    "claude-sonnet-4-20250514",
			Messages: []provider.Message{{Role: "user", Content: "conformance"}},
		})
		Expect(err).NotTo(HaveOccurred())

		chunks := collectChunks(ch)
		tcChunks := toolCallChunks(chunks)

		// Spec 1: ToolCall != nil.
		Expect(tcChunks).To(HaveLen(1), "exactly one tool-call chunk expected")

		tc := tcChunks[0]

		// Spec 2: EventType == "tool_call".
		Expect(tc.EventType).To(Equal("tool_call"),
			"Anthropic tool-call chunks must carry EventType=\"tool_call\"")

		// Spec 4: ID, Name, Arguments non-empty.
		Expect(tc.ToolCall.ID).To(Equal("toolu_01ABC"))
		Expect(tc.ToolCall.Name).To(Equal("skill_load"))
		Expect(tc.ToolCall.Arguments).NotTo(BeEmpty())
		Expect(tc.ToolCall.Arguments).To(HaveKeyWithValue("name", "pre-action"))
	})

	// Spec 3: error classification for 429 and 5xx.
	DescribeTable("error classification (spec 3)",
		func(statusCode int, expectedType provider.ErrorType, expectRetriable bool) {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(statusCode)
				fmt.Fprintf(w, `{"type":"error","error":{"type":"rate_limit_error","message":"synthetic conformance error"}}`)
			}))
			p := createProvider(server.URL)

			ch, err := p.Stream(context.Background(), provider.ChatRequest{
				Model:    "claude-sonnet-4-20250514",
				Messages: []provider.Message{{Role: "user", Content: "error"}},
			})
			Expect(err).NotTo(HaveOccurred())

			chunks := collectChunks(ch)
			Expect(chunks).NotTo(BeEmpty())
			lastChunk := chunks[len(chunks)-1]
			Expect(lastChunk.Error).To(HaveOccurred())
			Expect(lastChunk.Done).To(BeTrue())

			var provErr *provider.Error
			Expect(errors.As(lastChunk.Error, &provErr)).To(BeTrue(),
				"Anthropic stream error must unwrap to *provider.Error for engine retry classification")
			Expect(provErr.ErrorType).To(Equal(expectedType))
			Expect(provErr.HTTPStatus).To(Equal(statusCode))
			Expect(provErr.IsRetriable).To(Equal(expectRetriable))
		},
		Entry("429 rate limit", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, true),
		Entry("500 server error", http.StatusInternalServerError, provider.ErrorTypeServerError, true),
		Entry("529 overload", 529, provider.ErrorTypeOverload, true),
	)

	// Spec 5: done sentinel.
	It("terminates with Done=true after a tool-call stream", func() {
		events := anthropicToolCallEvents("toolu_01DONE", "test_tool", `{"key":"value"}`)
		server = serveAnthropicSSE(events)
		p := createProvider(server.URL)

		ch, err := p.Stream(context.Background(), provider.ChatRequest{
			Model:    "claude-sonnet-4-20250514",
			Messages: []provider.Message{{Role: "user", Content: "done?"}},
		})
		Expect(err).NotTo(HaveOccurred())

		chunks := collectChunks(ch)
		Expect(chunks).NotTo(BeEmpty())
		lastChunk := chunks[len(chunks)-1]
		Expect(lastChunk.Done).To(BeTrue(), "stream must terminate with Done=true")
	})

	// Spec 5 (text path): done sentinel for text-only stream.
	It("terminates with Done=true for a text-only stream", func() {
		events := []string{
			"event: message_start\n" +
				`data: {"type":"message_start","message":{"id":"msg_02","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-20250514","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}` + "\n\n",
			"event: content_block_start\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n",
			"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello world"}}` + "\n\n",
			"event: content_block_stop\n" +
				`data: {"type":"content_block_stop","index":0}` + "\n\n",
			"event: message_delta\n" +
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}` + "\n\n",
			"event: message_stop\n" +
				`data: {"type":"message_stop"}` + "\n\n",
		}
		server = serveAnthropicSSE(events)
		p := createProvider(server.URL)

		ch, err := p.Stream(context.Background(), provider.ChatRequest{
			Model:    "claude-sonnet-4-20250514",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		Expect(err).NotTo(HaveOccurred())

		chunks := collectChunks(ch)
		Expect(chunks).NotTo(BeEmpty())
		lastChunk := chunks[len(chunks)-1]
		Expect(lastChunk.Done).To(BeTrue(), "text stream must terminate with Done=true")
	})
})
