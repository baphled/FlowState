package openzen_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openai/openai-go/option"

	providerPkg "github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openzen"
)

var _ = Describe("OpenZen Provider", func() {
	Describe("New", func() {
		It("returns an error when API key is empty", func() {
			p, err := openzen.New("")
			Expect(err).To(HaveOccurred())
			Expect(p).To(BeNil())
		})

		It("returns a provider when API key is provided", func() {
			p, err := openzen.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Describe("NewWithOptions", func() {
		It("returns an error when API key is empty", func() {
			p, err := openzen.NewWithOptions("")
			Expect(err).To(HaveOccurred())
			Expect(p).To(BeNil())
		})

		It("returns a provider when API key and options are provided", func() {
			p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL("http://localhost:8080"))
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Describe("Name", func() {
		It("returns openzen", func() {
			p, err := openzen.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Name()).To(Equal("openzen"))
		})
	})

	Describe("Models", func() {
		It("returns the OpenZen model list", func() {
			p, err := openzen.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			models, err := p.Models()
			Expect(err).NotTo(HaveOccurred())
			Expect(models).NotTo(BeEmpty())
			Expect(models).To(ContainElement(HaveField("ID", Equal("claude-sonnet-4-5"))))
			for _, model := range models {
				Expect(model.Provider).To(Equal("openzen"))
			}
		})
	})

	Describe("Chat", func() {
		var server *httptest.Server

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when the server returns a valid response", func() {
			It("returns the assistant message", func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/chat/completions"))
					Expect(r.Method).To(Equal(http.MethodPost))

					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					var req map[string]interface{}
					Expect(json.Unmarshal(body, &req)).To(Succeed())
					Expect(req["model"]).To(Equal("claude-sonnet-4-5"))

					resp := map[string]interface{}{
						"choices": []map[string]interface{}{{
							"index": 0,
							"message": map[string]interface{}{
								"role":    "assistant",
								"content": "Hello from OpenZen",
							},
							"finish_reason": "stop",
						}},
						"usage": map[string]interface{}{
							"prompt_tokens":     10,
							"completion_tokens": 12,
							"total_tokens":      22,
						},
					}
					w.Header().Set("Content-Type", "application/json")
					Expect(json.NewEncoder(w).Encode(resp)).To(Succeed())
				}))

				p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				resp, err := p.Chat(context.Background(), providerPkg.ChatRequest{
					Model:    "claude-sonnet-4-5",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.Message.Role).To(Equal("assistant"))
				Expect(resp.Message.Content).To(Equal("Hello from OpenZen"))
				Expect(resp.Usage.TotalTokens).To(Equal(22))
			})
		})

		Context("when the server returns no choices", func() {
			It("returns an error", func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"choices": []map[string]interface{}{}})
				}))

				p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				_, err = p.Chat(context.Background(), providerPkg.ChatRequest{Model: "claude-sonnet-4-5"})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no choices"))
			})
		})

		Context("when the server returns an error", func() {
			It("returns a wrapped error", func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "boom"}})
				}))

				p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				_, err = p.Chat(context.Background(), providerPkg.ChatRequest{Model: "claude-sonnet-4-5"})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("openzen chat failed"))
			})
		})

		Context("when the server returns 401", func() {
			It("returns an error", func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "unauthorised"}})
				}))

				p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				_, err = p.Chat(context.Background(), providerPkg.ChatRequest{Model: "claude-sonnet-4-5"})
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when the server returns 429", func() {
			It("returns a rate limit error", func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusTooManyRequests)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "rate limited"}})
				}))

				p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				_, err = p.Chat(context.Background(), providerPkg.ChatRequest{Model: "claude-sonnet-4-5"})
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("Stream", func() {
		var server *httptest.Server

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		It("returns chunks from a valid streaming response", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`)
				fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":" world"},"finish_reason":null}]}`)
				fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))

			p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			ch, err := p.Stream(context.Background(), providerPkg.ChatRequest{Model: "claude-sonnet-4-5"})
			Expect(err).NotTo(HaveOccurred())

			var chunks []providerPkg.StreamChunk
			for chunk := range ch {
				chunks = append(chunks, chunk)
			}

			Expect(chunks).NotTo(BeEmpty())
		})

		It("closes the channel after completion", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))

			p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			ch, err := p.Stream(context.Background(), providerPkg.ChatRequest{Model: "claude-sonnet-4-5"})
			Expect(err).NotTo(HaveOccurred())

			for chunk := range ch {
				_ = chunk
			}

			_, open := <-ch
			Expect(open).To(BeFalse())
		})

		It("returns an error chunk on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "boom"}})
			}))

			p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			ch, err := p.Stream(context.Background(), providerPkg.ChatRequest{Model: "claude-sonnet-4-5"})
			Expect(err).NotTo(HaveOccurred())

			var last providerPkg.StreamChunk
			for chunk := range ch {
				last = chunk
			}
			Expect(last.Error).To(HaveOccurred())
			Expect(last.Done).To(BeTrue())
		})

		It("returns an error chunk on 401", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "unauthorised"}})
			}))

			p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			ch, err := p.Stream(context.Background(), providerPkg.ChatRequest{Model: "claude-sonnet-4-5"})
			Expect(err).NotTo(HaveOccurred())

			var last providerPkg.StreamChunk
			for chunk := range ch {
				last = chunk
			}
			Expect(last.Error).To(HaveOccurred())
		})

		It("returns an error chunk on 429", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "rate limited"}})
			}))

			p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			ch, err := p.Stream(context.Background(), providerPkg.ChatRequest{Model: "claude-sonnet-4-5"})
			Expect(err).NotTo(HaveOccurred())

			var last providerPkg.StreamChunk
			for chunk := range ch {
				last = chunk
			}
			Expect(last.Error).To(HaveOccurred())
		})

		It("returns an error chunk on timeout", func() {
			server = httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				time.Sleep(250 * time.Millisecond)
			}))

			p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			ch, err := p.Stream(ctx, providerPkg.ChatRequest{Model: "claude-sonnet-4-5"})
			Expect(err).NotTo(HaveOccurred())

			var last providerPkg.StreamChunk
			for chunk := range ch {
				last = chunk
			}
			Expect(last.Error).To(HaveOccurred())
		})
	})

	Describe("Embed", func() {
		var server *httptest.Server

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		It("returns the default embedding model when no model is specified", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal("/embeddings"))
				w.Header().Set("Content-Type", "application/json")
				body, err := io.ReadAll(r.Body)
				Expect(err).NotTo(HaveOccurred())
				var req map[string]interface{}
				Expect(json.Unmarshal(body, &req)).To(Succeed())
				Expect(req["model"]).To(Equal("text-embedding-3-small"))

				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"data": []map[string]interface{}{{"embedding": []float64{0.1, 0.2}}},
				})
			}))

			p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			embedding, err := p.Embed(context.Background(), providerPkg.EmbedRequest{Input: "hello"})
			Expect(err).NotTo(HaveOccurred())
			Expect(embedding).To(HaveLen(2))
		})

		It("returns an error when the embedding response has no data", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": []map[string]interface{}{}})
			}))

			p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			_, err = p.Embed(context.Background(), providerPkg.EmbedRequest{Input: "hello"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no embeddings returned"))
		})

		It("returns a wrapped error when the server fails", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "boom"}})
			}))

			p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			_, err = p.Embed(context.Background(), providerPkg.EmbedRequest{Input: "hello"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("openzen embed failed"))
		})

		It("returns an error on 401", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "unauthorised"}})
			}))

			p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			_, err = p.Embed(context.Background(), providerPkg.EmbedRequest{Input: "hello"})
			Expect(err).To(HaveOccurred())
		})

		It("returns an error on 429", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "rate limited"}})
			}))

			p, err := openzen.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			_, err = p.Embed(context.Background(), providerPkg.EmbedRequest{Input: "hello"})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("NewFromOpenCodeOrConfig", func() {
		var dir string

		BeforeEach(func() {
			var err error
			dir, err = os.MkdirTemp("", "openzen-auth-*")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = os.RemoveAll(dir)
		})

		It("uses valid OpenCode OpenZen credentials", func() {
			path := filepath.Join(dir, "auth.json")
			Expect(os.WriteFile(path, []byte(`{"openzen":{"type":"oauth","access":"openzen-token"}}`), 0o600)).To(Succeed())

			p, err := openzen.NewFromOpenCodeOrConfig(path, "fallback-token")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})

		It("falls back when the auth file is missing", func() {
			p, err := openzen.NewFromOpenCodeOrConfig(filepath.Join(dir, "missing.json"), "fallback-token")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})

		It("falls back when auth exists without OpenZen credentials", func() {
			path := filepath.Join(dir, "auth.json")
			Expect(os.WriteFile(path, []byte(`{"anthropic":{"type":"oauth","access":"anthropic-token"}}`), 0o600)).To(Succeed())

			p, err := openzen.NewFromOpenCodeOrConfig(path, "fallback-token")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})

		It("returns an error when neither source provides a token", func() {
			_, err := openzen.NewFromOpenCodeOrConfig(filepath.Join(dir, "missing.json"), "")
			Expect(err).To(HaveOccurred())
		})

		It("wraps malformed JSON errors", func() {
			path := filepath.Join(dir, "auth.json")
			Expect(os.WriteFile(path, []byte(`{"openzen":`), 0o600)).To(Succeed())

			_, err := openzen.NewFromOpenCodeOrConfig(path, "fallback-token")
			Expect(err).To(HaveOccurred())
		})

		It("uses the fallback key when the OpenCode path is empty", func() {
			p, err := openzen.NewFromOpenCodeOrConfig("", "fallback-token")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})

		It("falls back when the OpenZen access token is empty", func() {
			path := filepath.Join(dir, "auth.json")
			Expect(os.WriteFile(path, []byte(`{"openzen":{"type":"oauth","access":""}}`), 0o600)).To(Succeed())

			p, err := openzen.NewFromOpenCodeOrConfig(path, "fallback-token")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})
})
