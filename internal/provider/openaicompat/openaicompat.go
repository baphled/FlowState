package openaicompat

import (
	"context"
	"encoding/json"

	"github.com/baphled/flowstate/internal/provider"
	shared "github.com/baphled/flowstate/internal/provider/shared"
	openaiAPI "github.com/openai/openai-go"
)

// BuildMessages converts a slice of provider.Message to OpenAI-compatible message parameters.
//
// Expected:
//   - messages is a slice of provider.Message in FlowState's internal format.
//
// Returns:
//   - A slice of OpenAI API message parameter unions for chat completions.
//
// Side effects:
//   - None.
func BuildMessages(messages []provider.Message) []openaiAPI.ChatCompletionMessageParamUnion {
	result := make([]openaiAPI.ChatCompletionMessageParamUnion, 0, len(messages))
	pairs := shared.ConvertMessagesToRolePairs(messages)
	for i, pair := range pairs {
		m := messages[i]
		switch pair.Role {
		case "user":
			result = append(result, openaiAPI.UserMessage(pair.Content))
		case "assistant":
			if len(m.ToolCalls) > 0 {
				result = append(result, buildAssistantMessageWithToolCalls(m))
			} else {
				result = append(result, openaiAPI.AssistantMessage(pair.Content))
			}
		case "system":
			result = append(result, openaiAPI.SystemMessage(pair.Content))
		case "tool":
			if len(m.ToolCalls) > 0 {
				result = append(result, openaiAPI.ToolMessage(pair.Content, m.ToolCalls[0].ID))
			}
		}
	}
	return result
}

// buildAssistantMessageWithToolCalls constructs an assistant message parameter
// that includes tool calls for the OpenAI wire format.
//
// Expected:
//   - m is a provider Message with Role "assistant" and at least one ToolCall entry.
//
// Returns:
//   - A ChatCompletionMessageParamUnion wrapping an assistant message with ToolCalls set.
//
// Side effects:
//   - None.
func buildAssistantMessageWithToolCalls(m provider.Message) openaiAPI.ChatCompletionMessageParamUnion {
	toolCalls := make([]openaiAPI.ChatCompletionMessageToolCallParam, 0, len(m.ToolCalls))
	for _, tc := range m.ToolCalls {
		argsJSON, err := json.Marshal(tc.Arguments)
		if err != nil {
			argsJSON = []byte("{}")
		}
		toolCalls = append(toolCalls, openaiAPI.ChatCompletionMessageToolCallParam{
			ID: tc.ID,
			Function: openaiAPI.ChatCompletionMessageToolCallFunctionParam{
				Name:      tc.Name,
				Arguments: string(argsJSON),
			},
		})
	}
	msg := openaiAPI.ChatCompletionAssistantMessageParam{
		ToolCalls: toolCalls,
	}
	if m.Content != "" {
		msg.Content = openaiAPI.ChatCompletionAssistantMessageParamContentUnion{
			OfString: openaiAPI.String(m.Content),
		}
	}
	return openaiAPI.ChatCompletionMessageParamUnion{OfAssistant: &msg}
}

// BuildTools converts provider tools to OpenAI-compatible tool parameters.
//
// Expected:
//   - tools is a slice of provider.Tool definitions.
//
// Returns:
//   - A slice of OpenAI ChatCompletionToolParam, or nil if tools is empty.
//
// Side effects:
//   - None.
func BuildTools(tools []provider.Tool) []openaiAPI.ChatCompletionToolParam {
	if len(tools) == 0 {
		return nil
	}
	result := make([]openaiAPI.ChatCompletionToolParam, 0, len(tools))
	for _, t := range tools {
		base := shared.BuildBaseToolSchema(t)
		result = append(result, openaiAPI.ChatCompletionToolParam{
			Function: openaiAPI.FunctionDefinitionParam{
				Name:        base.Name,
				Description: openaiAPI.String(base.Description),
				Parameters: openaiAPI.FunctionParameters{
					"type":       t.Schema.Type,
					"properties": base.Properties,
					"required":   base.Required,
				},
			},
		})
	}
	return result
}

