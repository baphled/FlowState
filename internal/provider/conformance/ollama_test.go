package conformance_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/ollama"
)

// serveOllamaNDJSON creates an httptest.Server that serves newline-delimited
// JSON responses matching the Ollama chat API format.
func serveOllamaNDJSON(responses []map[string]interface{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, resp := range responses {
			data, _ := json.Marshal(resp)
			_, _ = w.Write(data)
			_, _ = w.Write([]byte("\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
}

var _ = Describe("Ollama streaming conformance", func() {
	var server *httptest.Server

	AfterEach(func() {
		if server != nil {
			server.Close()
		}
	})

	createProvider := func(serverURL string) *ollama.Provider {
		p, err := ollama.NewWithClient(serverURL, http.DefaultClient)
		Expect(err).NotTo(HaveOccurred())
		return p
	}

	// Spec 1, 2, 4: tool-call chunk shape, EventType, and fields populated.
	It("emits tool-call chunks with correct shape, EventType, and populated fields", func() {
		server = serveOllamaNDJSON([]map[string]interface{}{
			{
				"model": "llama3.2",
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]interface{}{
						{
							"function": map[string]interface{}{
								"name": "get_weather",
								"arguments": map[string]interface{}{
									"location": "London",
								},
							},
						},
					},
				},
				"done": true,
			},
		})
		p := createProvider(server.URL)

		ch, err := p.Stream(context.Background(), provider.ChatRequest{
			Model:    "llama3.2",
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
			"Ollama tool-call chunks must carry EventType=\"tool_call\"")

		// Spec 4: ID, Name, Arguments non-empty.
		// Note: Ollama uses the function name as the ID since the Ollama API
		// does not provide a separate call ID.
		Expect(tc.ToolCall.ID).NotTo(BeEmpty(),
			"ToolCall.ID must be populated")
		Expect(tc.ToolCall.Name).To(Equal("get_weather"))
		Expect(tc.ToolCall.Arguments).NotTo(BeEmpty())
		Expect(tc.ToolCall.Arguments).To(HaveKeyWithValue("location", "London"))
	})

	// Spec 3: error classification for 429 and 5xx.
	DescribeTable("error classification (spec 3)",
		func(statusCode int, expectedType provider.ErrorType, expectRetriable bool) {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(statusCode)
				_, _ = w.Write([]byte(`{"error": "synthetic conformance error"}`))
			}))
			p := createProvider(server.URL)

			ch, err := p.Stream(context.Background(), provider.ChatRequest{
				Model:    "llama3.2",
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
				"Ollama stream error must unwrap to *provider.Error for engine retry classification")
			Expect(provErr.ErrorType).To(Equal(expectedType))
			Expect(provErr.IsRetriable).To(Equal(expectRetriable))
		},
		Entry("429 rate limit", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, true),
		Entry("500 server error", http.StatusInternalServerError, provider.ErrorTypeServerError, true),
		Entry("503 service unavailable", http.StatusServiceUnavailable, provider.ErrorTypeServerError, true),
	)

	// Spec 5: done sentinel.
	It("terminates with Done=true after a tool-call response", func() {
		server = serveOllamaNDJSON([]map[string]interface{}{
			{
				"model": "llama3.2",
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]interface{}{
						{
							"function": map[string]interface{}{
								"name":      "test_tool",
								"arguments": map[string]interface{}{"key": "value"},
							},
						},
					},
				},
				"done": true,
			},
		})
		p := createProvider(server.URL)

		ch, err := p.Stream(context.Background(), provider.ChatRequest{
			Model:    "llama3.2",
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
		server = serveOllamaNDJSON([]map[string]interface{}{
			{
				"model":   "llama3.2",
				"message": map[string]interface{}{"role": "assistant", "content": "Hello world"},
				"done":    true,
			},
		})
		p := createProvider(server.URL)

		ch, err := p.Stream(context.Background(), provider.ChatRequest{
			Model:    "llama3.2",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		Expect(err).NotTo(HaveOccurred())

		chunks := collectChunks(ch)
		Expect(chunks).NotTo(BeEmpty())
		lastChunk := chunks[len(chunks)-1]
		Expect(lastChunk.Done).To(BeTrue(), "text stream must terminate with Done=true")
	})
})
