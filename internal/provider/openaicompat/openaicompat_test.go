package openaicompat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	anthropicAPI "github.com/anthropics/anthropic-sdk-go"
	ollamaAPI "github.com/ollama/ollama/api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
)

// GO: errors.As survives fmt.Errorf wrapping for all three SDKs.
// OpenAI exposes *openai.Error with StatusCode, Code, RawJSON(), DumpRequest(), and DumpResponse().
// Anthropic exposes *anthropic.Error with StatusCode, RequestID, RawJSON(), DumpRequest(), and DumpResponse(); the body must be parsed for error type/message.
// Ollama exposes *api.StatusError and *api.AuthorizationError with StatusCode plus Status/ErrorMessage or SigninURL; there is no raw body field.
var _ = Describe("OpenAI Compat", func() {
	Describe("BuildMessages", func() {
		Context("characterisation: role and content mapping", func() {
			It("maps user role and content to OpenAI UserMessage", func() {
				msgs := []provider.Message{{Role: "user", Content: "hello world"}}
				result := openaicompat.BuildMessages(msgs)
				Expect(result).To(HaveLen(1))
				Expect(result[0].OfUser).NotTo(BeNil())
				Expect(result[0].OfUser.Content.OfString.Value).To(Equal("hello world"))
			})

			It("maps assistant role and content to OpenAI AssistantMessage", func() {
				msgs := []provider.Message{{Role: "assistant", Content: "hi there"}}
				result := openaicompat.BuildMessages(msgs)
				Expect(result).To(HaveLen(1))
				Expect(result[0].OfAssistant).NotTo(BeNil())
				Expect(result[0].OfAssistant.Content.OfString.Value).To(Equal("hi there"))
			})

			It("maps system role and content to OpenAI SystemMessage", func() {
				msgs := []provider.Message{{Role: "system", Content: "you are helpful"}}
				result := openaicompat.BuildMessages(msgs)
				Expect(result).To(HaveLen(1))
				Expect(result[0].OfSystem).NotTo(BeNil())
				Expect(result[0].OfSystem.Content.OfString.Value).To(Equal("you are helpful"))
			})

			It("maps tool role using ToolCalls[0].ID for the OpenAI ToolMessage", func() {
				msgs := []provider.Message{{
					Role:      "tool",
					Content:   "tool result",
					ToolCalls: []provider.ToolCall{{ID: "call_123"}},
				}}
				result := openaicompat.BuildMessages(msgs)
				Expect(result).To(HaveLen(1))
				Expect(result[0].OfTool).NotTo(BeNil())
				Expect(result[0].OfTool.Content.OfString.Value).To(Equal("tool result"))
				Expect(result[0].OfTool.ToolCallID).To(Equal("call_123"))
			})
		})

		It("converts user messages correctly", func() {
			msgs := []provider.Message{{Role: "user", Content: "hello"}}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(1))
		})

		It("converts assistant messages correctly", func() {
			msgs := []provider.Message{{Role: "assistant", Content: "hi there"}}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(1))
		})

		It("converts system messages correctly", func() {
			msgs := []provider.Message{{Role: "system", Content: "you are helpful"}}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(1))
		})

		It("converts tool messages with ToolCalls ID", func() {
			msgs := []provider.Message{{
				Role:      "tool",
				Content:   "tool result",
				ToolCalls: []provider.ToolCall{{ID: "call_123"}},
			}}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(1))
		})

		It("returns empty slice for empty input", func() {
			result := openaicompat.BuildMessages([]provider.Message{})
			Expect(result).To(BeEmpty())
		})

		It("skips unknown roles", func() {
			msgs := []provider.Message{
				{Role: "user", Content: "hello"},
				{Role: "unknown", Content: "ignored"},
				{Role: "assistant", Content: "hi"},
			}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(2))
		})

		It("skips tool messages without ToolCalls", func() {
			msgs := []provider.Message{{Role: "tool", Content: "orphan result"}}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(BeEmpty())
		})

		It("preserves tool calls on assistant messages", func() {
			msgs := []provider.Message{{
				Role:    "assistant",
				Content: "Let me check the weather",
				ToolCalls: []provider.ToolCall{{
					ID:        "call_abc",
					Name:      "get_weather",
					Arguments: map[string]interface{}{"city": "London"},
				}},
			}}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(1))
			toolCalls := result[0].GetToolCalls()
			Expect(toolCalls).To(HaveLen(1))
			Expect(toolCalls[0].ID).To(Equal("call_abc"))
			Expect(toolCalls[0].Function.Name).To(Equal("get_weather"))
		})

		It("preserves tool calls on assistant message with empty content", func() {
			msgs := []provider.Message{{
				Role:    "assistant",
				Content: "",
				ToolCalls: []provider.ToolCall{{
					ID:        "call_xyz",
					Name:      "search",
					Arguments: map[string]interface{}{"query": "golang"},
				}},
			}}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(1))
			toolCalls := result[0].GetToolCalls()
			Expect(toolCalls).To(HaveLen(1))
			Expect(toolCalls[0].ID).To(Equal("call_xyz"))
			Expect(toolCalls[0].Function.Name).To(Equal("search"))
		})

		It("converts multiple mixed messages", func() {
			msgs := []provider.Message{
				{Role: "system", Content: "be helpful"},
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "hi"},
			}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(3))
		})
	})

	Describe("BuildTools", func() {
		Context("characterisation: multi-property schema mapping", func() {
			It("preserves all properties and required fields in the OpenAI parameters wrapper", func() {
				tools := []provider.Tool{{
					Name:        "search",
					Description: "Search for items",
					Schema: provider.ToolSchema{
						Type: "object",
						Properties: map[string]interface{}{
							"query": map[string]interface{}{"type": "string"},
							"limit": map[string]interface{}{"type": "integer"},
						},
						Required: []string{"query", "limit"},
					},
				}}
				result := openaicompat.BuildTools(tools)
				Expect(result).To(HaveLen(1))
				Expect(result[0].Function.Name).To(Equal("search"))
				Expect(result[0].Function.Description.Value).To(Equal("Search for items"))
				params := result[0].Function.Parameters
				Expect(params).To(HaveKey("properties"))
				Expect(params).To(HaveKey("required"))
				Expect(params["required"]).To(ConsistOf("query", "limit"))
			})
		})

		It("returns nil for empty tools slice", func() {
			result := openaicompat.BuildTools([]provider.Tool{})
			Expect(result).To(BeNil())
		})

		It("returns nil for nil tools slice", func() {
			result := openaicompat.BuildTools(nil)
			Expect(result).To(BeNil())
		})

		It("converts a single tool with all schema fields", func() {
			tools := []provider.Tool{{
				Name:        "get_weather",
				Description: "Get the weather",
				Schema: provider.ToolSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"location": map[string]interface{}{"type": "string"},
					},
					Required: []string{"location"},
				},
			}}
			result := openaicompat.BuildTools(tools)
			Expect(result).To(HaveLen(1))
			Expect(result[0].Function.Name).To(Equal("get_weather"))
		})

		It("converts multiple tools", func() {
			tools := []provider.Tool{
				{
					Name:        "tool_a",
					Description: "First tool",
					Schema:      provider.ToolSchema{Type: "object"},
				},
				{
					Name:        "tool_b",
					Description: "Second tool",
					Schema:      provider.ToolSchema{Type: "object"},
				},
			}
			result := openaicompat.BuildTools(tools)
			Expect(result).To(HaveLen(2))
			Expect(result[0].Function.Name).To(Equal("tool_a"))
			Expect(result[1].Function.Name).To(Equal("tool_b"))
		})
	})

	Describe("BuildParams", func() {
		It("sets model and messages", func() {
			req := provider.ChatRequest{
				Model: "gpt-4o",
				Messages: []provider.Message{
					{Role: "user", Content: "hello"},
				},
			}
			params := openaicompat.BuildParams(req)
			Expect(params.Model).To(Equal("gpt-4o"))
			Expect(params.Messages).To(HaveLen(1))
		})

		It("includes tools when present", func() {
			req := provider.ChatRequest{
				Model: "gpt-4o",
				Messages: []provider.Message{
					{Role: "user", Content: "hello"},
				},
				Tools: []provider.Tool{{
					Name:        "my_tool",
					Description: "A tool",
					Schema:      provider.ToolSchema{Type: "object"},
				}},
			}
			params := openaicompat.BuildParams(req)
			Expect(params.Tools).To(HaveLen(1))
		})

		It("omits tools when empty", func() {
			req := provider.ChatRequest{
				Model:    "gpt-4o",
				Messages: []provider.Message{{Role: "user", Content: "hi"}},
			}
			params := openaicompat.BuildParams(req)
			Expect(params.Tools).To(BeNil())
		})
	})

	Describe("ExtractToolCalls", func() {
		It("returns nil for empty slice", func() {
			result := openaicompat.ExtractToolCalls([]openaiAPI.ChatCompletionMessageToolCall{})
			Expect(result).To(BeNil())
		})

		It("returns nil for nil slice", func() {
			result := openaicompat.ExtractToolCalls(nil)
			Expect(result).To(BeNil())
		})

		It("converts a single tool call with ID, Name, and Arguments", func() {
			tc := unmarshalToolCall(`{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"London\"}"}}`)
			result := openaicompat.ExtractToolCalls([]openaiAPI.ChatCompletionMessageToolCall{tc})
			Expect(result).To(HaveLen(1))
			Expect(result[0].ID).To(Equal("call_1"))
			Expect(result[0].Name).To(Equal("get_weather"))
			Expect(result[0].Arguments).To(HaveKeyWithValue("city", "London"))
		})

		It("converts multiple tool calls", func() {
			tc1 := unmarshalToolCall(`{"id":"call_1","type":"function","function":{"name":"tool_a","arguments":"{}"}}`)
			tc2 := unmarshalToolCall(`{"id":"call_2","type":"function","function":{"name":"tool_b","arguments":"{\"x\":1}"}}`)
			result := openaicompat.ExtractToolCalls([]openaiAPI.ChatCompletionMessageToolCall{tc1, tc2})
			Expect(result).To(HaveLen(2))
			Expect(result[0].ID).To(Equal("call_1"))
			Expect(result[0].Name).To(Equal("tool_a"))
			Expect(result[1].ID).To(Equal("call_2"))
			Expect(result[1].Name).To(Equal("tool_b"))
		})
	})

	Describe("ParseChatResponse", func() {
		It("returns ErrNoChoices for nil response", func() {
			_, err := openaicompat.ParseChatResponse(nil)
			Expect(err).To(MatchError(provider.ErrNoChoices))
		})

		It("returns ErrNoChoices for empty choices", func() {
			resp := unmarshalCompletion(`{"id":"cmpl-1","model":"gpt-4o","choices":[],"object":"chat.completion","created":1}`)
			_, err := openaicompat.ParseChatResponse(resp)
			Expect(err).To(MatchError(provider.ErrNoChoices))
		})

		It("parses text response with role, content, and usage", func() {
			resp := unmarshalCompletion(`{
				"id":"cmpl-1","model":"gpt-4o","object":"chat.completion","created":1,
				"choices":[{"index":0,"message":{"role":"assistant","content":"Hello there"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
			}`)
			result, err := openaicompat.ParseChatResponse(resp)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Message.Role).To(Equal("assistant"))
			Expect(result.Message.Content).To(Equal("Hello there"))
			Expect(result.Usage.PromptTokens).To(Equal(10))
			Expect(result.Usage.CompletionTokens).To(Equal(5))
			Expect(result.Usage.TotalTokens).To(Equal(15))
		})

		It("parses response with tool calls", func() {
			resp := unmarshalCompletion(`{
				"id":"cmpl-1","model":"gpt-4o","object":"chat.completion","created":1,
				"choices":[{"index":0,"message":{
					"role":"assistant","content":"",
					"tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]
				},"finish_reason":"tool_calls"}],
				"usage":{"prompt_tokens":20,"completion_tokens":10,"total_tokens":30}
			}`)
			result, err := openaicompat.ParseChatResponse(resp)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Message.Role).To(Equal("assistant"))
			Expect(result.Message.ToolCalls).To(HaveLen(1))
			Expect(result.Message.ToolCalls[0].ID).To(Equal("call_abc"))
			Expect(result.Message.ToolCalls[0].Name).To(Equal("get_weather"))
			Expect(result.Message.ToolCalls[0].Arguments).To(HaveKeyWithValue("city", "Paris"))
		})

		It("returns nil tool calls when response has no tool calls", func() {
			resp := unmarshalCompletion(`{
				"id":"cmpl-1","model":"gpt-4o","object":"chat.completion","created":1,
				"choices":[{"index":0,"message":{"role":"assistant","content":"plain text"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}
			}`)
			result, err := openaicompat.ParseChatResponse(resp)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Message.ToolCalls).To(BeNil())
		})
	})
})

