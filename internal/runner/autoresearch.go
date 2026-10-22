// Package runner defines shared interface types used across internal packages
// to avoid import cycles. In particular it houses the AutoresearchRunner seam
// so that internal/engine does not need to import internal/cli or internal/app.
package runner

import (
	"context"
	"io"
	"time"
)

// AutoresearchRunner is the interface satisfied by the cli-layer adapter
// (autoresearchAppRunner). Defined here to keep internal/engine free of
// internal/cli and internal/app imports.
type AutoresearchRunner interface {
	RunAutoresearch(ctx context.Context, opts AutoresearchOpts, out io.Writer) (AutoresearchResult, error)
}

// AutoresearchOpts carries the operator-settable inputs for a programmatic
// autoresearch run. Fields map 1:1 to the public AutoresearchOptions in
// internal/cli so the bridge in internal/app is a straight field copy.
type AutoresearchOpts struct {
	Surface         string
	DriverScript    string
	EvaluatorScript string
	RunID           string
	MaxTrials       int
	TimeBudget      time.Duration
	MetricDirection string
	DriverAgent     string // agent ID to pass as FLOWSTATE_AUTORESEARCH_DRIVER_AGENT
	NoImproveWindow int    // 0 means use the CLI default (5)
	Program         string // skill name or path for the driver PROGRAM section; empty = "autoresearch"
}

// AutoresearchResult is the structured summary of a completed run as
// returned from AutoresearchRunner.RunAutoresearch. Fields are a strict
// subset of the cli.AutoresearchResult struct so the bridge in
// internal/app can copy them without import cycles.
type AutoresearchResult struct {
	RunID             string
	TerminationReason string
	TotalTrials       int
	Converged         bool
	BestScore         float64
	BestCandidateSHA  string
	BestCommitSHA     string
}

// AutoresearchPruner is the interface satisfied by the cli-layer adapter
// (autoresearchPruneAppRunner). Defined here to keep internal/engine free
// of internal/cli and internal/app imports.
type AutoresearchPruner interface {
	PruneAutoresearch(ctx context.Context, opts AutoresearchPruneOpts) (AutoresearchPruneResult, error)
}

// AutoresearchPruneOpts carries the operator-settable inputs for a
// programmatic autoresearch prune operation.
type AutoresearchPruneOpts struct {
	// OlderThan prunes runs whose started_at is before now-OlderThan.
	// A zero value is treated as "prune all" when All is false; callers
	// should set All=true explicitly to prune all runs.
	OlderThan time.Duration
	// All ignores OlderThan and prunes every run in the store.
	All bool
	// DryRun reports what would be deleted without making changes.
	DryRun bool
}

// AutoresearchPruneResult is the structured summary of a completed prune
// as returned from AutoresearchPruner.PruneAutoresearch.
type AutoresearchPruneResult struct {
	// RunsPruned is the number of runs deleted (or that would be deleted
	// on a dry run).
	RunsPruned int
	// KeysDeleted is the total number of coord-store keys deleted (or
	// that would be deleted on a dry run).
	KeysDeleted int
	// DryRun mirrors opts.DryRun so callers can distinguish a preview
	// from a real deletion.
	DryRun bool
	// Runs is the list of run IDs that were (or would be) pruned.
	Runs []string
}
