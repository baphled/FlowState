package openaicompat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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
			// Plan "Chat Attachments Backend (May 2026)" §6 task-11 /
			// task-12 — when the user message carries image
			// attachments, lift them into the OpenAI multimodal
			// content-block shape ({type: image_url, image_url: {
			// url: data:<media>;base64,<encoded>}}) ahead of the
			// text block. Empty / partial entries are skipped so
			// the request stays well-formed.
			//
			// Inherits Anthropic's image-first ordering convention
			// (the vision-prompt convention is "image then
			// instruction"). When no attachments are present, the
			// existing string-shaped UserMessage is preserved
			// unchanged for back-compat with every pre-PR3 test.
			if blocks := buildUserAttachmentParts(m); len(blocks) > 0 {
				result = append(result, openaiAPI.UserMessage(blocks))
				continue
			}
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
			// One OpenAI tool message per tool-call id. Historically only
			// ToolCalls[0].ID was emitted, which silently dropped results for
			// messages that bundled multiple tool calls.
			for _, tc := range m.ToolCalls {
				wireID := shared.TranslateToolCallID(tc.ID, shared.ToolIDTargetOpenAI)
				result = append(result, openaiAPI.ToolMessage(pair.Content, wireID))
			}
		default:
			// M4-adjacent observability (May 2026): the manager seam
			// canonicalises every Role to {user, assistant, system, tool}.
			// Silent-drop behaviour is preserved — adding a Warn surfaces
			// any future canonicalisation regression at runtime instead of
			// vanishing into the void.
			slog.Warn("openaicompat: dropped message with unknown role",
				"role", pair.Role,
			)
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
			ID: shared.TranslateToolCallID(tc.ID, shared.ToolIDTargetOpenAI),
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

// buildUserAttachmentParts lifts a user message's Attachments into the
// OpenAI multimodal content-part shape. Returns nil when the message
// carries no attachments OR when every attachment is incomplete (empty
// MediaType or empty Data) — the caller falls back to the string-shaped
// UserMessage in that case, preserving the pre-PR3 behaviour.
//
// Plan "Chat Attachments Backend (May 2026)" §6 task-11 / task-12. The
// helper consumes only the agnostic provider.Attachment slice and never
// returns the SDK type across the engine boundary (the SDK union is the
// caller's concern). Image-first ordering follows the vision-prompt
// convention ("image then instruction").
//
// Expected:
//   - m is a user-role message; Attachments may be nil or empty.
//   - Each Attachment.Data carries the raw image bytes (NOT
//     base64-encoded); this function does the base64 encoding into
//     a data-URL for the image_url part.
//
// Returns:
//   - A slice of content-part unions in [image_1, image_2, ..., text]
//     order, or nil when no usable attachments are present.
//
// Side effects:
//   - None.
func buildUserAttachmentParts(m provider.Message) []openaiAPI.ChatCompletionContentPartUnionParam {
	if len(m.Attachments) == 0 {
		return nil
	}
	parts := make([]openaiAPI.ChatCompletionContentPartUnionParam, 0, len(m.Attachments)+1)
	for _, a := range m.Attachments {
		if a.MediaType == "" || len(a.Data) == 0 {
			// Defence-in-depth: skip incomplete entries rather than
			// emit a malformed block. The upstream resolver should
			// surface the missing-bytes case as an error before
			// reaching here.
			continue
		}
		dataURL := fmt.Sprintf("data:%s;base64,%s",
			a.MediaType,
			base64.StdEncoding.EncodeToString(a.Data))
		parts = append(parts, openaiAPI.ImageContentPart(
			openaiAPI.ChatCompletionContentPartImageImageURLParam{
				URL: dataURL,
			},
		))
	}
	if len(parts) == 0 {
		return nil
	}
	// Append a text part for the message body — OpenAI's multimodal
	// payload accepts a mixed [image, ..., text] sequence per the
	// Chat Completions vision API. Always emit a text part (possibly
	// empty) so the wire payload is well-formed alongside the images.
	parts = append(parts, openaiAPI.TextContentPart(m.Content))
	return parts
}

