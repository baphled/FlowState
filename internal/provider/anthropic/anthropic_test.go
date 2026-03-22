package anthropic_test

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

	Describe("NewFromOpenCodeOrConfig", func() {
		var tmpDir string

		BeforeEach(func() {
			var err error
			tmpDir, err = os.MkdirTemp("", "anthropic-test-*")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			os.RemoveAll(tmpDir)
		})

		Context("when opencode auth has anthropic credentials", func() {
			It("returns a provider using the access token", func() {
				authJSON := `{"anthropic": {"type": "api_key", "access": "sk-ant-api03-test"}}`
				authPath := filepath.Join(tmpDir, "auth.json")
				err := os.WriteFile(authPath, []byte(authJSON), 0o600)
				Expect(err).NotTo(HaveOccurred())

				p, err := anthropic.NewFromOpenCodeOrConfig(authPath, "")
				Expect(err).NotTo(HaveOccurred())
				Expect(p).NotTo(BeNil())
			})
		})

		Context("when opencode auth has oauth credentials and no fallback key", func() {
			It("returns an error", func() {
				authJSON := `{"anthropic": {"type": "oauth", "access": "sk-ant-oat01-test"}}`
				authPath := filepath.Join(tmpDir, "auth.json")
				err := os.WriteFile(authPath, []byte(authJSON), 0o600)
				Expect(err).NotTo(HaveOccurred())

				p, err := anthropic.NewFromOpenCodeOrConfig(authPath, "")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("API key is required"))
				Expect(p).To(BeNil())
			})
		})

		Context("when opencode auth has oauth credentials and fallback key is provided", func() {
			It("returns a provider using the fallback key", func() {
				authJSON := `{"anthropic": {"type": "oauth", "access": "sk-ant-oat01-test"}}`
				authPath := filepath.Join(tmpDir, "auth.json")
				err := os.WriteFile(authPath, []byte(authJSON), 0o600)
				Expect(err).NotTo(HaveOccurred())

				p, err := anthropic.NewFromOpenCodeOrConfig(authPath, "fallback-api-key")
				Expect(err).NotTo(HaveOccurred())
				Expect(p).NotTo(BeNil())
			})
		})

		Context("when opencode auth has oauth prefix without oauth type", func() {
			It("detects oauth by prefix and falls back to fallback key", func() {
				authJSON := `{"anthropic": {"type": "api_key", "access": "sk-ant-oat01-sneaky"}}`
				authPath := filepath.Join(tmpDir, "auth.json")
				err := os.WriteFile(authPath, []byte(authJSON), 0o600)
				Expect(err).NotTo(HaveOccurred())

				p, err := anthropic.NewFromOpenCodeOrConfig(authPath, "fallback-key")
				Expect(err).NotTo(HaveOccurred())
				Expect(p).NotTo(BeNil())
			})
		})

		Context("when opencode path is empty", func() {
			It("falls back to the provided key", func() {
				p, err := anthropic.NewFromOpenCodeOrConfig("", "fallback-key")
				Expect(err).NotTo(HaveOccurred())
				Expect(p).NotTo(BeNil())
			})
		})

		Context("when opencode file does not exist", func() {
			It("falls back to the provided key", func() {
				p, err := anthropic.NewFromOpenCodeOrConfig("/nonexistent/auth.json", "fallback-key")
				Expect(err).NotTo(HaveOccurred())
				Expect(p).NotTo(BeNil())
			})
		})

		Context("when neither opencode nor fallback key is available", func() {
			It("returns an error", func() {
				p, err := anthropic.NewFromOpenCodeOrConfig("", "")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("API key is required"))
				Expect(p).To(BeNil())
			})
		})

		Context("when opencode file has invalid JSON", func() {
			It("returns an error", func() {
				authPath := filepath.Join(tmpDir, "auth.json")
				err := os.WriteFile(authPath, []byte("not json"), 0o600)
				Expect(err).NotTo(HaveOccurred())

				p, err := anthropic.NewFromOpenCodeOrConfig(authPath, "fallback-key")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("loading opencode auth"))
				Expect(p).To(BeNil())
			})
		})

		Context("when opencode auth has empty anthropic access token", func() {
			It("falls back to the provided key", func() {
				authJSON := `{"anthropic": {"type": "api_key", "access": ""}}`
				authPath := filepath.Join(tmpDir, "auth.json")
				err := os.WriteFile(authPath, []byte(authJSON), 0o600)
				Expect(err).NotTo(HaveOccurred())

				p, err := anthropic.NewFromOpenCodeOrConfig(authPath, "fallback-key")
				Expect(err).NotTo(HaveOccurred())
				Expect(p).NotTo(BeNil())
			})
		})

		Context("when opencode auth has no anthropic section", func() {
			It("falls back to the provided key", func() {
				authJSON := `{"github-copilot": {"type": "oauth", "access": "ghu_test"}}`
				authPath := filepath.Join(tmpDir, "auth.json")
				err := os.WriteFile(authPath, []byte(authJSON), 0o600)
				Expect(err).NotTo(HaveOccurred())

				p, err := anthropic.NewFromOpenCodeOrConfig(authPath, "fallback-key")
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
		var (
			server   *httptest.Server
			provider *anthropic.Provider
		)

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when the API returns models successfully", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/models" {
						resp := map[string]interface{}{
							"data": []map[string]interface{}{
								{
									"id":           "claude-sonnet-4-20250514",
									"display_name": "Claude Sonnet 4",
									"created_at":   "2025-05-14T00:00:00Z",
									"type":         "model",
								},
								{
									"id":           "claude-opus-4-20250514",
									"display_name": "Claude Opus 4",
									"created_at":   "2025-05-14T00:00:00Z",
									"type":         "model",
								},
							},
							"has_more": false,
						}
						w.Header().Set("Content-Type", "application/json")
						err := json.NewEncoder(w).Encode(resp)
						Expect(err).NotTo(HaveOccurred())
						return
					}
				}))

				var err error
				provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns the models from the API", func() {
				models, err := provider.Models()
				Expect(err).NotTo(HaveOccurred())
				Expect(models).To(HaveLen(2))
			})

			It("sets provider to anthropic for all models", func() {
				models, err := provider.Models()
				Expect(err).NotTo(HaveOccurred())

				for _, m := range models {
					Expect(m.Provider).To(Equal("anthropic"))
				}
			})

			It("preserves model IDs from the API", func() {
				models, err := provider.Models()
				Expect(err).NotTo(HaveOccurred())

				var modelIDs []string
				for _, m := range models {
					modelIDs = append(modelIDs, m.ID)
				}
				Expect(modelIDs).To(ContainElement("claude-sonnet-4-20250514"))
				Expect(modelIDs).To(ContainElement("claude-opus-4-20250514"))
			})

			It("sets default context length for all models", func() {
				models, err := provider.Models()
				Expect(err).NotTo(HaveOccurred())

				for _, m := range models {
					Expect(m.ContextLength).To(Equal(200000))
				}
			})
		})

		Context("when the API returns an error", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/models" {
						w.WriteHeader(http.StatusInternalServerError)
						resp := map[string]interface{}{
							"type": "error",
							"error": map[string]interface{}{
								"type":    "server_error",
								"message": "internal server error",
							},
						}
						_ = json.NewEncoder(w).Encode(resp)
						return
					}
				}))

				var err error
				provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("falls back to hardcoded models", func() {
				models, err := provider.Models()
				Expect(err).NotTo(HaveOccurred())
				Expect(models).NotTo(BeEmpty())
			})

			It("includes known models in fallback", func() {
				models, err := provider.Models()
				Expect(err).NotTo(HaveOccurred())

				var modelIDs []string
				for _, m := range models {
					modelIDs = append(modelIDs, m.ID)
				}
				Expect(modelIDs).To(ContainElement("claude-sonnet-4-20250514"))
				Expect(modelIDs).To(ContainElement("claude-3-5-haiku-latest"))
				Expect(modelIDs).To(ContainElement("claude-opus-4-20250514"))
			})

			It("sets provider to anthropic in fallback models", func() {
				models, err := provider.Models()
				Expect(err).NotTo(HaveOccurred())

				for _, m := range models {
					Expect(m.Provider).To(Equal("anthropic"))
				}
			})
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

		Context("when request includes system messages", func() {
			var capturedBody map[string]interface{}

			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					err = json.Unmarshal(body, &capturedBody)
					Expect(err).NotTo(HaveOccurred())

					resp := map[string]interface{}{
						"id":    "msg_123",
						"type":  "message",
						"role":  "assistant",
						"model": "claude-sonnet-4-20250514",
						"content": []map[string]interface{}{
							{"type": "text", "text": "Response"},
						},
						"stop_reason": "end_turn",
						"usage":       map[string]interface{}{"input_tokens": 10, "output_tokens": 5},
					}
					w.Header().Set("Content-Type", "application/json")
					err = json.NewEncoder(w).Encode(resp)
					Expect(err).NotTo(HaveOccurred())
				}))

				var err error
				provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("sends system prompt via the system parameter", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model: "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{
						{Role: "system", Content: "You are a helpful assistant."},
						{Role: "user", Content: "Hello"},
					},
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(capturedBody).To(HaveKey("system"))
				systemBlocks, ok := capturedBody["system"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(systemBlocks).To(HaveLen(1))

				block, ok := systemBlocks[0].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(block["text"]).To(Equal("You are a helpful assistant."))
				Expect(block["type"]).To(Equal("text"))
			})

			It("excludes system messages from the messages array", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model: "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{
						{Role: "system", Content: "You are a helpful assistant."},
						{Role: "user", Content: "Hello"},
					},
				})
				Expect(err).NotTo(HaveOccurred())

				messages, ok := capturedBody["messages"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(messages).To(HaveLen(1))

				firstMsg, ok := messages[0].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(firstMsg["role"]).To(Equal("user"))
			})
		})

		Context("when request includes tools", func() {
			var capturedBody map[string]interface{}

			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					err = json.Unmarshal(body, &capturedBody)
					Expect(err).NotTo(HaveOccurred())

					resp := map[string]interface{}{
						"id":    "msg_123",
						"type":  "message",
						"role":  "assistant",
						"model": "claude-sonnet-4-20250514",
						"content": []map[string]interface{}{
							{"type": "text", "text": "Response"},
						},
						"stop_reason": "end_turn",
						"usage":       map[string]interface{}{"input_tokens": 10, "output_tokens": 5},
					}
					w.Header().Set("Content-Type", "application/json")
					err = json.NewEncoder(w).Encode(resp)
					Expect(err).NotTo(HaveOccurred())
				}))

				var err error
				provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
				Expect(err).NotTo(HaveOccurred())
			})

			It("sends tools via the tools parameter", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model: "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{
						{Role: "user", Content: "Run a command"},
					},
					Tools: []providerPkg.Tool{
						{
							Name:        "bash",
							Description: "Run a bash command",
							Schema: providerPkg.ToolSchema{
								Type: "object",
								Properties: map[string]interface{}{
									"command": map[string]interface{}{
										"type":        "string",
										"description": "The command to run",
									},
								},
								Required: []string{"command"},
							},
						},
					},
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(capturedBody).To(HaveKey("tools"))
				tools, ok := capturedBody["tools"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(tools).To(HaveLen(1))

				tool, ok := tools[0].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(tool["name"]).To(Equal("bash"))
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

		Context("when streaming request includes system messages", func() {
			var capturedBody map[string]interface{}

			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					err = json.Unmarshal(body, &capturedBody)
					Expect(err).NotTo(HaveOccurred())

					w.Header().Set("Content-Type", "text/event-stream")
					events := []struct {
						eventType string
						data      string
					}{
						{"message_start", `{"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","model":"claude-sonnet-4-20250514","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}`},
						{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
						{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Response"}}`},
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

			It("sends system prompt via the system parameter", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model: "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{
						{Role: "system", Content: "You are a helpful assistant."},
						{Role: "user", Content: "Hello"},
					},
				})
				Expect(err).NotTo(HaveOccurred())
				for v := range ch {
					_ = v
				}

				Expect(capturedBody).To(HaveKey("system"))
				systemBlocks, ok := capturedBody["system"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(systemBlocks).To(HaveLen(1))

				block, ok := systemBlocks[0].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(block["text"]).To(Equal("You are a helpful assistant."))
			})

			It("excludes system messages from the messages array", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model: "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{
						{Role: "system", Content: "You are a helpful assistant."},
						{Role: "user", Content: "Hello"},
					},
				})
				Expect(err).NotTo(HaveOccurred())
				for v := range ch {
					_ = v
				}

				messages, ok := capturedBody["messages"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(messages).To(HaveLen(1))

				firstMsg, ok := messages[0].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(firstMsg["role"]).To(Equal("user"))
			})
		})

		Context("when streaming request includes tools", func() {
			var capturedBody map[string]interface{}

			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					err = json.Unmarshal(body, &capturedBody)
					Expect(err).NotTo(HaveOccurred())

					w.Header().Set("Content-Type", "text/event-stream")
					events := []struct {
						eventType string
						data      string
					}{
						{"message_start", `{"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","model":"claude-sonnet-4-20250514","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}`},
						{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
						{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Response"}}`},
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

			It("sends tools via the tools parameter", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model: "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{
						{Role: "user", Content: "Run a command"},
					},
					Tools: []providerPkg.Tool{
						{
							Name:        "bash",
							Description: "Run a bash command",
							Schema: providerPkg.ToolSchema{
								Type: "object",
								Properties: map[string]interface{}{
									"command": map[string]interface{}{
										"type":        "string",
										"description": "The command to run",
									},
								},
								Required: []string{"command"},
							},
						},
					},
				})
				Expect(err).NotTo(HaveOccurred())
				for v := range ch {
					_ = v
				}

				Expect(capturedBody).To(HaveKey("tools"))
				tools, ok := capturedBody["tools"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(tools).To(HaveLen(1))

				tool, ok := tools[0].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(tool["name"]).To(Equal("bash"))
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

				for v := range ch {
					_ = v
				}

				_, open := <-ch
				Expect(open).To(BeFalse())
			})
		})
	})

	Describe("Request body structure", func() {
		var (
			server      *httptest.Server
			provider    *anthropic.Provider
			capturedReq map[string]interface{}
		)

		BeforeEach(func() {
			capturedReq = nil
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				Expect(err).NotTo(HaveOccurred())

				var fresh map[string]interface{}
				err = json.Unmarshal(body, &fresh)
				Expect(err).NotTo(HaveOccurred())
				capturedReq = fresh

				resp := map[string]interface{}{
					"id":    "msg_smoke",
					"type":  "message",
					"role":  "assistant",
					"model": "claude-sonnet-4-20250514",
					"content": []map[string]interface{}{
						{"type": "text", "text": "OK"},
					},
					"stop_reason": "end_turn",
					"usage":       map[string]interface{}{"input_tokens": 50, "output_tokens": 5},
				}
				w.Header().Set("Content-Type", "application/json")
				err = json.NewEncoder(w).Encode(resp)
				Expect(err).NotTo(HaveOccurred())
			}))

			var err error
			provider, err = anthropic.NewWithOptions("test-api-key", option.WithBaseURL(server.URL))
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when request combines system prompt, messages, and tools", func() {
			BeforeEach(func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model: "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{
						{Role: "system", Content: "You are a coding assistant."},
						{Role: "user", Content: "List files"},
						{Role: "assistant", Content: "I can help with that."},
						{Role: "user", Content: "Run ls -la"},
					},
					Tools: []providerPkg.Tool{
						{
							Name:        "bash",
							Description: "Run a bash command",
							Schema: providerPkg.ToolSchema{
								Type: "object",
								Properties: map[string]interface{}{
									"command": map[string]interface{}{
										"type":        "string",
										"description": "The command to run",
									},
								},
								Required: []string{"command"},
							},
						},
						{
							Name:        "read_file",
							Description: "Read a file from disk",
							Schema: providerPkg.ToolSchema{
								Type: "object",
								Properties: map[string]interface{}{
									"path": map[string]interface{}{
										"type":        "string",
										"description": "The file path to read",
									},
								},
								Required: []string{"path"},
							},
						},
					},
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("sets model as a string", func() {
				Expect(capturedReq["model"]).To(Equal("claude-sonnet-4-20250514"))
			})

			It("sets max_tokens to a positive integer", func() {
				maxTokens, ok := capturedReq["max_tokens"].(float64)
				Expect(ok).To(BeTrue())
				Expect(maxTokens).To(BeNumerically(">", 0))
			})

			It("sends system prompt via system parameter with correct structure", func() {
				Expect(capturedReq).To(HaveKey("system"))
				systemBlocks, ok := capturedReq["system"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(systemBlocks).To(HaveLen(1))

				block, ok := systemBlocks[0].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(block).To(HaveKeyWithValue("type", "text"))
				Expect(block).To(HaveKeyWithValue("text", "You are a coding assistant."))
			})

			It("excludes system role from messages array", func() {
				messages, ok := capturedReq["messages"].([]interface{})
				Expect(ok).To(BeTrue())

				for _, m := range messages {
					msg, ok := m.(map[string]interface{})
					Expect(ok).To(BeTrue())
					Expect(msg["role"]).NotTo(Equal("system"))
				}
			})

			It("includes only user and assistant messages in correct order", func() {
				messages, ok := capturedReq["messages"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(messages).To(HaveLen(3))

				roles := make([]string, 0, len(messages))
				for _, m := range messages {
					msg, ok := m.(map[string]interface{})
					Expect(ok).To(BeTrue())
					roles = append(roles, msg["role"].(string))
				}
				Expect(roles).To(Equal([]string{"user", "assistant", "user"}))
			})

			It("sends all tools with correct structure", func() {
				Expect(capturedReq).To(HaveKey("tools"))
				tools, ok := capturedReq["tools"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(tools).To(HaveLen(2))

				bashTool, ok := tools[0].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(bashTool["name"]).To(Equal("bash"))
				Expect(bashTool).To(HaveKey("description"))
				Expect(bashTool).To(HaveKey("input_schema"))

				schema, ok := bashTool["input_schema"].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(schema["type"]).To(Equal("object"))
				Expect(schema).To(HaveKey("properties"))
				Expect(schema).To(HaveKey("required"))
			})

			It("structures tool input_schema with properties and required fields", func() {
				tools, ok := capturedReq["tools"].([]interface{})
				Expect(ok).To(BeTrue())

				readTool, ok := tools[1].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(readTool["name"]).To(Equal("read_file"))

				schema, ok := readTool["input_schema"].(map[string]interface{})
				Expect(ok).To(BeTrue())

				props, ok := schema["properties"].(map[string]interface{})
				Expect(ok).To(BeTrue())
				pathProp, ok := props["path"].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(pathProp["type"]).To(Equal("string"))

				required, ok := schema["required"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(required).To(ContainElement("path"))
			})

			It("always includes temperature set to zero", func() {
				Expect(capturedReq).To(HaveKey("temperature"))
				temp, ok := capturedReq["temperature"].(float64)
				Expect(ok).To(BeTrue())
				Expect(temp).To(BeNumerically("==", 0))
			})

			It("includes cache_control on system prompt blocks", func() {
				Expect(capturedReq).To(HaveKey("system"))
				systemBlocks, ok := capturedReq["system"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(systemBlocks).To(HaveLen(1))

				block, ok := systemBlocks[0].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(block).To(HaveKey("cache_control"))

				cacheControl, ok := block["cache_control"].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(cacheControl["type"]).To(Equal("ephemeral"))
			})
		})

		Context("when request has no system prompt or tools", func() {
			BeforeEach(func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model: "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{
						{Role: "user", Content: "Hello"},
					},
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("omits system field from request body", func() {
				Expect(capturedReq).NotTo(HaveKey("system"))
			})

			It("omits tools field from request body", func() {
				Expect(capturedReq).NotTo(HaveKey("tools"))
			})

			It("always includes temperature even without system or tools", func() {
				Expect(capturedReq).To(HaveKey("temperature"))
				temp, ok := capturedReq["temperature"].(float64)
				Expect(ok).To(BeTrue())
				Expect(temp).To(BeNumerically("==", 0))
			})
		})

		Context("when assistant messages have empty content", func() {
			BeforeEach(func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model: "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{
						{Role: "user", Content: "Hello"},
						{Role: "assistant", Content: ""},
						{Role: "user", Content: "Are you there?"},
					},
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("skips empty-content assistant messages from the messages array", func() {
				messages, ok := capturedReq["messages"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(messages).To(HaveLen(2))

				roles := make([]string, 0, len(messages))
				for _, m := range messages {
					msg, ok := m.(map[string]interface{})
					Expect(ok).To(BeTrue())
					roles = append(roles, msg["role"].(string))
				}
				Expect(roles).To(Equal([]string{"user", "user"}))
			})
		})

		Context("when request contains tool call history", func() {
			BeforeEach(func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model: "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{
						{Role: "user", Content: "Run ls"},
						{
							Role: "assistant",
							ToolCalls: []providerPkg.ToolCall{
								{ID: "toolu_01", Name: "bash", Arguments: map[string]interface{}{"command": "ls"}},
							},
						},
						{
							Role:    "tool",
							Content: "file1.txt\nfile2.txt",
							ToolCalls: []providerPkg.ToolCall{
								{ID: "toolu_01", Name: "bash"},
							},
						},
						{Role: "user", Content: "What files are there?"},
					},
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("sends assistant message with tool_use block", func() {
				messages, ok := capturedReq["messages"].([]interface{})
				Expect(ok).To(BeTrue())

				var assistantMsg map[string]interface{}
				for _, m := range messages {
					msg := m.(map[string]interface{})
					if msg["role"] == "assistant" {
						assistantMsg = msg
					}
				}
				Expect(assistantMsg).NotTo(BeNil())

				content, ok := assistantMsg["content"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(content).NotTo(BeEmpty())

				block := content[0].(map[string]interface{})
				Expect(block["type"]).To(Equal("tool_use"))
				Expect(block["id"]).To(Equal("toolu_01"))
				Expect(block["name"]).To(Equal("bash"))
			})

			It("sends tool result as user message with tool_result block", func() {
				messages, ok := capturedReq["messages"].([]interface{})
				Expect(ok).To(BeTrue())

				var toolResultMsg map[string]interface{}
				for _, m := range messages {
					msg := m.(map[string]interface{})
					if msg["role"] == "user" {
						content, ok := msg["content"].([]interface{})
						if !ok || len(content) == 0 {
							continue
						}
						block, ok := content[0].(map[string]interface{})
						if ok && block["type"] == "tool_result" {
							toolResultMsg = msg
						}
					}
				}
				Expect(toolResultMsg).NotTo(BeNil())

				content := toolResultMsg["content"].([]interface{})
				block := content[0].(map[string]interface{})
				Expect(block["type"]).To(Equal("tool_result"))
				Expect(block["tool_use_id"]).To(Equal("toolu_01"))
				Expect(block["content"]).NotTo(BeEmpty())
			})

			It("does not return 400 Bad Request from Anthropic", func() {
				Expect(capturedReq).To(HaveKey("messages"))
			})
		})

		Context("when message sequence has consecutive user messages", func() {
			BeforeEach(func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model: "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{
						{Role: "user", Content: "first"},
						{Role: "user", Content: "second"},
						{Role: "user", Content: "third"},
					},
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("merges consecutive user messages into a single user message", func() {
				messages, ok := capturedReq["messages"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(messages).To(HaveLen(1))

				msg, ok := messages[0].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(msg["role"]).To(Equal("user"))

				content, ok := msg["content"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(content).To(HaveLen(1))

				block, ok := content[0].(map[string]interface{})
				Expect(ok).To(BeTrue())
				Expect(block["text"]).To(ContainSubstring("first"))
				Expect(block["text"]).To(ContainSubstring("second"))
				Expect(block["text"]).To(ContainSubstring("third"))
			})
		})

		Context("when alternating sequence has consecutive users before assistant", func() {
			BeforeEach(func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model: "claude-sonnet-4-20250514",
					Messages: []providerPkg.Message{
						{Role: "user", Content: "hello"},
						{Role: "user", Content: "hey"},
						{Role: "assistant", Content: "Hi there!"},
						{Role: "user", Content: "current"},
					},
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("produces valid alternating sequence", func() {
				messages, ok := capturedReq["messages"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(messages).To(HaveLen(3))

				roles := make([]string, 0, 3)
				for _, m := range messages {
					msg := m.(map[string]interface{})
					roles = append(roles, msg["role"].(string))
				}
				Expect(roles).To(Equal([]string{"user", "assistant", "user"}))
			})

			It("merges the two leading user messages into one", func() {
				messages, ok := capturedReq["messages"].([]interface{})
				Expect(ok).To(BeTrue())

				first := messages[0].(map[string]interface{})
				content := first["content"].([]interface{})
				block := content[0].(map[string]interface{})
				Expect(block["text"]).To(ContainSubstring("hello"))
				Expect(block["text"]).To(ContainSubstring("hey"))
			})
		})
	})
})
