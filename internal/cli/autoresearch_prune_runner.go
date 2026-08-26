// Package cli's autoresearch_prune_runner.go provides
// autoresearchPruneAppRunner — the runner.AutoresearchPruner implementation
// that bridges the engine tool layer to the prune logic in autoresearch_prune.go.
//
// The runner is injected into internal/app at CLI startup via
// app.SetAutoresearchPruner to avoid an app→cli import cycle.

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/runner"
)

// autoresearchPruneAppRunner implements runner.AutoresearchPruner by
// delegating to the prune logic in autoresearch_prune.go.
type autoresearchPruneAppRunner struct {
	application *app.App
}

// PruneAutoresearch converts runner.AutoresearchPruneOpts into the internal
// prune shape, executes the prune, and returns structured counts.
//
// Expected:
//   - ctx is a valid context (the underlying prune path is synchronous but
//     the interface contract requires it).
//   - opts carries at least one of All or OlderThan set.
//
// Returns:
//   - A populated runner.AutoresearchPruneResult on success.
//   - An error if the prune operation fails.
//
// Side effects:
//   - Same as runAutoresearchPrune: reads and optionally deletes
//     coord-store keys depending on opts.DryRun.
func (r *autoresearchPruneAppRunner) PruneAutoresearch(
	_ context.Context,
	opts runner.AutoresearchPruneOpts,
) (runner.AutoresearchPruneResult, error) {
	if r.application.Config == nil || r.application.Config.DataDir == "" {
		return runner.AutoresearchPruneResult{}, errors.New("autoresearch prune requires a DataDir-configured app")
	}

	store, err := coordination.NewFileStore(filepath.Join(r.application.Config.DataDir, "coordination.json"))
	if err != nil {
		return runner.AutoresearchPruneResult{}, fmt.Errorf("opening coord-store: %w", err)
	}

	keys, err := store.List("autoresearch/")
	if err != nil {
		return runner.AutoresearchPruneResult{}, fmt.Errorf("listing coord-store: %w", err)
	}

	runIDs := uniqueAutoresearchRunIDs(keys)
	if len(runIDs) == 0 {
		return runner.AutoresearchPruneResult{DryRun: opts.DryRun}, nil
	}

	olderThan := opts.OlderThan
	if olderThan == 0 && !opts.All {
		olderThan = 168 * time.Hour
	}
	cutoff := time.Now().UTC().Add(-olderThan)

	eligible := pruneEligibleRuns(store, runIDs, opts.All, cutoff)
	if len(eligible) == 0 {
		return runner.AutoresearchPruneResult{DryRun: opts.DryRun}, nil
	}

	// Collect the deletion plan regardless of dry-run so we can return
	// accurate counts and the run IDs to the caller.
	var plan []runKeys
	for _, runID := range eligible {
		rk := runKeys{id: runID}
		rk.keys = keysForRun(store, runID)
		if len(rk.keys) > 0 {
			plan = append(plan, rk)
		}
	}

	totalKeys := 0
	runIDs2 := make([]string, 0, len(plan))
	for _, rk := range plan {
		totalKeys += len(rk.keys)
		runIDs2 = append(runIDs2, rk.id)
	}

	res := runner.AutoresearchPruneResult{
		RunsPruned:  len(plan),
		KeysDeleted: totalKeys,
		DryRun:      opts.DryRun,
		Runs:        runIDs2,
	}

	if opts.DryRun {
		return res, nil
	}

	// Execute the actual deletion via the shared helper.
	var sink bytes.Buffer
	if delErr := executePrune(&sink, store, plan); delErr != nil {
		return runner.AutoresearchPruneResult{}, delErr
	}

	return res, nil
}

// NewAutoresearchPruneAppRunner creates a runner.AutoresearchPruner backed
// by the given App. The CLI root command calls this and passes the result to
// app.SetAutoresearchPruner to wire the autoresearch_prune engine tool.
//
// Expected:
//   - application is non-nil.
//
// Returns:
//   - A runner.AutoresearchPruner that delegates to the cli package's prune logic.
//
// Side effects:
//   - None.
func NewAutoresearchPruneAppRunner(application *app.App) runner.AutoresearchPruner {
	return &autoresearchPruneAppRunner{application: application}
}
