package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

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
			Arguments: shared.ParseToolArguments(tc.Function.Arguments),
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
//   - providerName identifies the provider for error classification.
//
// Returns:
//   - A channel emitting provider.StreamChunk events as the stream progresses.
//
// Side effects:
//   - Spawns a goroutine and initiates network streaming.
func RunStream(
	ctx context.Context,
	client openaiAPI.Client,
	params openaiAPI.ChatCompletionNewParams,
	providerName string,
) <-chan provider.StreamChunk {
	ch := make(chan provider.StreamChunk, 16)
	go func() {
		defer close(ch)
		stream := client.Chat.Completions.NewStreaming(ctx, params)
		var acc openaiAPI.ChatCompletionAccumulator
		emitted := make(map[int]bool)
		for stream.Next() {
			chunk := stream.Current()
			acc.AddChunk(chunk)
			if len(chunk.Choices) > 0 {
				delta := chunk.Choices[0].Delta
				if delta.Content != "" {
					shared.SendChunk(ctx, ch, provider.StreamChunk{Content: delta.Content})
				}
			}
			if tc, ok := acc.JustFinishedToolCall(); ok {
				emitted[tc.Index] = true
				shared.SendChunk(ctx, ch, provider.StreamChunk{
					EventType: "tool_call",
					ToolCall: &provider.ToolCall{
						ID:        tc.ID,
						Name:      tc.Name,
						Arguments: shared.ParseToolArguments(tc.Arguments),
					},
				})
			}
			if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != "" {
				flushAccumulatedToolCalls(ctx, ch, &acc, emitted)
				shared.SendChunk(ctx, ch, provider.StreamChunk{Done: true})
			}
		}
		if err := stream.Err(); err != nil {
			shared.SendChunk(ctx, ch, provider.StreamChunk{Error: wrapStreamError(providerName, err), Done: true})
			return
		}
		flushAccumulatedToolCalls(ctx, ch, &acc, emitted)
	}()
	return ch
}

// wrapStreamError classifies a stream error as a *provider.Error.
// It delegates to WrapChatError for SDK-recognised errors (HTTP status errors,
// network errors) and falls back to an ErrorTypeUnknown *provider.Error for
// bare stream-decoder errors that WrapChatError cannot classify.
//
// Expected:
//   - providerName identifies the provider for error tagging.
//   - err is a non-nil error from stream.Err().
//
// Returns:
//   - A *provider.Error (always non-nil when err is non-nil).
//
// Side effects:
//   - None.
func wrapStreamError(providerName string, err error) error {
	if provErr := ParseProviderError(providerName, err); provErr != nil {
		return provErr
	}
	return &provider.Error{
		ErrorType: provider.ErrorTypeUnknown,
		Provider:  providerName,
		Message:   err.Error(),
		RawError:  err,
	}
}

// flushAccumulatedToolCalls emits any tool calls that the openai-go accumulator
// has recorded but RunStream has not yet forwarded via JustFinishedToolCall.
// This recovers tool calls from OpenAI-compatible providers whose stream shape
// never triggers the SDK's state-machine transitions — notably github-copilot,
// which combines the final tool_calls delta with finish_reason in one chunk,
// and zai, which emits an empty content field on every tool-call chunk.
//
// Expected:
//   - ctx is the caller context used to honour cancellation when sending.
//   - ch is the downstream StreamChunk channel.
//   - acc is the accumulator populated by stream.AddChunk calls.
//   - emitted tracks tool-call indexes that have already been forwarded so
//     callers do not see duplicate emissions for the happy path.
//
// Returns:
//   - None.
//
// Side effects:
//   - Sends one StreamChunk per previously unemitted tool call on ch and
//     marks that tool call as emitted.
func flushAccumulatedToolCalls(
	ctx context.Context,
	ch chan<- provider.StreamChunk,
	acc *openaiAPI.ChatCompletionAccumulator,
	emitted map[int]bool,
) {
	if acc == nil || len(acc.Choices) == 0 {
		return
	}
	toolCalls := acc.Choices[0].Message.ToolCalls
	for i := range toolCalls {
		if emitted[i] {
			continue
		}
		tc := toolCalls[i]
		if tc.ID == "" && tc.Function.Name == "" {
			continue
		}
		emitted[i] = true
		shared.SendChunk(ctx, ch, provider.StreamChunk{
			EventType: "tool_call",
			ToolCall: &provider.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: shared.ParseToolArguments(tc.Function.Arguments),
			},
		})
	}
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

// ParseProviderError extracts a *provider.Error from an OpenAI-compatible SDK error.
// It uses errors.As to retrieve the typed *openai.Error with HTTP status and error code.
//
// Expected:
//   - providerName is the provider identifier.
//   - err may be nil.
//
// Returns:
//   - A populated *provider.Error when the SDK error can be classified.
//   - Nil when err is nil or the error cannot be classified.
//
// Side effects:
//   - None.
func ParseProviderError(providerName string, err error) *provider.Error {
	if err == nil {
		return nil
	}

	var openaiErr *openaiAPI.Error
	if errors.As(err, &openaiErr) {
		errType, retriable := classifyHTTPStatus(openaiErr.StatusCode)
		return &provider.Error{
			HTTPStatus:  openaiErr.StatusCode,
			ErrorCode:   openaiErr.Code,
			ErrorType:   errType,
			Provider:    providerName,
			Message:     openaiErr.Message,
			IsRetriable: retriable,
			RawError:    err,
		}
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return &provider.Error{
			ErrorType:   provider.ErrorTypeNetworkError,
			Provider:    providerName,
			Message:     err.Error(),
			IsRetriable: true,
			RawError:    err,
		}
	}

	return nil
}

// classifyHTTPStatus maps an HTTP status code to an ErrorType and retriability flag.
//
// Expected:
//   - status is an HTTP status code returned by a provider.
//
// Returns:
//   - The matching provider.ErrorType and whether the error is retriable.
//
// Side effects:
//   - None.
func classifyHTTPStatus(status int) (provider.ErrorType, bool) {
	switch {
	case status == http.StatusTooManyRequests:
		return provider.ErrorTypeRateLimit, true
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return provider.ErrorTypeAuthFailure, false
	case status == http.StatusNotFound:
		return provider.ErrorTypeModelNotFound, false
	case status >= http.StatusInternalServerError:
		return provider.ErrorTypeServerError, true
	default:
		return provider.ErrorTypeUnknown, false
	}
}

// WrapChatError wraps a Chat() or Stream() error as a *provider.Error when possible.
//
// Expected:
//   - providerName is the provider identifier.
//   - err may be nil.
//
// Returns:
//   - Nil when err is nil.
//   - A *provider.Error when the error can be classified.
//   - The original error otherwise.
//
// Side effects:
//   - None.
func WrapChatError(providerName string, err error) error {
	if err == nil {
		return nil
	}

	if provErr := ParseProviderError(providerName, err); provErr != nil {
		return provErr
	}

	return err
}
