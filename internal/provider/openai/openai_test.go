package openai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

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
				Expect(err.Error()).To(ContainSubstring("openai chat failed"))
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
	})
})
