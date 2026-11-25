package zai

import (
	"net/http"

	"github.com/baphled/flowstate/internal/provider"
)

// APIError is a typed provider failure with HTTP status and the
// provider's own code/message; the adapter never retries.
type APIError struct {
	Status  int
	Code    string
	Message string
}

// Provider is the Z.ai adapter (Provider + Stream).
type Provider struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
	// coding selects the coding-plan delivery route (coding.go).
	coding bool
	// codingRoute is the coding-plan wire format: "openai" (default)
	// or "anthropic"; empty means the openai default.
	codingRoute string
	// baseURLSet marks an explicit base-URL override (config host /
	// env); the coding routes then use it as the host.
	baseURLSet bool
	// systemTopLevel hoists system-role messages into the Messages API
	// "system" field on the anthropic route (WithAnthropicSystem).
	systemTopLevel bool
	// anthropicFinish, historically anthropic-route only, now ends a completed
	// stream on every route with a finish event (WithFinishEvents); off by default.
	anthropicFinish bool
	// anthropicTools enables structured tools on the anthropic route (WithAnthropicTools).
	anthropicTools bool
}

// Option customises the adapter.
type Option func(*Provider)

type request struct {
	Model     string             `json:"model"`
	Messages  []provider.Message `json:"messages"`
	MaxTokens int64              `json:"max_tokens,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type response struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage"`
}

// sseState is what the plain route remembers across the lines of one stream:
// the wire finish_reason and the usage of the last chunk that carried one.
type sseState struct {
	finish string
	usage  *provider.Usage
}

// openAIUsage is the OpenAI-style usage object: prompt_tokens counts the cached
// tokens too, which neutral() separates out. Pointers tell an absent count from
// a reported zero; an object with no known count is not a report.
type openAIUsage struct {
	PromptTokens        *int64 `json:"prompt_tokens"`
	CompletionTokens    *int64 `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens *int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// openAIUsageWire has the fields of openAIUsage without its UnmarshalJSON, so
// decoding it does not recurse.
type openAIUsageWire openAIUsage

// anthropicUsage is the Messages API usage object. Pointers tell an absent
// count (keep the earlier one) from a reported zero.
type anthropicUsage struct {
	InputTokens     *int64 `json:"input_tokens"`
	OutputTokens    *int64 `json:"output_tokens"`
	CacheCreation   *int64 `json:"cache_creation_input_tokens"`
	CacheReadTokens *int64 `json:"cache_read_input_tokens"`
}

// usageAccumulator folds the usage objects of one Messages stream (message_start,
// then the cumulative message_delta) into one neutral usage.
type usageAccumulator struct {
	seen  bool
	usage provider.Usage
}
