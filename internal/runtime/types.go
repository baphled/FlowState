// Package runtime implements the engine turn loop (M1-SESSION): one
// provider turn streamed through the provider port and persisted as
// ordered attributable events via the session store's single writer.
// It owns session lifecycle and event ordering only; provider
// transport details stay in the adapters.
package runtime

import (
	"github.com/baphled/flowstate/internal/session/internalapp"
)

// DomainEventLog is the full persistence port used by the engine
// turn loop: replay, writer factory and status projection. The
// concrete adapter (*store.Store) satisfies it structurally, keeping
// the dependency direction store → domain correct.
type DomainEventLog = internalapp.TurnEventLog

// DomainSession is the session view consumed by the engine turn
// loop. *store.Session satisfies it structurally.
type DomainSession = internalapp.TurnSession

// PausePayload carries the pause metadata the engine hands to the
// writer when a budget cap stops a turn.
type PausePayload = internalapp.PausePayload

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
