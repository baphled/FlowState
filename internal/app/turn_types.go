package app

import (
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
)

// TypeUserMessage is the user-turn event type (DATA-MODEL §3).
const TypeUserMessage = "user.message"

// TypeAssistantDelta is one streamed assistant chunk event type.
const TypeAssistantDelta = "assistant.delta"

// TypeAssistantMessage is the final assistant event type.
const TypeAssistantMessage = "assistant.message"

// TypeTurnEnd is the turn-completion event type.
const TypeTurnEnd = "turn.end"

// TypeProviderError is the terminal provider-failure event type.
const TypeProviderError = "provider.error"

// CancelCause is a context cancellation cause that names the reason
// recorded in `session.cancelled`. A command-line signal watcher
// cancels the root context with one; internal/server cannot import
// this package, so it defines its own cause carrying the same
// CancelReason method, which CancelReason reads through an interface.
type CancelCause struct {
	// Reason is the recorded reason: sigint, sigterm, sighup,
	// client_disconnected, shutdown or cancelled.
	Reason string
}

// reasonedCause is what CancelReason looks for in a cancellation cause.
type reasonedCause interface {
	// CancelReason returns the reason to record for the cancellation.
	CancelReason() string
}

// turnState accumulates one turn stream's content, consumption and
// stop reason while events are consumed.
type turnState struct {
	content        string
	consumed       int64
	streamErr      bool
	budgetExceeded bool
	// err is the persistence failure that ended the stream, if any.
	err error
	// cause is the provider's own error when the stream failed.
	cause error
	// finishReason is the provider-neutral reason of the finish event.
	finishReason string
	// usage is the usage of the finish event; nil when not reported.
	usage *provider.Usage
}

// TurnOptions configures one engine turn; the zero value preserves
// the existing unlimited behaviour.
type TurnOptions struct {
	// Budget caps the turn; values <= 0 mean unlimited.
	Budget int64
	// MaxTokens caps the provider response; <= 0 means provider default.
	MaxTokens int64
	// ModelID overrides the session's stored model for this turn
	// when non-empty; empty keeps the config-driven session default.
	ModelID string
}

// TurnResult reports one completed engine turn.
type TurnResult struct {
	// SessionID is the session the turn ran in.
	SessionID string
	// FinalContent is the concatenated assistant content.
	FinalContent string
	// BudgetExceeded reports that the turn stopped early on its budget.
	BudgetExceeded bool
	// ConsumedBudget is the budget consumed this turn. In M1 tokens are
	// approximated 1:1 by the total length of streamed provider delta
	// content (sum of len(ev.Content) over the streamed chunks).
	ConsumedBudget int64
	// FinishReason is the provider-neutral finish reason reported by
	// the stream (for example "stop" or "length"); empty when the
	// provider sent none. A "length" turn is complete but truncated.
	FinishReason string
}

// Store aliases the session store so cmd depends only on app.
type Store = store.Store

// Streamer aliases the streaming-provider port for cmd use.
type Streamer = provider.Streamer

// ProviderResolver resolves a provider by name (registry-backed).
type ProviderResolver = func(name string) (provider.Provider, error)
