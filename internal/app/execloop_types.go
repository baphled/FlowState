package app

import (
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// ExecLoopConfig carries the collaborators and limits for one provider-driven run.
type ExecLoopConfig struct {
	Store                 *store.Store
	Session               *store.Session
	Streamer              provider.Streamer
	Registry              *tool.Registry
	Allowlist             []string
	Pool                  *BudgetPool
	LeaseItem, LeaseAgent string
	MaxTurns, TurnBudget  int64
}

// ExecLoopResult reports one completed bounded run.
type ExecLoopResult struct {
	Turns           int64
	FinalContent    string
	ToolCalls       int64
	BudgetExhausted bool
}
