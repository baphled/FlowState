package openaicompat_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openaiAPI "github.com/openai/openai-go"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
)

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

	Describe("ParseToolCallArguments", func() {
		It("parses valid JSON into map", func() {
			result := openaicompat.ParseToolCallArguments(`{"key":"value","num":42}`)
			Expect(result).To(HaveKeyWithValue("key", "value"))
			Expect(result).To(HaveKeyWithValue("num", BeNumerically("==", 42)))
		})

		It("returns empty map for invalid JSON", func() {
			result := openaicompat.ParseToolCallArguments("not json")
			Expect(result).To(BeEmpty())
		})

		It("returns empty map for empty string", func() {
			result := openaicompat.ParseToolCallArguments("")
			Expect(result).To(BeEmpty())
		})

		It("parses nested JSON structures", func() {
			result := openaicompat.ParseToolCallArguments(`{"outer":{"inner":"deep"}}`)
			Expect(result).To(HaveKey("outer"))
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