// GateAttachmentRequestSize is the openaicompat-side pre-flight gate
// that mirrors the Anthropic provider's 25 MB ceiling check (plan §6
// task-04, lifted to the shared seam in PR3 task-10). Every provider
// that wraps openaicompat (openai, copilot, openzen, zai, ollamacloud)
// calls this before BuildParams so the size error surfaces on the
// existing Stream / Chat error return path rather than failing on the
// wire after a partial encode.
//
// The agnostic provider.ErrAttachmentRequestTooLarge sentinel is
// wrapped with %w so callers can errors.Is against it without coupling
// to this package.
//
// Expected:
//   - req carries zero or more messages each possibly with
//     Attachments populated. Empty / nil slices return nil.
//
// Returns:
//   - nil when the total attachment-byte sum across every message is
//     within the ceiling.
//   - A wrapped provider.ErrAttachmentRequestTooLarge when over.
//
// Side effects:
//   - None.
func GateAttachmentRequestSize(req provider.ChatRequest) error {
	var total int64
	for _, m := range req.Messages {
		total += provider.TotalAttachmentBytes(m.Attachments)
	}
	if total > provider.MaxAttachmentRequestBytes() {
		return fmt.Errorf("openaicompat: %w (got %d bytes, limit %d)",
			provider.ErrAttachmentRequestTooLarge,
			total, provider.MaxAttachmentRequestBytes())
	}
	return nil
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
	// Opt in to OpenAI's stream_options.include_usage so the upstream
	// emits a terminal chunk carrying request-level token totals (per
	// OpenAI streaming spec — the chunk has empty choices and a
	// populated `usage` block). Without this flag the accumulator's
	// Usage stays zero and the engine cannot track streaming spend for
	// any provider that wraps openaicompat (openai, openzen, zai,
	// ollamacloud, github-copilot). The Bool() helper produces a typed
	// param.Opt[bool] which marshals correctly under the SDK's
	// omitzero contract.
	params.StreamOptions.IncludeUsage = openaiAPI.Bool(true)
	ch := make(chan provider.StreamChunk, 16)
	go func() {
		defer close(ch)
		stream := client.Chat.Completions.NewStreaming(ctx, params)
		var acc openaiAPI.ChatCompletionAccumulator
		emitted := make(map[int]bool)
		// inlineExtractor scans reasoning_content for inline-XML
		// `<tool_call>...</tool_call>` blocks (glm-4.5/4.6 variant) and
		// holds any partial block across stream chunks. Its Thinking
		// output is what flows downstream; any closed tool calls are
		// buffered and emitted at end-of-stream (see recoveredCalls)
		// so they cannot double-execute alongside a structured call
		// the SDK already produced.
		var inlineExtractor inlineToolCallExtractor
		var recoveredCalls []provider.ToolCall
		// sawFinish tracks whether the upstream signalled an in-stream
		// finish_reason. When true the post-loop epilogue is responsible
		// for emitting usage + Done so the usage chunk arrives before
		// downstream consumers see Done and stop reading.
		var sawFinish bool
		for stream.Next() {
			chunk := stream.Current()
			acc.AddChunk(chunk)
			if len(chunk.Choices) > 0 {
				delta := chunk.Choices[0].Delta
				if delta.Content != "" {
					shared.SendChunk(ctx, ch, provider.StreamChunk{Content: delta.Content})
				}
				// Drop #1 — extract reasoning_content for OpenAI-compat
				// providers that emit a separate reasoning channel
				// (zai/glm-4.6, DeepSeek-R1). The Go SDK's typed delta
				// has no Reasoning field; the data lives in the JSON
				// extra-fields map (see openai-go ChoiceDelta.JSON.ExtraFields).
				// extractReasoningContent returns the empty string when the
				// field is absent, so plain OpenAI providers are unaffected.
				//
				// May 2026 inline-XML recovery: glm-4.5/4.6 sometimes
				// emit tool calls as `<tool_call>...</tool_call>` markup
				// inside this reasoning channel. Funnel reasoning text
				// through inlineExtractor so the markup is stripped
				// from downstream Thinking and the recovered calls are
				// queued for end-of-stream emission.
				if reasoning := extractReasoningContent(delta); reasoning != "" {
					result := inlineExtractor.Feed(reasoning)
					if result.Thinking != "" {
						shared.SendChunk(ctx, ch, provider.StreamChunk{Thinking: result.Thinking})
					}
					recoveredCalls = append(recoveredCalls, result.ToolCalls...)
				}
			}
			if tc, ok := acc.JustFinishedToolCall(); ok {
				emitted[tc.Index] = true
				shared.SendChunk(ctx, ch, provider.StreamChunk{
					EventType:  "tool_call",
					ToolCallID: tc.ID,
					ToolCall: &provider.ToolCall{
						ID:        tc.ID,
						Name:      tc.Name,
						Arguments: shared.ParseToolArguments(tc.Arguments),
					},
				})
			}
			if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != "" {
				flushAccumulatedToolCalls(ctx, ch, &acc, emitted)
				flushInlineExtractor(ctx, ch, &inlineExtractor, recoveredCalls, emitted)
				recoveredCalls = nil
				sawFinish = true
				// Deliberately DO NOT emit Done here. The terminal
				// `stream_options.include_usage` chunk arrives AFTER
				// this finish-reason chunk and carries the request-level
				// token totals; we must keep the loop alive to consume
				// it and emit a usage chunk before Done.
			}
		}
		if err := stream.Err(); err != nil {
			shared.SendChunk(ctx, ch, provider.StreamChunk{Error: wrapStreamError(providerName, err), Done: true})
			return
		}
		flushAccumulatedToolCalls(ctx, ch, &acc, emitted)
		flushInlineExtractor(ctx, ch, &inlineExtractor, recoveredCalls, emitted)
		if sawFinish {
			emitStreamUsage(ctx, ch, &acc)
			shared.SendChunk(ctx, ch, provider.StreamChunk{Done: true})
		}
	}()
	return ch
}

