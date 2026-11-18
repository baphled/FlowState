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
}

// ExecLoopResult reports one completed bounded run.
type ExecLoopResult struct {
	Turns           int64
	FinalContent    string
	ToolCalls       int64
	BudgetExhausted bool
}

// SessionExecLoopRequest carries the CLI-parsed arguments for one
// provider-driven loop run.
type SessionExecLoopRequest struct {
	// Allow lists inline allowlisted tool names.
	Allow []string
	// AllowFile is the --allow-file manifest path.
	AllowFile string
	// Workdir is the --workdir override; empty defaults beside the db.
	Workdir string
	// Budget is the total loop budget pool; <= 0 means unlimited turns.
	Budget int64
	// MaxTokens caps each provider response; <= 0 means provider default.
	MaxTokens int64
	// MaxTurns bounds the loop; <= 0 uses execDefaultMaxTurns.
	MaxTurns int64
}
