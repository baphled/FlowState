package ollama_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	providerPkg "github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/ollama"
)

var _ = Describe("Ollama Provider", func() {
	Describe("Name", func() {
		It("returns ollama", func() {
			p := &ollama.Provider{}
			Expect(p.Name()).To(Equal("ollama"))
		})
	})

	Describe("NewWithClient", func() {
		Context("when base URL is valid", func() {
			It("creates a provider instance", func() {
				p, err := ollama.NewWithClient("http://localhost:11434", http.DefaultClient)
				Expect(err).NotTo(HaveOccurred())
				Expect(p).NotTo(BeNil())
			})
		})

		Context("when base URL is invalid", func() {
			It("returns an error", func() {
				p, err := ollama.NewWithClient("://invalid-url", http.DefaultClient)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to parse"))
				Expect(p).To(BeNil())
			})
		})
	})

	Describe("Chat", func() {
		var (
			server   *httptest.Server
			provider *ollama.Provider
		)

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when server returns valid response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/api/chat"))
					Expect(r.Method).To(Equal(http.MethodPost))

					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					var req map[string]interface{}
					err = json.Unmarshal(body, &req)
					Expect(err).NotTo(HaveOccurred())
					Expect(req["model"]).To(Equal("llama3.2"))

					resp := map[string]interface{}{
						"model": "llama3.2",
						"message": map[string]interface{}{
							"role":    "assistant",
							"content": "Hello! How can I help you today?",
						},
						"done":              true,
						"prompt_eval_count": 10,
						"eval_count":        15,
					}
					w.Header().Set("Content-Type", "application/json")
					err = json.NewEncoder(w).Encode(resp)
					Expect(err).NotTo(HaveOccurred())
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns chat response with message content", func() {
				ctx := context.Background()
				resp, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model: "llama3.2",
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
					_, _ = w.Write([]byte(`{"error": "internal server error"}`))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns an error", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model:    "llama3.2",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ollama chat failed"))
			})
		})
	})

	Describe("Stream", func() {
		var (
			server   *httptest.Server
			provider *ollama.Provider
		)

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when server returns valid streaming response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/api/chat"))
					Expect(r.Method).To(Equal(http.MethodPost))

					w.Header().Set("Content-Type", "application/x-ndjson")

					chunks := []map[string]interface{}{
						{
							"model": "llama3.2",
							"message": map[string]interface{}{
								"role":    "assistant",
								"content": "Hello",
							},
							"done": false,
						},
						{
							"model": "llama3.2",
							"message": map[string]interface{}{
								"role":    "assistant",
								"content": " world!",
							},
							"done": false,
						},
						{
							"model": "llama3.2",
							"message": map[string]interface{}{
								"role":    "assistant",
								"content": "",
							},
							"done": true,
						},
					}

					for _, chunk := range chunks {
						data, _ := json.Marshal(chunk)
						_, _ = w.Write(data)
						_, _ = w.Write([]byte("\n"))
						if f, ok := w.(http.Flusher); ok {
							f.Flush()
						}
					}
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns chunks from streaming response", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "llama3.2",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())

				var chunks []providerPkg.StreamChunk
				for chunk := range ch {
					chunks = append(chunks, chunk)
				}

				Expect(chunks).To(HaveLen(3))
				Expect(chunks[0].Content).To(Equal("Hello"))
				Expect(chunks[0].Done).To(BeFalse())
			})
		})

		Context("when context is cancelled mid-stream", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/x-ndjson")

					chunk := map[string]interface{}{
						"model":   "llama3.2",
						"message": map[string]interface{}{"role": "assistant", "content": "Hello"},
						"done":    false,
					}
					data, _ := json.Marshal(chunk)
					_, _ = w.Write(data)
					_, _ = w.Write([]byte("\n"))
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}

					time.Sleep(500 * time.Millisecond)
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("stops streaming when context is done", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()

				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "llama3.2",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())

				var lastChunk providerPkg.StreamChunk
				for chunk := range ch {
					lastChunk = chunk
				}

				Expect(lastChunk.Error).To(Or(BeNil(), MatchError(context.DeadlineExceeded)))
			})
		})
	})

	Describe("Embed", func() {
		var (
			server   *httptest.Server
			provider *ollama.Provider
		)

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when server returns valid embedding", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/api/embed"))
					Expect(r.Method).To(Equal(http.MethodPost))

					resp := map[string]interface{}{
						"embeddings": [][]float32{{0.1, 0.2, 0.3, 0.4, 0.5}},
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns float64 slice from embedding response", func() {
				ctx := context.Background()
				embedding, err := provider.Embed(ctx, providerPkg.EmbedRequest{
					Model: "llama3.2",
					Input: "test input",
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(embedding).To(HaveLen(5))
				Expect(embedding[0]).To(BeNumerically("~", 0.1, 0.001))
				Expect(embedding[4]).To(BeNumerically("~", 0.5, 0.001))
			})
		})

		Context("when server returns empty embeddings", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					resp := map[string]interface{}{
						"embeddings": [][]float32{},
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns an error", func() {
				ctx := context.Background()
				_, err := provider.Embed(ctx, providerPkg.EmbedRequest{
					Model: "llama3.2",
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
					_, _ = w.Write([]byte(`{"error": "invalid model"}`))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns an error", func() {
				ctx := context.Background()
				_, err := provider.Embed(ctx, providerPkg.EmbedRequest{
					Model: "invalid-model",
					Input: "test input",
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ollama embed failed"))
			})
		})
	})

	Describe("Models", func() {
		var (
			server   *httptest.Server
			provider *ollama.Provider
		)

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when server returns model list", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/api/tags"))
					Expect(r.Method).To(Equal(http.MethodGet))

					resp := map[string]interface{}{
						"models": []map[string]interface{}{
							{
								"name": "llama3.2:latest",
								"details": map[string]interface{}{
									"parameter_size": "3B",
								},
							},
							{
								"name": "mistral:7b",
								"details": map[string]interface{}{
									"parameter_size": "7B",
								},
							},
						},
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns model list", func() {
				models, err := provider.Models()
				Expect(err).NotTo(HaveOccurred())
				Expect(models).To(HaveLen(2))
				Expect(models[0].ID).To(Equal("llama3.2:latest"))
				Expect(models[0].Provider).To(Equal("ollama"))
				Expect(models[1].ID).To(Equal("mistral:7b"))
			})
		})

		Context("when server returns empty model list", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					resp := map[string]interface{}{
						"models": []map[string]interface{}{},
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns empty slice without error", func() {
				models, err := provider.Models()
				Expect(err).NotTo(HaveOccurred())
				Expect(models).To(BeEmpty())
			})
		})

		Context("when server returns error", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error": "server error"}`))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns an error", func() {
				_, err := provider.Models()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ollama list models failed"))
			})
		})
	})
})
