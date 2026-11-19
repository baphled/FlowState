package main

import (
	"context"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// cmdBudget reports the budget overview for one session: the declared
// pool anchor, the outstanding reserved total and the available
// remainder. Sessions without a pool report null pool/available.
func cmdBudget(ctx context.Context, st *app.Store, id string, stdout *os.File) int {
	snap, err := st.BudgetSnapshot(ctx, id)
	if err != nil {
		writeError(os.Stderr, err)
		return exitCodeFor(err)
	}
	out := map[string]any{
		"session_id": id,
		"reserved":   snap.Reserved,
	}
	if snap.Pool != nil {
		out["budget_pool"] = *snap.Pool
		out["available"] = *snap.Available
	} else {
		out["budget_pool"] = nil
		out["available"] = nil
	}
	if err := writeJSON(stdout, out); err != nil {
		writeError(os.Stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}
