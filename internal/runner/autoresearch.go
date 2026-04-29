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