var _ = Describe("Spike: SDK error type introspection", func() {
	It("extracts OpenAI typed errors through wrapping", func() {
		err := newOpenAIError(`{"message":"invalid request","param":"model","type":"invalid_request_error","code":"invalid_model"}`, http.StatusBadRequest)
		var extracted *openaiAPI.Error
		Expect(errors.As(fmt.Errorf("openai provider: %w", err), &extracted)).To(BeTrue())
		if extracted == nil {
			Fail("expected OpenAI error to be extracted")
		}
		Expect(extracted.StatusCode).To(Equal(http.StatusBadRequest))
		Expect(extracted.Code).To(Equal("invalid_model"))
		Expect(extracted.RawJSON()).To(ContainSubstring(`"code":"invalid_model"`))
	})

	It("extracts Anthropic typed errors through wrapping", func() {
		err := newAnthropicError(`{"message":"rate limited","type":"rate_limit_error"}`, http.StatusTooManyRequests, "req_123")
		var extracted *anthropicAPI.Error
		Expect(errors.As(fmt.Errorf("anthropic provider: %w", err), &extracted)).To(BeTrue())
		if extracted == nil {
			Fail("expected Anthropic error to be extracted")
		}
		Expect(extracted.StatusCode).To(Equal(http.StatusTooManyRequests))
		Expect(extracted.RequestID).To(Equal("req_123"))
		Expect(extracted.RawJSON()).To(ContainSubstring(`"rate_limit_error"`))
	})

	It("extracts Ollama typed errors through wrapping", func() {
		err := &ollamaAPI.StatusError{StatusCode: http.StatusNotFound, Status: "404 Not Found", ErrorMessage: "model not found"}
		var extracted *ollamaAPI.StatusError
		Expect(errors.As(fmt.Errorf("ollama provider: %w", err), &extracted)).To(BeTrue())
		if extracted == nil {
			Fail("expected Ollama status error to be extracted")
		}
		Expect(extracted.StatusCode).To(Equal(http.StatusNotFound))
		Expect(extracted.ErrorMessage).To(Equal("model not found"))
	})

	It("extracts Ollama authorisation errors through wrapping", func() {
		err := &ollamaAPI.AuthorizationError{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized", SigninURL: "https://ollama.com/signin"}
		var extracted *ollamaAPI.AuthorizationError
		Expect(errors.As(fmt.Errorf("ollama provider: %w", err), &extracted)).To(BeTrue())
		if extracted == nil {
			Fail("expected Ollama authorisation error to be extracted")
		}
		Expect(extracted.StatusCode).To(Equal(http.StatusUnauthorized))
		Expect(extracted.SigninURL).To(Equal("https://ollama.com/signin"))
	})
})

// ---
// RunStream streaming specs.
var _ = Describe("RunStream", func() {
	var server *httptest.Server

	AfterEach(func() {
		if server != nil {
			server.Close()
		}
	})

	It("streams text content chunks", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			chunks := []string{
				`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
				`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world!"},"finish_reason":null}]}`,
				`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "Hello"}},
		})
		ch := openaicompat.RunStream(ctx, client, params)
		var chunks []provider.StreamChunk
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}
		Expect(chunks).To(HaveLen(3))
		Expect(chunks[0].Content).To(Equal("Hello"))
		Expect(chunks[1].Content).To(Equal(" world!"))
		Expect(chunks[2].Done).To(BeTrue())
	})

	It("streams tool call chunks", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			chunks := []string{
				`{"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"London\"}"}}]},"finish_reason":null}]}`,
				`{"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "Weather?"}},
		})
		ch := openaicompat.RunStream(ctx, client, params)
		var chunks []provider.StreamChunk
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}
		Expect(chunks).To(HaveLen(2))
		Expect(chunks[0].ToolCall).NotTo(BeNil())
		Expect(chunks[0].ToolCall.ID).To(Equal("call_abc"))
		Expect(chunks[0].ToolCall.Name).To(Equal("get_weather"))
		Expect(chunks[0].ToolCall.Arguments).To(HaveKeyWithValue("city", "London"))
		Expect(chunks[1].Done).To(BeTrue())
	})

	PIt("emits tool calls when the terminal chunk combines delta and finish_reason (github-copilot shape)", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			chunks := []string{
				`{"id":"chatcmpl-copilot","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"delegate","arguments":""}}]},"finish_reason":null}]}`,
				`{"id":"chatcmpl-copilot","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"agent\":\"Explore\"}"}}]},"finish_reason":"tool_calls"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "delegate please"}},
		})
		ch := openaicompat.RunStream(ctx, client, params)
		var collected []provider.StreamChunk
		for chunk := range ch {
			collected = append(collected, chunk)
		}
		var toolCalls []provider.ToolCall
		for _, c := range collected {
			if c.ToolCall != nil {
				toolCalls = append(toolCalls, *c.ToolCall)
			}
		}
		Expect(toolCalls).To(HaveLen(1))
		Expect(toolCalls[0].Name).To(Equal("delegate"))
		Expect(toolCalls[0].ID).To(Equal("call_x"))
		Expect(toolCalls[0].Arguments).To(HaveKeyWithValue("agent", "Explore"))
	})

	PIt("emits tool calls when every chunk carries empty content alongside tool_calls (zai shape)", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			chunks := []string{
				`{"id":"chatcmpl-zai","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"","tool_calls":[{"index":0,"id":"call_y","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
				`{"id":"chatcmpl-zai","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"","tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"x.txt\"}"}}]},"finish_reason":"tool_calls"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "read file"}},
		})
		ch := openaicompat.RunStream(ctx, client, params)
		var collected []provider.StreamChunk
		for chunk := range ch {
			collected = append(collected, chunk)
		}
		var toolCalls []provider.ToolCall
		for _, c := range collected {
			if c.ToolCall != nil {
				toolCalls = append(toolCalls, *c.ToolCall)
			}
		}
		Expect(toolCalls).To(HaveLen(1))
		Expect(toolCalls[0].Name).To(Equal("read_file"))
		Expect(toolCalls[0].ID).To(Equal("call_y"))
		Expect(toolCalls[0].Arguments).To(HaveKeyWithValue("path", "x.txt"))
	})

	It("propagates server errors", func() {
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
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "fail"}},
		})
		ch := openaicompat.RunStream(ctx, client, params)
		var lastChunk provider.StreamChunk
		for chunk := range ch {
			lastChunk = chunk
		}
		Expect(lastChunk.Error).To(HaveOccurred())
		Expect(lastChunk.Done).To(BeTrue())
	})

	It("respects context cancellation", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			for range 10 {
				fmt.Fprintf(w, "data: %s\n\n", `{"id":"chatcmpl-3","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"chunk"},"finish_reason":null}]}`)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(50 * time.Millisecond)
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "cancel"}},
		})
		ch := openaicompat.RunStream(ctx, client, params)
		var gotCancel bool
		for chunk := range ch {
			if chunk.Error != nil && ctx.Err() != nil {
				gotCancel = true
			}
		}
		Expect(gotCancel).To(BeTrue())
	})

	It("handles empty stream", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "empty"}},
		})
		ch := openaicompat.RunStream(ctx, client, params)
		var chunks []provider.StreamChunk
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}
		Expect(chunks).To(BeEmpty())
	})
})