// BuildParams constructs OpenAI chat completion parameters from a ChatRequest.
//
// Expected:
//   - req is a provider.ChatRequest with model, messages, and optional tools.
//
// Returns:
//   - An OpenAI ChatCompletionNewParams struct ready for API submission.
//
// Side effects:
//   - None.
func BuildParams(req provider.ChatRequest) openaiAPI.ChatCompletionNewParams {
	params := openaiAPI.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: BuildMessages(req.Messages),
	}
	if tools := BuildTools(req.Tools); len(tools) > 0 {
		params.Tools = tools
	}
	return params
}

// ParseToolCallArguments parses raw JSON tool call arguments into a map.
//
// Expected:
//   - raw is a JSON string of tool call arguments.
//
// Returns:
//   - A map of argument names to values, or an empty map on parse failure.
//
// Side effects:
//   - None.
func ParseToolCallArguments(raw string) map[string]interface{} {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return map[string]interface{}{}
	}
	return args
}

// ExtractToolCalls converts OpenAI tool call objects to provider ToolCall objects.
//
// Expected:
//   - toolCalls is a slice of OpenAI ChatCompletionMessageToolCall from a response.
//
// Returns:
//   - A slice of provider.ToolCall, or nil if input is empty.
//
// Side effects:
//   - None.
func ExtractToolCalls(toolCalls []openaiAPI.ChatCompletionMessageToolCall) []provider.ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	result := make([]provider.ToolCall, 0, len(toolCalls))
	for i := range toolCalls {
		tc := toolCalls[i]
		result = append(result, provider.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: ParseToolCallArguments(tc.Function.Arguments),
		})
	}
	return result
}

// RunStream executes a streaming chat completion and emits StreamChunk events.
//
// Expected:
//   - ctx is a valid context for the API call.
//   - client is an initialised OpenAI API client.
//   - params contains the chat completion parameters.
//
// Returns:
//   - A channel emitting provider.StreamChunk events as the stream progresses.
//
// Side effects:
//   - Spawns a goroutine and initiates network streaming.
func RunStream(ctx context.Context, client openaiAPI.Client, params openaiAPI.ChatCompletionNewParams) <-chan provider.StreamChunk {
	ch := make(chan provider.StreamChunk, 16)
	go func() {
		defer close(ch)
		stream := client.Chat.Completions.NewStreaming(ctx, params)
		var acc openaiAPI.ChatCompletionAccumulator
		for stream.Next() {
			chunk := stream.Current()
			acc.AddChunk(chunk)
			if len(chunk.Choices) > 0 {
				delta := chunk.Choices[0].Delta
				if delta.Content != "" {
					ch <- provider.StreamChunk{Content: delta.Content}
				}
			}
			if tc, ok := acc.JustFinishedToolCall(); ok {
				ch <- provider.StreamChunk{
					ToolCall: &provider.ToolCall{
						ID:        tc.ID,
						Name:      tc.Name,
						Arguments: ParseToolCallArguments(tc.Arguments),
					},
				}
			}
			if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != "" {
				ch <- provider.StreamChunk{Done: true}
			}
		}
		if err := stream.Err(); err != nil {
			ch <- provider.StreamChunk{Error: err, Done: true}
		}
	}()
	return ch
}

// ParseChatResponse converts an OpenAI chat completion to a provider ChatResponse.
//
// Expected:
//   - resp is a pointer to an OpenAI ChatCompletion (may be nil).
//
// Returns:
//   - A provider.ChatResponse with message content, tool calls, and usage.
//   - provider.ErrNoChoices if resp is nil or has no choices.
//
// Side effects:
//   - None.
func ParseChatResponse(resp *openaiAPI.ChatCompletion) (provider.ChatResponse, error) {
	if resp == nil || len(resp.Choices) == 0 {
		return provider.ChatResponse{}, provider.ErrNoChoices
	}
	msg := resp.Choices[0].Message
	return provider.ChatResponse{
		Message: provider.Message{
			Role:      string(msg.Role),
			Content:   msg.Content,
			ToolCalls: ExtractToolCalls(msg.ToolCalls),
		},
		Usage: provider.Usage{
			PromptTokens:     int(resp.Usage.PromptTokens),
			CompletionTokens: int(resp.Usage.CompletionTokens),
			TotalTokens:      int(resp.Usage.TotalTokens),
		},
	}, nil
}