// emitStreamUsage reads cumulative token totals from the accumulator and
// sends a `provider.StreamChunk{EventType:"usage"}` carrying a
// `provider.UsageDelta` so downstream consumers (session accumulator at
// internal/session/accumulator.go:333; engine spend tracking) can record
// streaming spend for openaicompat-backed providers. The chunk is
// suppressed when the accumulator captured no tokens — that happens
// when the upstream did not honour `stream_options.include_usage`
// (older mocks, partial SDK forks) and avoids synthesising zero-value
// usage events for unaffected callers.
//
// Expected:
//   - ctx is the caller context used to honour cancellation when sending.
//   - ch is the downstream StreamChunk channel.
//   - acc is the accumulator populated by stream.AddChunk calls; the SDK
//     sums chunk.Usage.* into acc.Usage on every chunk so a single
//     read after the loop captures the request-level total.
//
// Side effects:
//   - Sends at most one StreamChunk on ch.
func emitStreamUsage(
	ctx context.Context,
	ch chan<- provider.StreamChunk,
	acc *openaiAPI.ChatCompletionAccumulator,
) {
	if acc == nil {
		return
	}
	prompt := acc.Usage.PromptTokens
	completion := acc.Usage.CompletionTokens
	total := acc.Usage.TotalTokens
	if prompt == 0 && completion == 0 && total == 0 {
		return
	}
	shared.SendChunk(ctx, ch, provider.StreamChunk{
		EventType: "usage",
		Usage: &provider.UsageDelta{
			InputTokens:  prompt,
			OutputTokens: completion,
		},
	})
}

