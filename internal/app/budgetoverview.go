package app

import (
	"context"

	"github.com/baphled/flowstate/internal/store"
)

// BudgetOverview reports one session's durable budget snapshot for
// the CLI surface through the owning layer (ADR-001). It is a pure
// pass-through over store.BudgetSnapshot: errors surface unchanged
// so ErrorCode keeps mapping store.ErrUnknownSession to
// session_not_found (exit 1). Unlike ReserveChildBudget it must NOT
// wrap into ErrUsage — the usage sentinel is matched first by
// ErrorCode, which would reclassify unknown sessions as exit 2.
func BudgetOverview(ctx context.Context, st *Store, sessionID string) (store.BudgetSnapshot, error) {
	return st.BudgetSnapshot(ctx, sessionID)
}
