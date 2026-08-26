package ollama_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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

		Context("when assistant message in history has tool calls", func() {
			var capturedMessages []map[string]interface{}

			BeforeEach(func() {
				capturedMessages = nil
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					var req map[string]interface{}
					err = json.Unmarshal(body, &req)
					Expect(err).NotTo(HaveOccurred())

					msgs := req["messages"].([]interface{})
					for _, m := range msgs {
						capturedMessages = append(capturedMessages, m.(map[string]interface{}))
					}

					resp := map[string]interface{}{
						"model": "llama3.2",
						"message": map[string]interface{}{
							"role":    "assistant",
							"content": "Tool call acknowledged",
						},
						"done":              true,
						"prompt_eval_count": 5,
						"eval_count":        5,
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("includes tool calls in the assistant message sent to Ollama", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model: "llama3.2",
					Messages: []providerPkg.Message{
						{Role: "user", Content: "What is the weather?"},
						{
							Role:    "assistant",
							Content: "",
							ToolCalls: []providerPkg.ToolCall{{
								ID:        "call_weather",
								Name:      "get_weather",
								Arguments: map[string]interface{}{"location": "London"},
							}},
						},
						{Role: "tool", Content: `{"temperature": "15C"}`,
							ToolCalls: []providerPkg.ToolCall{{ID: "call_weather"}}},
					},
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(capturedMessages).To(HaveLen(3))
				assistantMsg := capturedMessages[1]
				Expect(assistantMsg["role"]).To(Equal("assistant"))
				toolCalls, ok := assistantMsg["tool_calls"].([]interface{})
				Expect(ok).To(BeTrue(), "assistant message should have tool_calls")
				Expect(toolCalls).To(HaveLen(1))
				fn := toolCalls[0].(map[string]interface{})["function"].(map[string]interface{})
				Expect(fn["name"]).To(Equal("get_weather"))
			})
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

			It("returns a structured provider error", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model:    "llama3.2",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).To(HaveOccurred())

				var provErr *providerPkg.Error
				Expect(errors.As(err, &provErr)).To(BeTrue())
				Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeServerError))
				Expect(provErr.Provider).To(Equal("ollama"))
			})
		})

		Context("when server returns 401", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns authentication error", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model:    "llama3.2",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when server returns 429", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(`{"error": "rate limit exceeded"}`))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns rate limit error", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model:    "llama3.2",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when given a multi-role conversation", func() {
			var capturedMessages []map[string]interface{}

			BeforeEach(func() {
				capturedMessages = nil
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					var req map[string]interface{}
					err = json.Unmarshal(body, &req)
					Expect(err).NotTo(HaveOccurred())

					msgs := req["messages"].([]interface{})
					for _, m := range msgs {
						capturedMessages = append(capturedMessages, m.(map[string]interface{}))
					}

					resp := map[string]interface{}{
						"model": "llama3.2",
						"message": map[string]interface{}{
							"role":    "assistant",
							"content": "Done",
						},
						"done":              true,
						"prompt_eval_count": 5,
						"eval_count":        5,
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(resp)
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("sends correct role and content for all message types", func() {
				ctx := context.Background()
				_, err := provider.Chat(ctx, providerPkg.ChatRequest{
					Model: "llama3.2",
					Messages: []providerPkg.Message{
						{Role: "system", Content: "You are helpful."},
						{Role: "user", Content: "What is the weather?"},
						{Role: "assistant", Content: "Let me check."},
						{Role: "tool", Content: `{"temperature": "15C"}`},
					},
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(capturedMessages).To(HaveLen(4))
				Expect(capturedMessages[0]["role"]).To(Equal("system"))
				Expect(capturedMessages[0]["content"]).To(Equal("You are helpful."))
				Expect(capturedMessages[1]["role"]).To(Equal("user"))
				Expect(capturedMessages[1]["content"]).To(Equal("What is the weather?"))
				Expect(capturedMessages[2]["role"]).To(Equal("assistant"))
				Expect(capturedMessages[2]["content"]).To(Equal("Let me check."))
				Expect(capturedMessages[3]["role"]).To(Equal("tool"))
				Expect(capturedMessages[3]["content"]).To(Equal(`{"temperature": "15C"}`))
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

		Context("when assistant message in history has tool calls", func() {
			var capturedMessages []map[string]interface{}

			BeforeEach(func() {
				capturedMessages = nil
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					var req map[string]interface{}
					err = json.Unmarshal(body, &req)
					Expect(err).NotTo(HaveOccurred())

					msgs := req["messages"].([]interface{})
					for _, m := range msgs {
						capturedMessages = append(capturedMessages, m.(map[string]interface{}))
					}

					w.Header().Set("Content-Type", "application/x-ndjson")
					resp := map[string]interface{}{
						"model":   "llama3.2",
						"message": map[string]interface{}{"role": "assistant", "content": "Done"},
						"done":    true,
					}
					data, _ := json.Marshal(resp)
					_, _ = w.Write(data)
					_, _ = w.Write([]byte("\n"))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("includes tool calls in the assistant message sent to Ollama", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model: "llama3.2",
					Messages: []providerPkg.Message{
						{Role: "user", Content: "What is the weather?"},
						{
							Role:    "assistant",
							Content: "",
							ToolCalls: []providerPkg.ToolCall{{
								ID:        "call_weather",
								Name:      "get_weather",
								Arguments: map[string]interface{}{"location": "London"},
							}},
						},
						{Role: "tool", Content: `{"temperature": "15C"}`,
							ToolCalls: []providerPkg.ToolCall{{ID: "call_weather"}}},
					},
				})
				Expect(err).NotTo(HaveOccurred())

				for v := range ch {
					_ = v
				}

				Expect(capturedMessages).To(HaveLen(3))
				assistantMsg := capturedMessages[1]
				Expect(assistantMsg["role"]).To(Equal("assistant"))
				toolCalls, ok := assistantMsg["tool_calls"].([]interface{})
				Expect(ok).To(BeTrue(), "assistant message should have tool_calls")
				Expect(toolCalls).To(HaveLen(1))
				fn := toolCalls[0].(map[string]interface{})["function"].(map[string]interface{})
				Expect(fn["name"]).To(Equal("get_weather"))
			})
		})

		Context("when given a multi-role conversation", func() {
			var capturedMessages []map[string]interface{}

			BeforeEach(func() {
				capturedMessages = nil
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					var req map[string]interface{}
					err = json.Unmarshal(body, &req)
					Expect(err).NotTo(HaveOccurred())

					msgs := req["messages"].([]interface{})
					for _, m := range msgs {
						capturedMessages = append(capturedMessages, m.(map[string]interface{}))
					}

					w.Header().Set("Content-Type", "application/x-ndjson")
					resp := map[string]interface{}{
						"model":   "llama3.2",
						"message": map[string]interface{}{"role": "assistant", "content": "Done"},
						"done":    true,
					}
					data, _ := json.Marshal(resp)
					_, _ = w.Write(data)
					_, _ = w.Write([]byte("\n"))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("sends correct role and content for all message types", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model: "llama3.2",
					Messages: []providerPkg.Message{
						{Role: "system", Content: "You are helpful."},
						{Role: "user", Content: "What is the weather?"},
						{Role: "assistant", Content: "Let me check."},
						{Role: "tool", Content: `{"temperature": "15C"}`},
					},
				})
				Expect(err).NotTo(HaveOccurred())

				for v := range ch {
					_ = v
				}

				Expect(capturedMessages).To(HaveLen(4))
				Expect(capturedMessages[0]["role"]).To(Equal("system"))
				Expect(capturedMessages[0]["content"]).To(Equal("You are helpful."))
				Expect(capturedMessages[1]["role"]).To(Equal("user"))
				Expect(capturedMessages[1]["content"]).To(Equal("What is the weather?"))
				Expect(capturedMessages[2]["role"]).To(Equal("assistant"))
				Expect(capturedMessages[2]["content"]).To(Equal("Let me check."))
				Expect(capturedMessages[3]["role"]).To(Equal("tool"))
				Expect(capturedMessages[3]["content"]).To(Equal(`{"temperature": "15C"}`))
			})
		})

		Context("when tools are provided", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					var req map[string]interface{}
					err = json.Unmarshal(body, &req)
					Expect(err).NotTo(HaveOccurred())

					tools, ok := req["tools"].([]interface{})
					Expect(ok).To(BeTrue(), "tools should be present in request")
					Expect(tools).To(HaveLen(1))

					tool := tools[0].(map[string]interface{})
					Expect(tool["type"]).To(Equal("function"))
					fn := tool["function"].(map[string]interface{})
					Expect(fn["name"]).To(Equal("get_weather"))
					Expect(fn["description"]).To(Equal("Get current weather"))

					w.Header().Set("Content-Type", "application/x-ndjson")
					resp := map[string]interface{}{
						"model":   "llama3.2",
						"message": map[string]interface{}{"role": "assistant", "content": "Done"},
						"done":    true,
					}
					data, _ := json.Marshal(resp)
					_, _ = w.Write(data)
					_, _ = w.Write([]byte("\n"))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("sends tool schemas to Ollama", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "llama3.2",
					Messages: []providerPkg.Message{{Role: "user", Content: "What is the weather?"}},
					Tools: []providerPkg.Tool{
						{
							Name:        "get_weather",
							Description: "Get current weather",
							Schema: providerPkg.ToolSchema{
								Type: "object",
								Properties: map[string]interface{}{
									"location": map[string]interface{}{
										"type":        "string",
										"description": "City name",
									},
								},
								Required: []string{"location"},
							},
						},
					},
				})
				Expect(err).NotTo(HaveOccurred())

				for v := range ch {
					_ = v
					_ = 0
				}
			})
		})

		Context("characterisation: buildOllamaTools maps name, description, and required via shared.BuildBaseToolSchema", func() {
			var capturedReq map[string]interface{}

			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					err = json.Unmarshal(body, &capturedReq)
					Expect(err).NotTo(HaveOccurred())

					w.Header().Set("Content-Type", "application/x-ndjson")
					resp := map[string]interface{}{
						"model":   "llama3.2",
						"message": map[string]interface{}{"role": "assistant", "content": "Done"},
						"done":    true,
					}
					data, _ := json.Marshal(resp)
					_, _ = w.Write(data)
					_, _ = w.Write([]byte("\n"))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("sends correct name, description, and required fields for each tool", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "llama3.2",
					Messages: []providerPkg.Message{{Role: "user", Content: "help"}},
					Tools: []providerPkg.Tool{
						{
							Name:        "search",
							Description: "Search for items",
							Schema: providerPkg.ToolSchema{
								Type: "object",
								Properties: map[string]interface{}{
									"query": map[string]interface{}{"type": "string"},
									"limit": map[string]interface{}{"type": "integer"},
								},
								Required: []string{"query", "limit"},
							},
						},
					},
				})
				Expect(err).NotTo(HaveOccurred())
				for v := range ch {
					_ = v
				}

				tools, ok := capturedReq["tools"].([]interface{})
				Expect(ok).To(BeTrue(), "tools should be present in request")
				Expect(tools).To(HaveLen(1))

				tool := tools[0].(map[string]interface{})
				fn := tool["function"].(map[string]interface{})
				Expect(fn["name"]).To(Equal("search"))
				Expect(fn["description"]).To(Equal("Search for items"))

				params := fn["parameters"].(map[string]interface{})
				required := params["required"].([]interface{})
				Expect(required).To(ConsistOf("query", "limit"))
			})
		})

		Context("when server returns tool call response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/x-ndjson")

					resp := map[string]interface{}{
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
					}
					data, _ := json.Marshal(resp)
					_, _ = w.Write(data)
					_, _ = w.Write([]byte("\n"))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns tool_call chunk when Ollama returns tool call", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "llama3.2",
					Messages: []providerPkg.Message{{Role: "user", Content: "What is the weather in London?"}},
				})
				Expect(err).NotTo(HaveOccurred())

				var toolCallChunks []providerPkg.StreamChunk
				for chunk := range ch {
					if chunk.EventType == "tool_call" {
						toolCallChunks = append(toolCallChunks, chunk)
					}
				}

				Expect(toolCallChunks).To(HaveLen(1))
				Expect(toolCallChunks[0].ToolCall).NotTo(BeNil())
				Expect(toolCallChunks[0].ToolCall.Name).To(Equal("get_weather"))
				Expect(toolCallChunks[0].ToolCall.Arguments["location"]).To(Equal("London"))
			})
		})

		Context("when message has system role", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					var req map[string]interface{}
					err = json.Unmarshal(body, &req)
					Expect(err).NotTo(HaveOccurred())

					messages := req["messages"].([]interface{})
					Expect(messages).To(HaveLen(2))

					sysMsg := messages[0].(map[string]interface{})
					Expect(sysMsg["role"]).To(Equal("system"))
					Expect(sysMsg["content"]).To(Equal("You are a helpful assistant"))

					userMsg := messages[1].(map[string]interface{})
					Expect(userMsg["role"]).To(Equal("user"))

					w.Header().Set("Content-Type", "application/x-ndjson")
					resp := map[string]interface{}{
						"model":   "llama3.2",
						"message": map[string]interface{}{"role": "assistant", "content": "Hello!"},
						"done":    true,
					}
					data, _ := json.Marshal(resp)
					_, _ = w.Write(data)
					_, _ = w.Write([]byte("\n"))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("handles system message role", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model: "llama3.2",
					Messages: []providerPkg.Message{
						{Role: "system", Content: "You are a helpful assistant"},
						{Role: "user", Content: "Hello"},
					},
				})
				Expect(err).NotTo(HaveOccurred())

				for v := range ch {
					_ = v
					_ = 0
				}
			})
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

		Context("when server returns 401", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns authentication error via channel", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "llama3.2",
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
					_, _ = w.Write([]byte(`{"error": "rate limit exceeded"}`))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns rate limit error via channel", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "llama3.2",
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
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns timeout error via channel", func() {
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
				Expect(lastChunk.Error).To(HaveOccurred())
			})
		})

		Context("when streaming completes successfully", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/x-ndjson")
					resp := map[string]interface{}{
						"model":   "llama3.2",
						"message": map[string]interface{}{"role": "assistant", "content": "Done"},
						"done":    true,
					}
					data, _ := json.Marshal(resp)
					_, _ = w.Write(data)
					_, _ = w.Write([]byte("\n"))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("closes channel after completion", func() {
				ctx := context.Background()
				ch, err := provider.Stream(ctx, providerPkg.ChatRequest{
					Model:    "llama3.2",
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

		Context("when server returns 401", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns authentication error", func() {
				ctx := context.Background()
				_, err := provider.Embed(ctx, providerPkg.EmbedRequest{
					Model: "llama3.2",
					Input: "test input",
				})
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when server returns 429", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(`{"error": "rate limit exceeded"}`))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns rate limit error", func() {
				ctx := context.Background()
				_, err := provider.Embed(ctx, providerPkg.EmbedRequest{
					Model: "llama3.2",
					Input: "test input",
				})
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("error classification", func() {
		Describe("via Chat", func() {
			var (
				server *httptest.Server
				prov   *ollama.Provider
			)

			AfterEach(func() {
				if server != nil {
					server.Close()
				}
			})

			Context("when connection is refused", func() {
				BeforeEach(func() {
					server = httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
					closedURL := server.URL
					server.Close()
					server = nil

					var err error
					prov, err = ollama.NewWithClient(closedURL, &http.Client{Timeout: 2 * time.Second})
					Expect(err).NotTo(HaveOccurred())
				})

				It("returns NetworkError provider error", func() {
					_, err := prov.Chat(context.Background(), providerPkg.ChatRequest{
						Model:    "llama3.2",
						Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
					})
					Expect(err).To(HaveOccurred())

					var provErr *providerPkg.Error
					Expect(errors.As(err, &provErr)).To(BeTrue())
					Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeNetworkError))
					Expect(provErr.Provider).To(Equal("ollama"))
					Expect(provErr.IsRetriable).To(BeTrue())
				})
			})

			Context("when server returns 404", func() {
				BeforeEach(func() {
					server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(`{"error": "model 'nonexistent' not found"}`))
					}))

					var err error
					prov, err = ollama.NewWithClient(server.URL, server.Client())
					Expect(err).NotTo(HaveOccurred())
				})

				It("returns ModelNotFound provider error", func() {
					_, err := prov.Chat(context.Background(), providerPkg.ChatRequest{
						Model:    "nonexistent",
						Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
					})
					Expect(err).To(HaveOccurred())

					var provErr *providerPkg.Error
					Expect(errors.As(err, &provErr)).To(BeTrue())
					Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeModelNotFound))
					Expect(provErr.HTTPStatus).To(Equal(404))
					Expect(provErr.Provider).To(Equal("ollama"))
					Expect(provErr.IsRetriable).To(BeFalse())
				})
			})

			Context("when server returns 503", func() {
				BeforeEach(func() {
					server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = w.Write([]byte(`{"error": "service unavailable"}`))
					}))

					var err error
					prov, err = ollama.NewWithClient(server.URL, server.Client())
					Expect(err).NotTo(HaveOccurred())
				})

				It("returns ServerError provider error", func() {
					_, err := prov.Chat(context.Background(), providerPkg.ChatRequest{
						Model:    "llama3.2",
						Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
					})
					Expect(err).To(HaveOccurred())

					var provErr *providerPkg.Error
					Expect(errors.As(err, &provErr)).To(BeTrue())
					Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeServerError))
					Expect(provErr.HTTPStatus).To(Equal(503))
					Expect(provErr.Provider).To(Equal("ollama"))
					Expect(provErr.IsRetriable).To(BeTrue())
				})
			})

			Context("when server returns 503 with loading message", func() {
				BeforeEach(func() {
					server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = w.Write([]byte(`{"error": "model is still loading into memory"}`))
					}))

					var err error
					prov, err = ollama.NewWithClient(server.URL, server.Client())
					Expect(err).NotTo(HaveOccurred())
				})

				It("returns Overload provider error", func() {
					_, err := prov.Chat(context.Background(), providerPkg.ChatRequest{
						Model:    "llama3.2",
						Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
					})
					Expect(err).To(HaveOccurred())

					var provErr *providerPkg.Error
					Expect(errors.As(err, &provErr)).To(BeTrue())
					Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeOverload))
					Expect(provErr.HTTPStatus).To(Equal(503))
					Expect(provErr.Provider).To(Equal("ollama"))
					Expect(provErr.IsRetriable).To(BeTrue())
				})
			})

			Context("when server returns 503 with busy message", func() {
				BeforeEach(func() {
					server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = w.Write([]byte(`{"error": "server is busy, try again later"}`))
					}))

					var err error
					prov, err = ollama.NewWithClient(server.URL, server.Client())
					Expect(err).NotTo(HaveOccurred())
				})

				It("returns Overload provider error", func() {
					_, err := prov.Chat(context.Background(), providerPkg.ChatRequest{
						Model:    "llama3.2",
						Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
					})
					Expect(err).To(HaveOccurred())

					var provErr *providerPkg.Error
					Expect(errors.As(err, &provErr)).To(BeTrue())
					Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeOverload))
					Expect(provErr.HTTPStatus).To(Equal(503))
					Expect(provErr.Provider).To(Equal("ollama"))
					Expect(provErr.IsRetriable).To(BeTrue())
				})
			})

			Context("when server returns 503 with overloaded message", func() {
				BeforeEach(func() {
					server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = w.Write([]byte(`{"error": "system overloaded"}`))
					}))

					var err error
					prov, err = ollama.NewWithClient(server.URL, server.Client())
					Expect(err).NotTo(HaveOccurred())
				})

				It("returns Overload provider error", func() {
					_, err := prov.Chat(context.Background(), providerPkg.ChatRequest{
						Model:    "llama3.2",
						Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
					})
					Expect(err).To(HaveOccurred())

					var provErr *providerPkg.Error
					Expect(errors.As(err, &provErr)).To(BeTrue())
					Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeOverload))
					Expect(provErr.HTTPStatus).To(Equal(503))
					Expect(provErr.Provider).To(Equal("ollama"))
					Expect(provErr.IsRetriable).To(BeTrue())
				})
			})

			Context("when server returns 503 with connection refused message", func() {
				BeforeEach(func() {
					server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = w.Write([]byte(`{"error": "connection refused"}`))
					}))

					var err error
					prov, err = ollama.NewWithClient(server.URL, server.Client())
					Expect(err).NotTo(HaveOccurred())
				})

				It("returns ServerError provider error (not overload)", func() {
					_, err := prov.Chat(context.Background(), providerPkg.ChatRequest{
						Model:    "llama3.2",
						Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
					})
					Expect(err).To(HaveOccurred())

					var provErr *providerPkg.Error
					Expect(errors.As(err, &provErr)).To(BeTrue())
					Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeServerError))
					Expect(provErr.HTTPStatus).To(Equal(503))
					Expect(provErr.Provider).To(Equal("ollama"))
					Expect(provErr.IsRetriable).To(BeTrue())
				})
			})

			Context("when server returns 401", func() {
				BeforeEach(func() {
					server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusUnauthorized)
						_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
					}))

					var err error
					prov, err = ollama.NewWithClient(server.URL, server.Client())
					Expect(err).NotTo(HaveOccurred())
				})

				It("returns AuthFailure provider error", func() {
					_, err := prov.Chat(context.Background(), providerPkg.ChatRequest{
						Model:    "llama3.2",
						Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
					})
					Expect(err).To(HaveOccurred())

					var provErr *providerPkg.Error
					Expect(errors.As(err, &provErr)).To(BeTrue())
					Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeAuthFailure))
					Expect(provErr.HTTPStatus).To(Equal(401))
					Expect(provErr.Provider).To(Equal("ollama"))
					Expect(provErr.IsRetriable).To(BeFalse())
				})
			})

			Context("when no error occurs", func() {
				BeforeEach(func() {
					server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						resp := map[string]interface{}{
							"model":             "llama3.2",
							"message":           map[string]interface{}{"role": "assistant", "content": "Hi"},
							"done":              true,
							"prompt_eval_count": 5,
							"eval_count":        3,
						}
						w.Header().Set("Content-Type", "application/json")
						_ = json.NewEncoder(w).Encode(resp)
					}))

					var err error
					prov, err = ollama.NewWithClient(server.URL, server.Client())
					Expect(err).NotTo(HaveOccurred())
				})

				It("returns nil error", func() {
					_, err := prov.Chat(context.Background(), providerPkg.ChatRequest{
						Model:    "llama3.2",
						Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
					})
					Expect(err).NotTo(HaveOccurred())
				})
			})
		})

		Describe("via Stream", func() {
			var (
				server *httptest.Server
				prov   *ollama.Provider
			)

			AfterEach(func() {
				if server != nil {
					server.Close()
				}
			})

			Context("when server returns 404", func() {
				BeforeEach(func() {
					server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(`{"error": "model 'nonexistent' not found"}`))
					}))

					var err error
					prov, err = ollama.NewWithClient(server.URL, server.Client())
					Expect(err).NotTo(HaveOccurred())
				})

				It("sends ModelNotFound provider error via channel", func() {
					ch, err := prov.Stream(context.Background(), providerPkg.ChatRequest{
						Model:    "nonexistent",
						Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
					})
					Expect(err).NotTo(HaveOccurred())

					var lastChunk providerPkg.StreamChunk
					for chunk := range ch {
						lastChunk = chunk
					}
					Expect(lastChunk.Error).To(HaveOccurred())
					Expect(lastChunk.Done).To(BeTrue())

					var provErr *providerPkg.Error
					Expect(errors.As(lastChunk.Error, &provErr)).To(BeTrue())
					Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeModelNotFound))
					Expect(provErr.HTTPStatus).To(Equal(404))
					Expect(provErr.Provider).To(Equal("ollama"))
				})
			})

			Context("when server returns 401", func() {
				BeforeEach(func() {
					server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusUnauthorized)
						_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
					}))

					var err error
					prov, err = ollama.NewWithClient(server.URL, server.Client())
					Expect(err).NotTo(HaveOccurred())
				})

				It("sends AuthFailure provider error via channel", func() {
					ch, err := prov.Stream(context.Background(), providerPkg.ChatRequest{
						Model:    "llama3.2",
						Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
					})
					Expect(err).NotTo(HaveOccurred())

					var lastChunk providerPkg.StreamChunk
					for chunk := range ch {
						lastChunk = chunk
					}
					Expect(lastChunk.Error).To(HaveOccurred())
					Expect(lastChunk.Done).To(BeTrue())

					var provErr *providerPkg.Error
					Expect(errors.As(lastChunk.Error, &provErr)).To(BeTrue())
					Expect(provErr.ErrorType).To(Equal(providerPkg.ErrorTypeAuthFailure))
					Expect(provErr.HTTPStatus).To(Equal(401))
					Expect(provErr.Provider).To(Equal("ollama"))
				})
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

		Context("when server returns 401", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns authentication error", func() {
				_, err := provider.Models()
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when server returns 429", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(`{"error": "rate limit exceeded"}`))
				}))

				var err error
				provider, err = ollama.NewWithClient(server.URL, server.Client())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns rate limit error", func() {
				_, err := provider.Models()
				Expect(err).To(HaveOccurred())
			})
		})
	})

	// Plan §6 task-15 — defence-in-depth document-skip. PDFs reaching
	// the Ollama translator are dropped with a structured slog.Warn
	// (the Ollama SDK has no Documents field). The upload-time gate
	// is the primary defence; this closes R13's
	// model-switch-mid-staging window.
	Describe("Defence-in-depth document-skip (PR4 task-15, AC-15-LogShape-Pinned)", func() {
		var server *httptest.Server
		var provider *ollama.Provider

		BeforeEach(func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				resp := map[string]any{
					"model":             "llama3.2",
					"message":           map[string]any{"role": "assistant", "content": "ok"},
					"done":              true,
					"prompt_eval_count": 1,
					"eval_count":        1,
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}))
			var err error
			provider, err = ollama.NewWithClient(server.URL, server.Client())
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		It("emits slog.Warn with the AC-15-LogShape-Pinned 4-field schema for a Kind=document attachment", func() {
			var buf bytes.Buffer
			handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
			prev := slog.Default()
			slog.SetDefault(slog.New(handler))
			defer slog.SetDefault(prev)

			_, err := provider.Chat(context.Background(), providerPkg.ChatRequest{
				Model: "llama3.2",
				Messages: []providerPkg.Message{
					{Role: "user", Content: "x", Attachments: []providerPkg.Attachment{
						{ID: "doc-abc", Kind: "document", MediaType: "application/pdf"},
					}},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			var entry map[string]any
			Expect(json.Unmarshal(buf.Bytes(), &entry)).To(Succeed())
			Expect(entry).To(HaveKeyWithValue("msg",
				"attachment_dropped: provider does not support documents"))
			Expect(entry).To(HaveKeyWithValue("provider", "ollama"))
			Expect(entry).To(HaveKeyWithValue("attachment_id", "doc-abc"))
			Expect(entry).To(HaveKeyWithValue("kind", "document"))
			Expect(entry).To(HaveKeyWithValue("media_type", "application/pdf"))
			Expect(entry).To(HaveKeyWithValue("level", "WARN"))
		})
	})

	// PR3 deferral closure — image attachments thread into
	// ollamaAPI.Message.Images []ImageData (raw bytes, NOT data: URLs).
	// The Ollama SDK marshals []byte values as base64-encoded JSON
	// strings on the wire, so specs assert against decoded payloads.
	// Multimodal models (llama3.2-vision, llava, etc.) consume Images
	// server-side; non-multimodal models silently ignore the field.
	Describe("Image attachment threading (PR3 deferral closure)", func() {
		var (
			server           *httptest.Server
			provider         *ollama.Provider
			capturedMessages []map[string]interface{}
		)

		// imagesFor decodes the captured wire-level `images` array on
		// the given message into raw []byte slices for comparison.
		// Returns nil when the key is absent (omitempty drops the field
		// for messages with no images).
		imagesFor := func(msg map[string]interface{}) [][]byte {
			raw, ok := msg["images"]
			if !ok || raw == nil {
				return nil
			}
			arr, ok := raw.([]interface{})
			if !ok {
				return nil
			}
			out := make([][]byte, 0, len(arr))
			for _, entry := range arr {
				s, ok := entry.(string)
				Expect(ok).To(BeTrue(), "images entries must marshal as base64 strings")
				decoded, err := base64.StdEncoding.DecodeString(s)
				Expect(err).NotTo(HaveOccurred())
				out = append(out, decoded)
			}
			return out
		}

		BeforeEach(func() {
			capturedMessages = nil
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				Expect(err).NotTo(HaveOccurred())

				var req map[string]interface{}
				err = json.Unmarshal(body, &req)
				Expect(err).NotTo(HaveOccurred())

				msgs := req["messages"].([]interface{})
				for _, m := range msgs {
					capturedMessages = append(capturedMessages, m.(map[string]interface{}))
				}

				resp := map[string]interface{}{
					"model": "llama3.2-vision",
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "ack",
					},
					"done":              true,
					"prompt_eval_count": 1,
					"eval_count":        1,
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}))

			var err error
			provider, err = ollama.NewWithClient(server.URL, server.Client())
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		It("threads a single image attachment into Images[0] with raw bytes", func() {
			pngBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x01}
			_, err := provider.Chat(context.Background(), providerPkg.ChatRequest{
				Model: "llama3.2-vision",
				Messages: []providerPkg.Message{
					{Role: "user", Content: "describe", Attachments: []providerPkg.Attachment{
						{ID: "img-1", Kind: "image", MediaType: "image/png", Data: pngBytes},
					}},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(capturedMessages).To(HaveLen(1))
			imgs := imagesFor(capturedMessages[0])
			Expect(imgs).To(HaveLen(1))
			Expect(imgs[0]).To(Equal(pngBytes))
		})

		It("threads multiple images in declaration order", func() {
			first := []byte{0x89, 0x50, 0x4E, 0x47, 0xAA}
			second := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0xBB}
			third := []byte{0x52, 0x49, 0x46, 0x46, 0xCC}
			_, err := provider.Chat(context.Background(), providerPkg.ChatRequest{
				Model: "llama3.2-vision",
				Messages: []providerPkg.Message{
					{Role: "user", Content: "compare", Attachments: []providerPkg.Attachment{
						{ID: "img-a", Kind: "image", MediaType: "image/png", Data: first},
						{ID: "img-b", Kind: "image", MediaType: "image/jpeg", Data: second},
						{ID: "img-c", Kind: "image", MediaType: "image/webp", Data: third},
					}},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(capturedMessages).To(HaveLen(1))
			imgs := imagesFor(capturedMessages[0])
			Expect(imgs).To(HaveLen(3))
			Expect(imgs[0]).To(Equal(first))
			Expect(imgs[1]).To(Equal(second))
			Expect(imgs[2]).To(Equal(third))
		})

		It("defaults empty Kind to image (PR1 backwards-compat)", func() {
			payload := []byte{0x89, 0x50, 0x4E, 0x47, 0x42, 0x43}
			_, err := provider.Chat(context.Background(), providerPkg.ChatRequest{
				Model: "llama3.2-vision",
				Messages: []providerPkg.Message{
					{Role: "user", Content: "legacy", Attachments: []providerPkg.Attachment{
						{ID: "img-legacy", Kind: "", MediaType: "image/png", Data: payload},
					}},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(capturedMessages).To(HaveLen(1))
			imgs := imagesFor(capturedMessages[0])
			Expect(imgs).To(HaveLen(1))
			Expect(imgs[0]).To(Equal(payload))
		})

		It("preserves PR4 document-skip when image and document attachments are mixed", func() {
			var buf bytes.Buffer
			handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
			prev := slog.Default()
			slog.SetDefault(slog.New(handler))
			defer slog.SetDefault(prev)

			imgBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x11, 0x22}
			_, err := provider.Chat(context.Background(), providerPkg.ChatRequest{
				Model: "llama3.2-vision",
				Messages: []providerPkg.Message{
					{Role: "user", Content: "mixed", Attachments: []providerPkg.Attachment{
						{ID: "img-mix", Kind: "image", MediaType: "image/png", Data: imgBytes},
						{ID: "doc-mix", Kind: "document", MediaType: "application/pdf"},
					}},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Image threaded to Images[].
			Expect(capturedMessages).To(HaveLen(1))
			imgs := imagesFor(capturedMessages[0])
			Expect(imgs).To(HaveLen(1))
			Expect(imgs[0]).To(Equal(imgBytes))

			// Document slog.Warn'd with the AC-15-LogShape-Pinned schema.
			var entry map[string]any
			Expect(json.Unmarshal(buf.Bytes(), &entry)).To(Succeed())
			Expect(entry).To(HaveKeyWithValue("msg",
				"attachment_dropped: provider does not support documents"))
			Expect(entry).To(HaveKeyWithValue("provider", "ollama"))
			Expect(entry).To(HaveKeyWithValue("attachment_id", "doc-mix"))
			Expect(entry).To(HaveKeyWithValue("kind", "document"))
			Expect(entry).To(HaveKeyWithValue("media_type", "application/pdf"))
		})

		It("omits Images when no attachments are present", func() {
			_, err := provider.Chat(context.Background(), providerPkg.ChatRequest{
				Model: "llama3.2",
				Messages: []providerPkg.Message{
					{Role: "user", Content: "plain text"},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(capturedMessages).To(HaveLen(1))
			// `images` is `omitempty` on ollamaAPI.Message — the key
			// must not appear in the wire payload when no attachments
			// thread through.
			_, present := capturedMessages[0]["images"]
			Expect(present).To(BeFalse(), "images key must be absent for attachment-free messages")
		})
	})
})
