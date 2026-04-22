package zai_test

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/baphled/flowstate/internal/provider/zai"
)

var _ = Describe("ZAI Provider", func() {
	Describe("New", func() {
		It("returns an error when API key is empty", func() {
			p, err := zai.New("")
			Expect(err).To(HaveOccurred())
			Expect(p).To(BeNil())
		})

		It("returns a provider when API key is provided", func() {
			p, err := zai.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Describe("NewWithOptions", func() {
		It("returns an error when API key is empty", func() {
			p, err := zai.NewWithOptions("")
			Expect(err).To(HaveOccurred())
			Expect(p).To(BeNil())
		})

		It("returns a provider when API key and options are provided", func() {
			p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL("http://localhost:8080"))
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Describe("Name", func() {
		It("returns zai", func() {
			p, err := zai.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Name()).To(Equal("zai"))
		})
	})

	Describe("Models", func() {
		It("returns the Z.AI model list", func() {
			p, err := zai.New("test-api-key")
			Expect(err).NotTo(HaveOccurred())

			models, err := p.Models()
			Expect(err).NotTo(HaveOccurred())
			Expect(models).NotTo(BeEmpty())
			Expect(models).To(ContainElement(HaveField("ID", Equal("glm-5"))))
			for _, model := range models {
				Expect(model.Provider).To(Equal("zai"))
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
					Expect(req["model"]).To(Equal("glm-5"))

					resp := map[string]interface{}{
						"choices": []map[string]interface{}{{
							"index": 0,
							"message": map[string]interface{}{
								"role":    "assistant",
								"content": "Hello from Z.AI",
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

				p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				resp, err := p.Chat(context.Background(), providerPkg.ChatRequest{
					Model:    "glm-5",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.Message.Role).To(Equal("assistant"))
				Expect(resp.Message.Content).To(Equal("Hello from Z.AI"))
				Expect(resp.Usage.TotalTokens).To(Equal(22))
			})
		})

		Context("when the server returns no choices", func() {
			It("returns an error", func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"choices": []map[string]interface{}{}})
				}))

				p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				_, err = p.Chat(context.Background(), providerPkg.ChatRequest{Model: "glm-5"})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no choices"))
			})
		})

		Context("when the server returns an error", func() {
			It("returns a structured provider error", func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "boom"}})
				}))

				p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				_, err = p.Chat(context.Background(), providerPkg.ChatRequest{Model: "glm-5"})
				Expect(err).To(HaveOccurred())

				var provErr *providerPkg.Error
				Expect(errors.As(err, &provErr)).To(BeTrue())
				Expect(provErr.Provider).To(Equal("zai"))
				Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeServerError))
			})
		})

		Context("when the server returns 401", func() {
			It("returns an error", func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "unauthorised"}})
				}))

				p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				_, err = p.Chat(context.Background(), providerPkg.ChatRequest{Model: "glm-5"})
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when the server returns 429", func() {
			It("returns a rate limit error", func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusTooManyRequests)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "rate limited"}})
				}))

				p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				_, err = p.Chat(context.Background(), providerPkg.ChatRequest{Model: "glm-5"})
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

			p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			ch, err := p.Stream(context.Background(), providerPkg.ChatRequest{Model: "glm-5"})
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

			p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			ch, err := p.Stream(context.Background(), providerPkg.ChatRequest{Model: "glm-5"})
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

			p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			ch, err := p.Stream(context.Background(), providerPkg.ChatRequest{Model: "glm-5"})
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

			p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			ch, err := p.Stream(context.Background(), providerPkg.ChatRequest{Model: "glm-5"})
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

			p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			ch, err := p.Stream(context.Background(), providerPkg.ChatRequest{Model: "glm-5"})
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

			p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			ch, err := p.Stream(ctx, providerPkg.ChatRequest{Model: "glm-5"})
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
				Expect(req["model"]).To(Equal("embedding-3"))

				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"data": []map[string]interface{}{{"embedding": []float64{0.1, 0.2}}},
				})
			}))

			p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
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

			p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
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

			p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			_, err = p.Embed(context.Background(), providerPkg.EmbedRequest{Input: "hello"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("zai embed failed"))
		})

		It("returns an error on 401", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "unauthorised"}})
			}))

			p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
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

			p, err := zai.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())

			_, err = p.Embed(context.Background(), providerPkg.EmbedRequest{Input: "hello"})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("NewFromOpenCodeOrConfig", func() {
		var dir string

		BeforeEach(func() {
			var err error
			dir, err = os.MkdirTemp("", "zai-auth-*")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = os.RemoveAll(dir)
		})

		It("uses valid OpenCode Z.AI credentials", func() {
			path := filepath.Join(dir, "auth.json")
			Expect(os.WriteFile(path, []byte(`{"zai":{"type":"oauth","access":"zai-token"}}`), 0o600)).To(Succeed())

			p, err := zai.NewFromOpenCodeOrConfig(path, "fallback-token")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})

		It("falls back when the auth file is missing", func() {
			p, err := zai.NewFromOpenCodeOrConfig(filepath.Join(dir, "missing.json"), "fallback-token")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})

		It("falls back when auth exists without Z.AI credentials", func() {
			path := filepath.Join(dir, "auth.json")
			Expect(os.WriteFile(path, []byte(`{"anthropic":{"type":"oauth","access":"anthropic-token"}}`), 0o600)).To(Succeed())

			p, err := zai.NewFromOpenCodeOrConfig(path, "fallback-token")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})

		It("returns an error when neither source provides a token", func() {
			_, err := zai.NewFromOpenCodeOrConfig(filepath.Join(dir, "missing.json"), "")
			Expect(err).To(HaveOccurred())
		})

		It("wraps malformed JSON errors", func() {
			path := filepath.Join(dir, "auth.json")
			Expect(os.WriteFile(path, []byte(`{"zai":`), 0o600)).To(Succeed())

			_, err := zai.NewFromOpenCodeOrConfig(path, "fallback-token")
			Expect(err).To(HaveOccurred())
		})

		It("uses the fallback key when the OpenCode path is empty", func() {
			p, err := zai.NewFromOpenCodeOrConfig("", "fallback-token")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})

		It("falls back when the Z.AI access token is empty", func() {
			path := filepath.Join(dir, "auth.json")
			Expect(os.WriteFile(path, []byte(`{"zai":{"type":"oauth","access":""}}`), 0o600)).To(Succeed())

			p, err := zai.NewFromOpenCodeOrConfig(path, "fallback-token")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})

		It("reads zai-coding-plan top-level key with 'key' field (OpenCode alias)", func() {
			path := filepath.Join(dir, "auth.json")
			Expect(os.WriteFile(path, []byte(`{"zai-coding-plan":{"type":"api","key":"zai-coding-key"}}`), 0o600)).To(Succeed())

			p, err := zai.NewFromOpenCodeOrConfig(path, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})

		It("prefers zai/access when both keys are present", func() {
			path := filepath.Join(dir, "auth.json")
			Expect(os.WriteFile(path, []byte(
				`{"zai":{"type":"oauth","access":"primary"},"zai-coding-plan":{"type":"api","key":"secondary"}}`,
			), 0o600)).To(Succeed())

			p, err := zai.NewFromOpenCodeOrConfig(path, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})

		Context("endpoint selection based on auth source", func() {
			// The Z.AI `zai-coding-plan` subscription routes to a separate
			// coding-plan endpoint. Using the general pay-per-token endpoint
			// with a coding-plan key returns HTTP 429 / code 1113 (billing).
			// The general-endpoint constant must stay the pay-per-token URL;
			// coding-plan auth must route to the coding-plan URL.
			It("exposes the general pay-per-token URL for the canonical zai source", func() {
				Expect(zai.BaseURLForAuthSource("zai")).To(
					Equal("https://api.z.ai/api/paas/v4"),
				)
			})

			It("exposes the coding-plan URL for the zai-coding-plan source", func() {
				Expect(zai.BaseURLForAuthSource("zai-coding-plan")).To(
					Equal("https://api.z.ai/api/coding/paas/v4"),
				)
			})

			It("defaults to the general URL for an unknown source", func() {
				Expect(zai.BaseURLForAuthSource("")).To(
					Equal("https://api.z.ai/api/paas/v4"),
				)
			})

			It("reports zai-coding-plan as source when auth.json has only the coding-plan key", func() {
				path := filepath.Join(dir, "auth.json")
				Expect(os.WriteFile(path, []byte(
					`{"zai-coding-plan":{"type":"api","key":"coding-key"}}`,
				), 0o600)).To(Succeed())

				token, source, err := zai.ResolveOpenCodeAuthForTest(path, "")
				Expect(err).NotTo(HaveOccurred())
				Expect(token).To(Equal("coding-key"))
				Expect(source).To(Equal("zai-coding-plan"))
			})

			It("reports zai as source when auth.json has the canonical zai key", func() {
				path := filepath.Join(dir, "auth.json")
				Expect(os.WriteFile(path, []byte(
					`{"zai":{"type":"oauth","access":"canonical"}}`,
				), 0o600)).To(Succeed())

				token, source, err := zai.ResolveOpenCodeAuthForTest(path, "")
				Expect(err).NotTo(HaveOccurred())
				Expect(token).To(Equal("canonical"))
				Expect(source).To(Equal("zai"))
			})

			It("reports zai as source when only a fallback env/config key is available", func() {
				token, source, err := zai.ResolveOpenCodeAuthForTest("", "env-fallback")
				Expect(err).NotTo(HaveOccurred())
				Expect(token).To(Equal("env-fallback"))
				Expect(source).To(Equal("zai"))
			})
		})
	})

	Describe("Z.AI error code classification", func() {
		var server *httptest.Server

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		zaiErrorHandler := func(code, message string) http.HandlerFunc {
			return func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]interface{}{
						"message": message,
						"code":    code,
					},
				})
			}
		}

		chatRequest := providerPkg.ChatRequest{
			Model:    "glm-5",
			Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
		}

		Context("via Chat", func() {
			It("classifies code 1001 as RateLimit (retriable)", func() {
				server = httptest.NewServer(zaiErrorHandler("1001", "Rate limit exceeded"))
				p, err := zai.NewWithOptions("test-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				_, err = p.Chat(context.Background(), chatRequest)
				Expect(err).To(HaveOccurred())

				var provErr *providerPkg.Error
				Expect(errors.As(err, &provErr)).To(BeTrue())
				Expect(provErr.ErrorCode).To(Equal("1001"))
				Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeRateLimit))
				Expect(provErr.IsRetriable).To(BeTrue())
			})

			It("classifies code 1002 as Overload (retriable)", func() {
				server = httptest.NewServer(zaiErrorHandler("1002", "Server overloaded"))
				p, err := zai.NewWithOptions("test-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				_, err = p.Chat(context.Background(), chatRequest)
				Expect(err).To(HaveOccurred())

				var provErr *providerPkg.Error
				Expect(errors.As(err, &provErr)).To(BeTrue())
				Expect(provErr.ErrorCode).To(Equal("1002"))
				Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeOverload))
				Expect(provErr.IsRetriable).To(BeTrue())
			})

			It("classifies code 1112 as Quota (not retriable)", func() {
				server = httptest.NewServer(zaiErrorHandler("1112", "Quota exceeded"))
				p, err := zai.NewWithOptions("test-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				_, err = p.Chat(context.Background(), chatRequest)
				Expect(err).To(HaveOccurred())

				var provErr *providerPkg.Error
				Expect(errors.As(err, &provErr)).To(BeTrue())
				Expect(provErr.ErrorCode).To(Equal("1112"))
				Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeQuota))
				Expect(provErr.IsRetriable).To(BeFalse())
			})

			It("classifies code 1113 as Billing (not retriable)", func() {
				server = httptest.NewServer(zaiErrorHandler("1113", "Insufficient balance"))
				p, err := zai.NewWithOptions("test-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				_, err = p.Chat(context.Background(), chatRequest)
				Expect(err).To(HaveOccurred())

				var provErr *providerPkg.Error
				Expect(errors.As(err, &provErr)).To(BeTrue())
				Expect(provErr.ErrorCode).To(Equal("1113"))
				Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeBilling))
				Expect(provErr.IsRetriable).To(BeFalse())
			})

			It("preserves base classification for unknown error codes", func() {
				server = httptest.NewServer(zaiErrorHandler("9999", "Unknown error"))
				p, err := zai.NewWithOptions("test-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())

				_, err = p.Chat(context.Background(), chatRequest)
				Expect(err).To(HaveOccurred())

				var provErr *providerPkg.Error
				Expect(errors.As(err, &provErr)).To(BeTrue())
				Expect(provErr.ErrorCode).To(Equal("9999"))
				Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeRateLimit))
				Expect(provErr.IsRetriable).To(BeTrue())
			})
		})

		Context("via Stream", func() {
			It("classifies code 1113 as Billing in stream error chunks", func() {
				server = httptest.NewServer(zaiErrorHandler("1113", "Insufficient balance"))
				p, err := zai.NewWithOptions("test-key",
					option.WithBaseURL(server.URL),
					option.WithMaxRetries(0),
				)
				Expect(err).NotTo(HaveOccurred())

				ch, err := p.Stream(context.Background(), chatRequest)
				Expect(err).NotTo(HaveOccurred())

				var last providerPkg.StreamChunk
				for chunk := range ch {
					last = chunk
				}
				Expect(last.Error).To(HaveOccurred())

				var provErr *providerPkg.Error
				Expect(errors.As(last.Error, &provErr)).To(BeTrue())
				Expect(provErr.ErrorCode).To(Equal("1113"))
				Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeBilling))
				Expect(provErr.IsRetriable).To(BeFalse())
			})
		})
	})
})
