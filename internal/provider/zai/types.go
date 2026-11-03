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
	// coding selects the Anthropic-compatible coding-plan route.
	coding bool
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
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}