var _ = Describe("ParseProviderError", func() {
	const testProvider = "test-provider"

	Context("when error is nil", func() {
		It("returns nil", func() {
			Expect(openaicompat.ParseProviderError(testProvider, nil)).To(Succeed())
		})
	})

	Context("when error is an OpenAI SDK error", func() {
		It("classifies 429 as rate limit and retriable", func() {
			err := newOpenAIError(`{"message":"rate limited","type":"rate_limit","code":"rate_limit_exceeded"}`, http.StatusTooManyRequests)
			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.HTTPStatus).To(Equal(http.StatusTooManyRequests))
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeRateLimit))
			Expect(result.IsRetriable).To(BeTrue())
			Expect(result.Provider).To(Equal(testProvider))
			Expect(result.ErrorCode).To(Equal("rate_limit_exceeded"))
			Expect(result.Message).To(Equal("rate limited"))
			Expect(result.RawError).To(Equal(err))
		})

		It("classifies 401 as auth failure and not retriable", func() {
			err := newOpenAIError(`{"message":"invalid key","type":"auth","code":"invalid_api_key"}`, http.StatusUnauthorized)
			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeAuthFailure))
			Expect(result.IsRetriable).To(BeFalse())
		})

		It("classifies 403 as auth failure and not retriable", func() {
			err := newOpenAIError(`{"message":"forbidden","type":"auth","code":"forbidden"}`, http.StatusForbidden)
			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeAuthFailure))
			Expect(result.IsRetriable).To(BeFalse())
		})

		It("classifies 404 as model not found and not retriable", func() {
			err := newOpenAIError(`{"message":"model not found","type":"not_found","code":"model_not_found"}`, http.StatusNotFound)
			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeModelNotFound))
			Expect(result.IsRetriable).To(BeFalse())
		})

		It("classifies 500 as server error and retriable", func() {
			err := newOpenAIError(`{"message":"internal error","type":"server_error","code":"server_error"}`, http.StatusInternalServerError)
			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeServerError))
			Expect(result.IsRetriable).To(BeTrue())
		})

		It("classifies 503 as server error and retriable", func() {
			err := newOpenAIError(`{"message":"unavailable","type":"server_error","code":"unavailable"}`, http.StatusServiceUnavailable)
			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeServerError))
			Expect(result.IsRetriable).To(BeTrue())
		})

		It("survives fmt.Errorf wrapping", func() {
			inner := newOpenAIError(`{"message":"rate limited","type":"rate_limit","code":"rate_limit"}`, http.StatusTooManyRequests)
			wrapped := fmt.Errorf("provider call: %w", inner)
			result := openaicompat.ParseProviderError(testProvider, wrapped)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeRateLimit))
		})

		It("classifies unknown status as unknown and not retriable", func() {
			err := newOpenAIError(`{"message":"teapot","type":"unknown","code":"teapot"}`, http.StatusTeapot)
			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeUnknown))
			Expect(result.IsRetriable).To(BeFalse())
		})
	})

	Context("when error is a network error", func() {
		It("classifies url.Error as network error and retriable", func() {
			netErr := &url.Error{Op: "Post", URL: "https://api.openai.com/v1/chat", Err: errors.New("connection refused")}
			result := openaicompat.ParseProviderError(testProvider, netErr)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeNetworkError))
			Expect(result.IsRetriable).To(BeTrue())
			Expect(result.Provider).To(Equal(testProvider))
		})
	})

	Context("when error is unrecognised", func() {
		It("returns nil for a plain error", func() {
			Expect(openaicompat.ParseProviderError(testProvider, errors.New("something"))).To(Succeed())
		})
	})
})

