package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openai/openai-go/option"

	providerPkg "github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openai"
)

var _ = Describe("OpenAI Provider", func() {
	Describe("New", func() {
		Context("when API key is empty", func() {
			It("returns an error", func() {
				p, err := openai.New("")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("API key is required"))
				Expect(p).To(BeNil())
			})
		})

		Context("when API key is provided", func() {
			It("returns a provider instance", func() {
				p, err := openai.New("test-api-key")
				Expect(err).NotTo(HaveOccurred())
				Expect(p).NotTo(BeNil())
			})
		})
	})

	Describe("NewWithOptions", func() {
		Context("when API key is empty", func() {
			It("returns an error", func() {
				p, err := openai.NewWithOptions("")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("API key is required"))
				Expect(p).To(BeNil())
			})
		})

		Context("when API key is provided with options", func() {
			It("returns a provider instance", func() {
				p, err := openai.NewWithOptions("test-api-key", option.WithBaseURL("http://localhost:8080"))
				Expect(err).NotTo(HaveOccurred())
				Expect(p).NotTo(BeNil())
			})
		})
	})

	Describe("Name", func() {
		It("returns openai", func() {
			p, err := openai.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Name()).To(Equal("openai"))
		})
	})

	Describe("Models", func() {
		It("returns a non-empty slice of models", func() {
			p, err := openai.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			models, err := p.Models()
			Expect(err).NotTo(HaveOccurred())
			Expect(models).NotTo(BeEmpty())
		})

		It("includes gpt-4o model", func() {
			p, err := openai.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			models, err := p.Models()
			Expect(err).NotTo(HaveOccurred())

			var modelIDs []string
			for _, m := range models {
				modelIDs = append(modelIDs, m.ID)
			}
			Expect(modelIDs).To(ContainElement("gpt-4o"))
		})

		It("sets provider to openai for all models", func() {
			p, err := openai.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			models, err := p.Models()
			Expect(err).NotTo(HaveOccurred())

			for _, m := range models {
				Expect(m.Provider).To(Equal("openai"))
			}
		})

		// Phase-5 Slice β — limit-registry corrections.
		//
		// gpt-5 was missing from the registry: any caller that picked
		// the model fell through to the engine's
		// ctxstore.DefaultModelContextFallback (16K), which forced the
		// proactive overflow gate to refuse healthy turns and starved
		// the auto-compactor's gate-proximity tier. Add it explicitly
		// at OpenAI's published gpt-5 limits (400K context, 128K max
		// output) so registry consultation surfaces the real budget.
		//
		// Table-driven assertions per the limit-registry-corrections
		// brief: extends the existing Describe("Models") seam.
		It("advertises the correct ContextLength and OutputLimit for each model", func() {
			p, err := openai.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			models, err := p.Models()
			Expect(err).NotTo(HaveOccurred())

			byID := make(map[string]providerPkg.Model, len(models))
			for _, m := range models {
				byID[m.ID] = m
			}

			cases := []struct {
				id            string
				contextLength int
				outputLimit   int
			}{
				{id: "gpt-5", contextLength: 400000, outputLimit: 128000},
				{id: "gpt-4o", contextLength: 128000, outputLimit: 16384},
				{id: "gpt-4o-mini", contextLength: 128000, outputLimit: 16384},
				{id: "gpt-4-turbo", contextLength: 128000, outputLimit: 4096},
				{id: "gpt-3.5-turbo", contextLength: 16385, outputLimit: 4096},
			}
			for _, tc := range cases {
				m, ok := byID[tc.id]
				Expect(ok).To(BeTrue(), "registry must contain %s", tc.id)
				Expect(m.ContextLength).To(Equal(tc.contextLength),
					"%s ContextLength must match the upstream-published limit", tc.id)
				Expect(m.OutputLimit).To(Equal(tc.outputLimit),
					"%s OutputLimit must match the upstream-published max-output", tc.id)
			}
		})
	})

	Describe("Chat", func() {
		var (
			server   *httptest.Server
			provider *openai.Provider
		)

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when server returns valid response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/chat/completions"))
					Expect(r.Method).To(Equal(http.MethodPost))

					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					var req map[string]interface{}
					err = json.Unmarshal(body, &req)
					Expect(err).NotTo(HaveOccurred())
					Expect(req["model"]).To(Equal("gpt-4o"))

					resp := map[string]interface{}{
						"id":      "chatcmpl-123",
						"object":  "chat.completion",
						"created": 1677652288,
						"model":   "gpt-4o",
						"choices": []map[string]interface{}{
							{
								"index": 0,
								"message": map[string]interface{}{
									"role":    "assistant",
									"content": "Hello! How can I help you today?",
								},
								"finish_reason": "stop",
							},
						},
						"usage": map[string]interface{}{
							"prompt_tokens":     10,
							"completion_tokens": 15,
							"total_tokens":      25,
						},
					}
					w.Header().Set("Content-Type", "application/json")
					err = json.NewEncoder(w).Encode(resp)
					Expect(err).NotTo(HaveOccurred())
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns chat response with message content", func() {
				ctx := context.Background()
				resp, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model: "gpt-4o",
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

		Context("when server returns no choices", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					resp := map[string]interface{}{
						"id":      "chatcmpl-123",
						"object":  "chat.completion",
						"model":   "gpt-4o",
						"choices": []map[string]interface{}{},
						"usage": map[string]interface{}{
							"prompt_tokens":     10,
							"completion_tokens": 0,
							"total_tokens":      10,
						},
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns an error", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model:    "gpt-4o",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no choices"))
			})
		})

		Context("when server returns error", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					resp := map[string]interface{}{
						"error": map[string]interface{}{
							"message": "internal server error",
							"type":    "server_error",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns an error", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model:    "gpt-4o",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("internal server error"))
			})
		})

		Context("when server returns 401", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					resp := map[string]interface{}{
						"error": map[string]interface{}{
							"message": "Incorrect API key provided",
							"type":    "invalid_request_error",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns authentication error", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model:    "gpt-4o",
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
						"error": map[string]interface{}{
							"message": "Rate limit exceeded",
							"type":    "rate_limit_error",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns rate limit error", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model:    "gpt-4o",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).To(HaveOccurred())
			})
		})

		// Sibling follow-up to anthropic Phase 3 #3 — OpenAI emits
		// `retry-after` and `x-ratelimit-*` on 429s. Verify the
		// e2e Chat path attaches those to provider.Error.RateLimit
		// so the failover hook honours the carrier-issued back-off.
		Context("when server returns 429 with rate-limit headers", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("retry-after", "12")
					w.Header().Set("x-ratelimit-limit-requests", "500")
					w.Header().Set("x-ratelimit-remaining-requests", "0")
					w.Header().Set("x-ratelimit-reset-requests", "12s")
					w.Header().Set("x-ratelimit-limit-tokens", "30000")
					w.Header().Set("x-ratelimit-remaining-tokens", "1000")
					w.Header().Set("x-ratelimit-reset-tokens", "200ms")
					w.Header().Set("x-request-id", "req_openai_429")
					w.WriteHeader(http.StatusTooManyRequests)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"error": map[string]interface{}{
							"message": "Rate limit exceeded",
							"type":    "rate_limit_error",
						},
					})
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("attaches RateLimit metadata parsed from headers", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model:    "gpt-4o",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).To(HaveOccurred())

				var provErr *providerPkg.Error
				Expect(errors.As(err, &provErr)).To(BeTrue())
				Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeRateLimit))
				Expect(provErr.RateLimit).NotTo(BeNil())
				Expect(provErr.RateLimit.RetryAfter).To(Equal(12 * time.Second))
				Expect(provErr.RateLimit.RequestsLimit).To(Equal(500))
				Expect(provErr.RateLimit.RequestsRemaining).To(Equal(0))
				Expect(provErr.RateLimit.RequestsReset.IsZero()).To(BeFalse())
				Expect(provErr.RateLimit.TokensLimit).To(Equal(30000))
				Expect(provErr.RateLimit.TokensRemaining).To(Equal(1000))
				Expect(provErr.RateLimit.TokensReset.IsZero()).To(BeFalse())
				Expect(provErr.RateLimit.RequestID).To(Equal("req_openai_429"))
			})
		})
	})

	Describe("Stream", func() {
		var (
			server   *httptest.Server
			provider *openai.Provider
		)

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when server returns valid streaming response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/chat/completions"))

					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("Cache-Control", "no-cache")
					w.Header().Set("Connection", "keep-alive")

					chunks := []string{
						`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
						`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world!"},"finish_reason":null}]}`,
						`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
					}

					for _, chunk := range chunks {
						fmt.Fprintf(w, "data: %s\n\n", chunk)
						if f, ok := w.(http.Flusher); ok {
							f.Flush()
						}
					}
					fmt.Fprint(w, "data: [DONE]\n\n")
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns chunks from streaming response", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "gpt-4o",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())

				var chunks []providerPkg.StreamChunk
				for chunk := range ch {
					chunks = append(chunks, chunk)
				}

				Expect(chunks).To(HaveLen(3))
				Expect(chunks[0].Content).To(Equal("Hello"))
			})
		})

		Context("when server returns error", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					resp := map[string]interface{}{
						"error": map[string]interface{}{
							"message": "internal server error",
							"type":    "server_error",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns error chunk", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "gpt-4o",
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
						"error": map[string]interface{}{
							"message": "Incorrect API key provided",
							"type":    "invalid_request_error",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns authentication error via channel", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "gpt-4o",
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
						"error": map[string]interface{}{
							"message": "Rate limit exceeded",
							"type":    "rate_limit_error",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns rate limit error via channel", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "gpt-4o",
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
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns timeout error via channel", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()

				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "gpt-4o",
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
					fmt.Fprintf(w, "data: %s\n\n", `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}]}`)
					fmt.Fprint(w, "data: [DONE]\n\n")
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("closes channel after completion", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "gpt-4o",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())

				for v := range ch {
					_ = v
				}

				_, open := <-ch
				Expect(open).To(BeFalse())
			})
		})
	})

	Describe("Embed", func() {
		var (
			server   *httptest.Server
			provider *openai.Provider
		)

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when server returns valid embedding", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/embeddings"))
					Expect(r.Method).To(Equal(http.MethodPost))

					resp := map[string]interface{}{
						"object": "list",
						"model":  "text-embedding-3-small",
						"data": []map[string]interface{}{
							{
								"object":    "embedding",
								"index":     0,
								"embedding": []float64{0.1, 0.2, 0.3, 0.4, 0.5},
							},
						},
						"usage": map[string]interface{}{
							"prompt_tokens": 5,
							"total_tokens":  5,
						},
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns float64 slice from embedding response", func() {
				ctx := context.Background()
				embedding, err := provider.Embed(ctx, providerPkg.EmbedRequest{
					Model: "text-embedding-3-small",
					Input: "test input",
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(embedding).To(HaveLen(5))
				Expect(embedding[0]).To(BeNumerically("~", 0.1, 0.001))
				Expect(embedding[4]).To(BeNumerically("~", 0.5, 0.001))
			})
		})

		Context("when model is not specified", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					var req map[string]interface{}
					_ = json.Unmarshal(body, &req)
					Expect(req["model"]).To(Equal("text-embedding-3-small"))

					resp := map[string]interface{}{
						"object": "list",
						"model":  "text-embedding-3-small",
						"data": []map[string]interface{}{
							{"object": "embedding", "index": 0, "embedding": []float64{0.1}},
						},
						"usage": map[string]interface{}{"prompt_tokens": 1, "total_tokens": 1},
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("uses default model", func() {
				ctx := context.Background()
				embedding, err := provider.Embed(ctx, providerPkg.EmbedRequest{
					Input: "test input",
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(embedding).To(HaveLen(1))
			})
		})

		Context("when server returns empty data", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					resp := map[string]interface{}{
						"object": "list",
						"data":   []map[string]interface{}{},
						"usage":  map[string]interface{}{"prompt_tokens": 0, "total_tokens": 0},
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns an error", func() {
				ctx := context.Background()
				_, err := provider.Embed(ctx, providerPkg.EmbedRequest{
					Model: "text-embedding-3-small",
					Input: "test input",
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no embeddings returned"))
			})
		})

		Context("when server returns error", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusBadRequest)
					resp := map[string]interface{}{
						"error": map[string]interface{}{
							"message": "invalid model",
							"type":    "invalid_request_error",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns an error", func() {
				ctx := context.Background()
				_, err := provider.Embed(ctx, providerPkg.EmbedRequest{
					Model: "invalid-model",
					Input: "test input",
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("openai embed failed"))
			})
		})

		Context("when server returns 401", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					resp := map[string]interface{}{
						"error": map[string]interface{}{
							"message": "Incorrect API key provided",
							"type":    "invalid_request_error",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns authentication error", func() {
				ctx := context.Background()
				_, err := provider.Embed(ctx, providerPkg.EmbedRequest{
					Model: "text-embedding-3-small",
					Input: "test input",
				})
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when server returns 429", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusTooManyRequests)
					resp := map[string]interface{}{
						"error": map[string]interface{}{
							"message": "Rate limit exceeded",
							"type":    "rate_limit_error",
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns rate limit error", func() {
				ctx := context.Background()
				_, err := provider.Embed(ctx, providerPkg.EmbedRequest{
					Model: "text-embedding-3-small",
					Input: "test input",
				})
				Expect(err).To(HaveOccurred())
			})
		})
	})
})

// Plan "Chat Attachments Backend (May 2026)" §6 task-11 — image
// attachment threading + AC-12-UsageDelta-No-Regress (the OpenAI
// provider's Chat path uses openaicompat.ParseChatResponse, whose
// usage block is the same one PR1's Anthropic path uses; threading
// images alongside text must NOT regress the non-streaming usage
// totals).
var _ = Describe("OpenAI Provider image attachments (PR3 task-11)", func() {
	pngBytes := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

	Describe("Chat path threads image attachments and preserves usage", func() {
		var (
			server     *httptest.Server
			capturedBody map[string]interface{}
			prov       *openai.Provider
		)

		BeforeEach(func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				Expect(err).NotTo(HaveOccurred())
				Expect(json.Unmarshal(body, &capturedBody)).To(Succeed())

				resp := map[string]interface{}{
					"id":      "chatcmpl-img",
					"object":  "chat.completion",
					"created": 1677652288,
					"model":   "gpt-4o",
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"message": map[string]interface{}{
								"role":    "assistant",
								"content": "I see a small image.",
							},
							"finish_reason": "stop",
						},
					},
					// AC-12-UsageDelta-No-Regress: usage block must
					// land untouched on the response, exactly as
					// PR1's Anthropic path requires.
					"usage": map[string]interface{}{
						"prompt_tokens":     42,
						"completion_tokens": 8,
						"total_tokens":      50,
					},
				}
				w.Header().Set("Content-Type", "application/json")
				Expect(json.NewEncoder(w).Encode(resp)).To(Succeed())
			}))

			var err error
			prov, err = openai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		It("emits the user message as an [image_url, text] content-part array on the wire", func() {
			ctx := context.Background()
			_, err := prov.Chat(ctx, providerPkg.ChatRequest{
				Model: "gpt-4o",
				Messages: []providerPkg.Message{
					{Role: "user", Content: "describe this", Attachments: []providerPkg.Attachment{
						{ID: "a", MediaType: "image/png", Data: pngBytes},
					}},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Inspect the wire payload — the first message's content
			// must be an array of parts, not a bare string.
			msgs, ok := capturedBody["messages"].([]interface{})
			Expect(ok).To(BeTrue())
			Expect(msgs).To(HaveLen(1))
			first, ok := msgs[0].(map[string]interface{})
			Expect(ok).To(BeTrue())
			content, ok := first["content"].([]interface{})
			Expect(ok).To(BeTrue(), "user message content must be an array of parts when attachments are present")
			Expect(content).To(HaveLen(2))
			// Image part first.
			img, ok := content[0].(map[string]interface{})
			Expect(ok).To(BeTrue())
			Expect(img["type"]).To(Equal("image_url"))
			imgURL, ok := img["image_url"].(map[string]interface{})
			Expect(ok).To(BeTrue())
			Expect(imgURL["url"]).To(HavePrefix("data:image/png;base64,"))
			// Text part second.
			txt, ok := content[1].(map[string]interface{})
			Expect(ok).To(BeTrue())
			Expect(txt["type"]).To(Equal("text"))
			Expect(txt["text"]).To(Equal("describe this"))
		})

		It("preserves usage totals on the response (AC-12-UsageDelta-No-Regress)", func() {
			ctx := context.Background()
			resp, err := prov.Chat(ctx, providerPkg.ChatRequest{
				Model: "gpt-4o",
				Messages: []providerPkg.Message{
					{Role: "user", Content: "x", Attachments: []providerPkg.Attachment{
						{ID: "a", MediaType: "image/png", Data: pngBytes},
					}},
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Usage.PromptTokens).To(Equal(42))
			Expect(resp.Usage.CompletionTokens).To(Equal(8))
			Expect(resp.Usage.TotalTokens).To(Equal(50))
		})
	})

	Describe("attachment-size pre-flight gate", func() {
		It("rejects requests whose attachments exceed the shared 25 MB ceiling", func() {
			prov, err := openai.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			// 26 MB total — one byte over the ceiling.
			big := make([]byte, providerPkg.MaxAttachmentRequestBytes()+1)
			copy(big, pngBytes)

			ctx := context.Background()
			_, streamErr := prov.Stream(ctx, providerPkg.ChatRequest{
				Model: "gpt-4o",
				Messages: []providerPkg.Message{
					{Role: "user", Content: "too big", Attachments: []providerPkg.Attachment{
						{ID: "x", MediaType: "image/png", Data: big},
					}},
				},
			})
			Expect(streamErr).To(HaveOccurred())
			Expect(errors.Is(streamErr, providerPkg.ErrAttachmentRequestTooLarge)).To(BeTrue())

			_, chatErr := prov.Chat(ctx, providerPkg.ChatRequest{
				Model: "gpt-4o",
				Messages: []providerPkg.Message{
					{Role: "user", Content: "too big", Attachments: []providerPkg.Attachment{
						{ID: "x", MediaType: "image/png", Data: big},
					}},
				},
			})
			Expect(chatErr).To(HaveOccurred())
			Expect(errors.Is(chatErr, providerPkg.ErrAttachmentRequestTooLarge)).To(BeTrue())
		})
	})
})
