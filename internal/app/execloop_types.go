package app

import (
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// ExecLoopConfig carries the collaborators and limits for one provider-driven run.
type ExecLoopConfig struct {
	Store      *store.Store
	Session    *store.Session
	Streamer   provider.Streamer
	Registry   *tool.Registry
	Allowlist  []string
	Pool       *BudgetPool
	LeaseItem  string
	LeaseAgent string
	MaxTurns   int64
	TurnBudget int64
	// MaxTokens caps each provider turn's response tokens; <= 0 uses
	// the provider default.
	MaxTokens int64
	// SystemPrompt, when non-empty, is sent as a message of role system first
	// in every provider request of the loop. It is not persisted as an event
	// and is not part of replayed history.
	SystemPrompt string
}

// ExecLoopResult reports one completed bounded run.
type ExecLoopResult struct {
	Turns           int64
	FinalContent    string
	ToolCalls       int64
	BudgetExhausted bool
	// FinishReason is the finish reason of the final provider turn;
	// empty when the provider reported none.
	FinishReason string
	// Usage sums the provider usage of the turns that reported it; a turn
	// without usage adds nothing (Usage.UsageTurns tells how many did).
	Usage UsageTotals
	// LastContextTokens is ContextTokens of the last turn that reported
	// usage; meaningful only when LastContextReported is true.
	LastContextTokens int64
	// LastContextReported is false when no turn reported usage.
	LastContextReported bool
}

// execTurn is one provider turn's streamed outcome inside the loop.
type execTurn struct {
	// Content is the concatenated assistant text.
	Content string
	// Calls are the complete tool calls the turn requested.
	Calls []provider.ToolCall
	// FinishReason is the neutral reason of the turn's finish event;
	// empty when the provider sent none.
	FinishReason string
	// Usage is the usage of the turn's finish event; nil when not reported.
	Usage *provider.Usage
}

// SessionExecLoopRequest carries the CLI-parsed arguments for one
// provider-driven loop run.
type SessionExecLoopRequest struct {
	// Allow lists inline allowlisted tool names.
	Allow []string
	// AllowFile is the --allow-file manifest path.
	AllowFile string
	// RequireAllowlist makes an empty loaded allowlist (inline plus
	// manifest) a usage error instead of a tool-less loop run.
	RequireAllowlist bool
	// Workdir is the --workdir override; empty defaults beside the db.
	Workdir string
	// Agents is the --agents scope (user, project or both); empty offers no
	// delegation. A scope requires `delegate` on the allowlist and the other
	// way round; both are usage errors.
	Agents string
	// ProjectRoot is where project profiles are found when Workdir is empty;
	// empty means the process current directory. The CLI leaves it empty; it is
	// a seam for in-process callers and specs, which must not change directory.
	ProjectRoot string
	// Budget is the total loop budget pool; <= 0 means unlimited turns.
	Budget int64
	// MaxTokens caps each provider response; <= 0 means provider default.
	MaxTokens int64
	// MaxTurns bounds the loop; <= 0 uses execDefaultMaxTurns (25).
	MaxTurns int64
}

// UsageTotals sums the provider usage of the turns that reported it. A turn
// without usage adds nothing and is not counted in UsageTurns, so a caller can
// tell a partial total from a complete one.
type UsageTotals struct {
	// InputTokens is the summed prompt tokens billed at the full rate.
	InputTokens int64
	// OutputTokens is the summed tokens the model generated.
	OutputTokens int64
	// CacheReadTokens is the summed prompt tokens served from the cache.
	CacheReadTokens int64
	// CacheWriteTokens is the summed prompt tokens written to the cache.
	CacheWriteTokens int64
	// UsageTurns is how many turns reported usage.
	UsageTurns int64
}

// SessionUsage is the usage a session's event log records, as `session doctor`
// reports it. It is computed from the assistant.message events only, so a log
// written before usage was recorded reads as UsageTurns 0: not reported.
type SessionUsage struct {
	// Totals sums the turns that reported usage; Totals.UsageTurns is
	// the turns_reported of the wire shape.
	Totals UsageTotals
	// TurnsTotal counts every assistant.message event, reported or not.
	TurnsTotal int64
	// LastContextTokens is ContextTokens of the last turn that reported
	// usage; it is meaningful only when Totals.UsageTurns > 0.
	LastContextTokens int64
}
