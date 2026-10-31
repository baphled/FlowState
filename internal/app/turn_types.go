package app

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

// turnState accumulates one turn stream's content, consumption and
// stop reason while events are consumed.
type turnState struct {
	content        string
	consumed       int64
	streamErr      bool
	budgetExceeded bool
	err            error
}

// TurnOptions configures one engine turn; the zero value preserves
// the existing unlimited behaviour.
type TurnOptions struct {
	// Budget caps the turn; values <= 0 mean unlimited.
	Budget int64
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
}