var _ = Describe("WrapChatError", func() {
	const testProvider = "test-provider"

	It("returns nil for nil error", func() {
		Expect(openaicompat.WrapChatError(testProvider, nil)).To(Succeed())
	})

	It("wraps an OpenAI SDK error as *provider.Error", func() {
		inner := newOpenAIError(`{"message":"rate limited","type":"rate_limit","code":"rate_limit"}`, http.StatusTooManyRequests)
		result := openaicompat.WrapChatError(testProvider, inner)
		Expect(result).To(HaveOccurred())
		var provErr *provider.Error
		Expect(errors.As(result, &provErr)).To(BeTrue())
		Expect(provErr.ErrorType).To(Equal(provider.ErrorTypeRateLimit))
	})

	It("returns the original error when unrecognised", func() {
		plain := errors.New("something unexpected")
		result := openaicompat.WrapChatError(testProvider, plain)
		Expect(result).To(Equal(plain))
	})
})

func unmarshalToolCall(raw string) openaiAPI.ChatCompletionMessageToolCall {
	var tc openaiAPI.ChatCompletionMessageToolCall
	if err := json.Unmarshal([]byte(raw), &tc); err != nil {
		panic("failed to unmarshal tool call: " + err.Error())
	}
	return tc
}

func unmarshalCompletion(raw string) *openaiAPI.ChatCompletion {
	var resp openaiAPI.ChatCompletion
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		panic("failed to unmarshal completion: " + err.Error())
	}
	return &resp
}

func newOpenAIError(body string, statusCode int) *openaiAPI.Error {
	var err openaiAPI.Error
	if uErr := json.Unmarshal([]byte(body), &err); uErr != nil {
		panic("failed to unmarshal openai error: " + uErr.Error())
	}
	err.StatusCode = statusCode
	err.Request = httptest.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", http.NoBody)
	err.Response = &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	return &err
}

func newAnthropicError(body string, statusCode int, requestID string) *anthropicAPI.Error {
	var err anthropicAPI.Error
	if uErr := json.Unmarshal([]byte(body), &err); uErr != nil {
		panic("failed to unmarshal anthropic error: " + uErr.Error())
	}
	err.StatusCode = statusCode
	err.RequestID = requestID
	err.Request = httptest.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", http.NoBody)
	err.Response = &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	return &err
}