// flushInlineExtractor drains any remaining buffered reasoning text
// from the extractor and emits queued recovered tool calls.
//
// Recovered calls are SUPPRESSED when the SDK has already emitted any
// structured tool call on this stream — that case is the providers
// that emit BOTH a structured tool_calls array AND a reasoning
// look-alike string; honouring both would double-execute. The
// structured array wins; recovery is the fallback only.
//
// Expected:
//   - ctx is the caller context.
//   - ch is the downstream StreamChunk channel.
//   - extractor is non-nil; its remaining buffer is flushed as Thinking.
//   - recovered are the inline-XML tool calls assembled during the stream.
//   - emitted tracks structured tool-call indexes already forwarded; a
//     non-empty map suppresses recovery.
//
// Side effects:
//   - Sends one Thinking chunk if any unparsed reasoning remains, and
//     one tool_call chunk per recovered call when not suppressed.
func flushInlineExtractor(
	ctx context.Context,
	ch chan<- provider.StreamChunk,
	extractor *inlineToolCallExtractor,
	recovered []provider.ToolCall,
	emitted map[int]bool,
) {
	if extractor != nil {
		if remaining := extractor.Flush(); remaining != "" {
			shared.SendChunk(ctx, ch, provider.StreamChunk{Thinking: remaining})
		}
	}
	if len(recovered) == 0 {
		return
	}
	if len(emitted) > 0 {
		// Structured tool calls already won; recovery would
		// double-execute. Drop the recovered set silently — the
		// canonical call is already on the wire.
		return
	}
	for i := range recovered {
		tc := recovered[i]
		shared.SendChunk(ctx, ch, provider.StreamChunk{
			EventType:  "tool_call",
			ToolCallID: tc.ID,
			ToolCall:   &tc,
		})
	}
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

// extractReasoningContent pulls a `reasoning_content` text fragment out of an
// OpenAI-compat delta when the upstream provider emits its reasoning tokens
// on that non-standard channel (zai/glm-4.6, DeepSeek-R1). The openai-go SDK's
// typed `ChatCompletionChunkChoiceDelta` has no `Reasoning` field, so the data
// is only reachable via the SDK's `JSON.ExtraFields` map of preserved JSON
// values.
//
// Expected:
//   - delta is the parsed delta from a streaming chunk choice. May contain a
//     `reasoning_content` extra field (string-typed) when the provider speaks
//     the reasoning dialect; otherwise the field is absent.
//
// Returns:
//   - The decoded reasoning text when present.
//   - The empty string when the field is absent, null, or non-string. Plain
//     OpenAI providers fall through this empty path so the helper is a no-op
//     for them.
//
// Side effects:
//   - None.
func extractReasoningContent(delta openaiAPI.ChatCompletionChunkChoiceDelta) string {
	field, ok := delta.JSON.ExtraFields["reasoning_content"]
	if !ok {
		return ""
	}
	// Note: respjson.Field.Valid() returns false for ExtraFields entries
	// because the SDK stores them with internal status=invalid (the typed
	// `valid` status is reserved for fields with a typed Go counterpart).
	// We rely on the Raw() text directly — presence of the key + non-empty,
	// non-null raw JSON is enough.
	raw := field.Raw()
	if raw == "" || raw == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal([]byte(raw), &text); err != nil {
		// reasoning_content is documented as a string by the providers
		// that emit it. A non-string value is either a future schema
		// change or noise — ignore rather than mis-emit.
		return ""
	}
	return text
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
			EventType:  "tool_call",
			ToolCallID: tc.ID,
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
// When the underlying HTTP response carries OpenAI-style rate-limit
// metadata headers (`retry-after`, `x-ratelimit-*`, `x-request-id` /
// `request-id`), they are parsed into provider.RateLimit and attached
// so failover schedulers can honour the carrier-issued back-off
// instead of guessing per error type. Headers absent or unparseable
// leave the field nil — callers must treat that as "no metadata
// available", not "limits zeroed". Both OpenAI's duration-string
// reset format ("1s", "10ms") and Z.AI's seconds-int form are
// accepted because openaicompat is shared across both backends.
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
			RateLimit:   extractRateLimitHeaders(openaiErr.Response),
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

// extractRateLimitHeaders inspects the openai-go SDK error's underlying
// http.Response for the documented rate-limit headers and returns a
// populated RateLimit when at least one header is present.
//
// OpenAI exposes per-window budgets for requests and tokens. Each
// window has three headers — limit, remaining, reset. Reset is a
// duration string in the OpenAI dialect ("1s", "10ms") and a
// seconds-int in the Z.AI dialect; both are accepted and turned into a
// wall-clock time. The 429 path also carries `retry-after` (seconds
// or HTTP-date) and either `x-request-id` (OpenAI) or `request-id`
// (Z.AI) for support correlation.
//
// Expected:
//   - resp may be nil.
//
// Returns:
//   - A pointer to a RateLimit when at least one rate-limit header was
//     present.
//   - nil otherwise.
//
// Side effects:
//   - None.
func extractRateLimitHeaders(resp *http.Response) *provider.RateLimit {
	if resp == nil {
		return nil
	}
	headers := resp.Header
	if len(headers) == 0 {
		return nil
	}

	rl := newEmptyRateLimit()
	hasAny := readScalarHeaders(headers, &rl)
	hasAny = readWindowHeaders(headers, &rl) || hasAny

	if !hasAny {
		return nil
	}
	return &rl
}

// newEmptyRateLimit builds a RateLimit pre-populated with -1 sentinels
// so the caller can disambiguate "header not provided" from a real "0
// remaining". Reset times stay zero-valued.
//
// Returns:
//   - A RateLimit value ready to receive header-driven mutations.
//
// Side effects:
//   - None.
func newEmptyRateLimit() provider.RateLimit {
	return provider.RateLimit{
		InputTokensLimit:      -1,
		InputTokensRemaining:  -1,
		OutputTokensLimit:     -1,
		OutputTokensRemaining: -1,
		RequestsLimit:         -1,
		RequestsRemaining:     -1,
		TokensLimit:           -1,
		TokensRemaining:       -1,
	}
}

// readScalarHeaders captures the scalar (non-window) rate-limit
// signals: `retry-after` and the request-id header (which most OpenAI-
// compat backends emit as `x-request-id`, while Z.AI also emits a bare
// `request-id`).
//
// Expected:
//   - headers is the response header map (may be empty).
//   - rl is non-nil; mutated in place on hits.
//
// Returns:
//   - true when at least one scalar header was captured.
//
// Side effects:
//   - Mutates *rl on hits.
func readScalarHeaders(headers http.Header, rl *provider.RateLimit) bool {
	hit := false
	if v := headers.Get("retry-after"); v != "" {
		if d, ok := parseRetryAfter(v); ok {
			rl.RetryAfter = d
			hit = true
		}
	}
	// Try the canonical OpenAI form first; fall back to the bare form
	// some backends (Z.AI, fireworks) emit.
	if v := headers.Get("x-request-id"); v != "" {
		rl.RequestID = v
		hit = true
	} else if v := headers.Get("request-id"); v != "" {
		rl.RequestID = v
		hit = true
	}
	return hit
}

// readWindowHeaders captures the requests/tokens limit/remaining/reset
// triples. Decomposed out of extractRateLimitHeaders so the latter
// stays under the gocognit threshold.
//
// Expected:
//   - headers is the response header map (may be empty).
//   - rl is non-nil; mutated in place on hits.
//
// Returns:
//   - true when at least one window header was captured.
//
// Side effects:
//   - Mutates *rl on hits.
func readWindowHeaders(headers http.Header, rl *provider.RateLimit) bool {
	windows := []struct {
		limitHdr     string
		remainingHdr string
		resetHdr     string
		limitDst     *int
		remainingDst *int
		resetDst     *time.Time
	}{
		{
			"x-ratelimit-limit-requests",
			"x-ratelimit-remaining-requests",
			"x-ratelimit-reset-requests",
			&rl.RequestsLimit, &rl.RequestsRemaining,
			&rl.RequestsReset,
		},
		{
			"x-ratelimit-limit-tokens",
			"x-ratelimit-remaining-tokens",
			"x-ratelimit-reset-tokens",
			&rl.TokensLimit, &rl.TokensRemaining,
			&rl.TokensReset,
		},
	}
	hit := false
	for _, w := range windows {
		if readIntHeader(headers, w.limitHdr, w.limitDst) {
			hit = true
		}
		if readIntHeader(headers, w.remainingHdr, w.remainingDst) {
			hit = true
		}
		if readResetHeader(headers, w.resetHdr, w.resetDst) {
			hit = true
		}
	}
	return hit
}

// parseRetryAfter parses the `retry-after` HTTP header value. The spec
// permits either a non-negative integer (seconds) or an HTTP-date;
// most providers emit the seconds form on 429 but we accept both.
//
// Expected:
//   - value is the raw header text.
//
// Returns:
//   - The duration to wait and true on success.
//   - 0 and false when the header is absent, blank, or unparseable.
//
// Side effects:
//   - None.
func parseRetryAfter(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(value); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(value); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

// readIntHeader parses a non-negative integer header into dst and
// returns true when the header was present and parseable.
//
// Expected:
//   - headers is the response header map (may be empty).
//   - name is the header to look up.
//   - dst is non-nil; left untouched on parse failure.
//
// Returns:
//   - true when the header was present and successfully parsed.
//
// Side effects:
//   - Mutates *dst on success.
func readIntHeader(headers http.Header, name string, dst *int) bool {
	v := headers.Get(name)
	if v == "" {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return false
	}
	*dst = n
	return true
}

// readResetHeader parses a window-reset header into dst as a wall-clock
// time. Two formats are accepted because openaicompat is shared across
// backends:
//
//   - OpenAI emits a Go-compatible duration string like "1s" or "10ms".
//   - Z.AI emits a bare seconds-int.
//
// Both are interpreted as "time-until-reset" relative to now. Reset is
// stored as a future wall-clock time so the failover hook can compare
// against time.Now() directly.
//
// Expected:
//   - headers is the response header map (may be empty).
//   - name is the header to look up.
//   - dst is non-nil; left untouched on parse failure.
//
// Returns:
//   - true when the header was present and successfully parsed.
//
// Side effects:
//   - Mutates *dst on success.
func readResetHeader(headers http.Header, name string, dst *time.Time) bool {
	v := strings.TrimSpace(headers.Get(name))
	if v == "" {
		return false
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		*dst = time.Now().Add(time.Duration(secs) * time.Second)
		return true
	}
	if d, err := time.ParseDuration(v); err == nil && d >= 0 {
		*dst = time.Now().Add(d)
		return true
	}
	return false
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
