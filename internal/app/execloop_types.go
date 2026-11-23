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
	// Budget is the total loop budget pool; <= 0 means unlimited turns.
	Budget int64
	// MaxTokens caps each provider response; <= 0 means provider default.
	MaxTokens int64
	// MaxTurns bounds the loop; <= 0 uses execDefaultMaxTurns.
	MaxTurns int64
}
