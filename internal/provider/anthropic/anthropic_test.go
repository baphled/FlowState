package anthropic_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	providerPkg "github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
)

var _ = Describe("Anthropic Provider", func() {
	Describe("New", func() {
		Context("when API key is empty", func() {
			It("returns an error", func() {
				p, err := anthropic.New("")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("API key is required"))
				Expect(p).To(BeNil())
			})
		})

		Context("when API key is provided", func() {
			It("returns a provider instance", func() {
				p, err := anthropic.New("test-api-key")
				Expect(err).NotTo(HaveOccurred())
				Expect(p).NotTo(BeNil())
			})
		})
	})

	Describe("NewWithOptions", func() {
		Context("when API key is empty", func() {
			It("returns an error", func() {
				p, err := anthropic.NewWithOptions("")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("API key is required"))
				Expect(p).To(BeNil())
			})
		})

		Context("when API key is provided with options", func() {
			It("returns a provider instance", func() {
				p, err := anthropic.NewWithOptions("test-api-key", option.WithBaseURL("http://localhost:8080"))
				Expect(err).NotTo(HaveOccurred())
				Expect(p).NotTo(BeNil())
			})
		})
	})

	Describe("Name", func() {
		It("returns anthropic", func() {
			p, err := anthropic.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Name()).To(Equal("anthropic"))
		})
	})

	Describe("Embed", func() {
		It("returns ErrNotSupported", func() {
			p, err := anthropic.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			ctx := context.Background()
			_, embedErr := p.Embed(ctx, providerPkg.EmbedRequest{
				Input: "test input",
				Model: "test-model",
			})
			Expect(embedErr).To(MatchError(anthropic.ErrNotSupported))
		})
	})

	Describe("Models", func() {
		It("returns a non-empty slice of models", func() {
			p, err := anthropic.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			models, err := p.Models()
			Expect(err).NotTo(HaveOccurred())
			Expect(models).NotTo(BeEmpty())
		})

		It("sets provider to anthropic for all models", func() {
			p, err := anthropic.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			models, err := p.Models()
			Expect(err).NotTo(HaveOccurred())

			for _, m := range models {
				Expect(m.Provider).To(Equal("anthropic"))
			}
		})

		It("includes claude-sonnet-4 model", func() {
			p, err := anthropic.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			models, err := p.Models()
			Expect(err).NotTo(HaveOccurred())

			var modelIDs []string
			for _, m := range models {
				modelIDs = append(modelIDs, m.ID)
			}
			Expect(modelIDs).To(ContainElement("claude-sonnet-4-20250514"))
		})
	})

	Describe("Chat", func() {
		var (
			server   *httptest.Server
			provider *anthropic.Provider
		)

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when server returns valid response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/v1/messages"))
					Expect(r.Method).To(Equal(http.MethodPost))

					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					var req map[string]interface{}
					err = json.Unmarshal(body, &req)
					Expect(err).NotTo(HaveOccurred())
					Expect(req["model"]).To(Equal("claude-sonnet-4-20250514"))

					resp := map[string]interface{}{
						"id":    "msg_123",
						"type":  "message",
						"role":  "assistant",
						"model": "claude-sonnet-4-20250514",
						"content": []map[string]interface{}{
							{
								"type": "text",
								"text": "Hello! How can I help you today?",
							},
						},
						"stop_reason": "end_turn",
						"usage": map[string]interface{}{
							"input_tokens":  10,
							"output_tokens": 15,
						},
					}
					w.Header().Set("Content-Type", "application/json")
					err = json.NewEncoder(w).Encode(resp)
					Expect(err).NotTo(HaveOccurred())
				}))

				var err error
				provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns chat response with message content", func() {
				ctx := context.Background()
				resp, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model: "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{
						{Role: "user", Content: "Hello"},
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.Message.Role).To(Equal("assistant"))
				Expect(resp.Message.Content).To(Equal("Hello! How can I help you today?"))
				Expect(resp.Usage.PromptTokens).To(Equal(10))
				Expect(resp.Usage.CompletionTokens).To(Equal(15))
				Expect(resp.Usage.TotalTokens).To(Equal(25))
			})
		})

		Context("when server returns error", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					resp := map[string]interface{}{
						"type": "error",
						"error": map[string]interface{}{
							"type":    "server_error",
							"message": "internal server error",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns an error", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model:    "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("anthropic chat failed"))
			})
		})

		Context("when server returns 401", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					resp := map[string]interface{}{
						"type": "error",
						"error": map[string]interface{}{
							"type":    "authentication_error",
							"message": "invalid x-api-key",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns authentication error", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model:    "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when server returns 429", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusTooManyRequests)
					resp := map[string]interface{}{
						"type": "error",
						"error": map[string]interface{}{
							"type":    "rate_limit_error",
							"message": "Rate limit exceeded",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns rate limit error", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model:    "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("Stream", func() {
		var (
			server   *httptest.Server
			provider *anthropic.Provider
		)

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when server returns valid streaming response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/v1/messages"))

					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("Cache-Control", "no-cache")
					w.Header().Set("Connection", "keep-alive")

					events := []struct {
						eventType string
						data      string
					}{
						{"message_start", `{"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","model":"claude-sonnet-4-20250514","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}`},
						{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
						{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`},
						{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world!"}}`},
						{"content_block_stop", `{"type":"content_block_stop","index":0}`},
						{"message_stop", `{"type":"message_stop"}`},
					}

					for _, event := range events {
						fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.eventType, event.data)
						if f, ok := w.(http.Flusher); ok {
							f.Flush()
						}
					}
				}))

				var err error
				provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns chunks from streaming response", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())

				var chunks []providerPkg.StreamChunk
				for chunk := range ch {
					chunks = append(chunks, chunk)
				}

				Expect(chunks).NotTo(BeEmpty())
			})
		})

		Context("when server returns error", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					resp := map[string]interface{}{
						"type": "error",
						"error": map[string]interface{}{
							"type":    "server_error",
							"message": "internal server error",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns error chunk", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())

				var lastChunk providerPkg.StreamChunk
				for chunk := range ch {
					lastChunk = chunk
				}
				Expect(lastChunk.Error).To(HaveOccurred())
				Expect(lastChunk.Done).To(BeTrue())
			})
		})

		Context("when server returns 401", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					resp := map[string]interface{}{
						"type": "error",
						"error": map[string]interface{}{
							"type":    "authentication_error",
							"message": "invalid x-api-key",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns authentication error via channel", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())

				var lastChunk providerPkg.StreamChunk
				for chunk := range ch {
					lastChunk = chunk
				}
				Expect(lastChunk.Error).To(HaveOccurred())
				Expect(lastChunk.Done).To(BeTrue())
			})
		})

		Context("when server returns 429", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusTooManyRequests)
					resp := map[string]interface{}{
						"type": "error",
						"error": map[string]interface{}{
							"type":    "rate_limit_error",
							"message": "Rate limit exceeded",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns rate limit error via channel", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())

				var lastChunk providerPkg.StreamChunk
				for chunk := range ch {
					lastChunk = chunk
				}
				Expect(lastChunk.Error).To(HaveOccurred())
				Expect(lastChunk.Done).To(BeTrue())
			})
		})

		Context("when server times out", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					time.Sleep(2 * time.Second)
				}))

				var err error
				provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns timeout error via channel", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()

				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())

				var lastChunk providerPkg.StreamChunk
				for chunk := range ch {
					lastChunk = chunk
				}
				Expect(lastChunk.Error).To(HaveOccurred())
			})
		})

		Context("when streaming completes successfully", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					events := []struct {
						eventType string
						data      string
					}{
						{"message_start", `{"type":"message_start","message":{"id":"msg_456","type":"message","role":"assistant","model":"claude-sonnet-4-20250514","content":[],"stop_reason":null,"usage":{"input_tokens":5,"output_tokens":0}}}`},
						{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
						{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi there!"}}`},
						{"content_block_stop", `{"type":"content_block_stop","index":0}`},
						{"message_stop", `{"type":"message_stop"}`},
					}
					for _, event := range events {
						fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.eventType, event.data)
						if f, ok := w.(http.Flusher); ok {
							f.Flush()
						}
					}
				}))

				var err error
				provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns content chunks with text", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())

				var contentChunks []providerPkg.StreamChunk
				for chunk := range ch {
					if chunk.Content != "" {
						contentChunks = append(contentChunks, chunk)
					}
				}
				Expect(contentChunks).NotTo(BeEmpty())
				Expect(contentChunks[0].Content).To(Equal("Hi there!"))
			})

			It("closes channel after completion", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())

				for range ch {
				}

				_, open := <-ch
				Expect(open).To(BeFalse())
			})
		})
	})
})
