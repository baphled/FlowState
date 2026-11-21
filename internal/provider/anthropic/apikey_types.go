package anthropic

import "github.com/baphled/flowstate/internal/provider/zai"

// DefaultAPIBaseURL is the Anthropic API host used when no override is configured.
const DefaultAPIBaseURL = "https://api.anthropic.com"

// APIKeyModel is the model id used when a request names none.
const APIKeyModel = "claude-sonnet-5-5"

// APIKey is the API-key route of the anthropic provider: ordinary Anthropic API
// billing with a key the user created, sent as x-api-key to the Messages API.
// It composes the existing Messages wire client and is independent of the
// subscription route; it never reads the Claude Code sign-in or runs `claude`.
type APIKey struct {
	key    string
	client *zai.Provider
}

// APIError is an error response from the Anthropic API. It carries the HTTP
// status, Anthropic's error type and message, and never the API key.
type APIError struct {
	// Status is the HTTP status code (401, 429, 529, ...).
	Status int
	// Type is Anthropic's error type, for example authentication_error.
	Type string
	// Message is Anthropic's own message with the API key redacted.
	Message string
	// cause is the underlying error that errors.Is can match; it is never printed.
	cause error
}

// routeError is a non-API failure of the API-key route (transport, stream,
// cancellation) with the key redacted from its message.
type routeError struct {
	msg   string
	cause error
}
