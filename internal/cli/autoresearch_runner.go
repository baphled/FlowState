// Package cli's autoresearch_runner.go provides autoresearchAppRunner —
// the runner.AutoresearchRunner implementation that bridges the engine
// tool layer to RunAutoresearchWithResult in this package.
//
// The runner is injected into internal/app at CLI startup via
// app.SetAutoresearchRunner to avoid an app→cli import cycle.

package cli

import (
	"context"
	"io"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/runner"
)

// autoresearchAppRunner implements runner.AutoresearchRunner by delegating
// to RunAutoresearchWithResult in this package.
type autoresearchAppRunner struct {
	application *app.App
}

// RunAutoresearch converts runner.AutoresearchOpts to cli.AutoresearchOptions,
// calls RunAutoresearchWithResult, and converts the result back.
//
// Expected:
//   - ctx is a valid context.
//   - opts contains at least opts.Surface (non-empty).
//   - out receives human-readable progress lines.
//
// Returns:
//   - A populated runner.AutoresearchResult on success.
//   - An error if the run fails.
//
// Side effects:
//   - Same as RunAutoresearchWithResult.
func (r *autoresearchAppRunner) RunAutoresearch(
	ctx context.Context,
	opts runner.AutoresearchOpts,
	out io.Writer,
) (runner.AutoresearchResult, error) {
	cliOpts := AutoresearchOptions{
		Surface:         opts.Surface,
		DriverScript:    opts.DriverScript,
		EvaluatorScript: opts.EvaluatorScript,
		RunID:           opts.RunID,
		MaxTrials:       opts.MaxTrials,
		TimeBudget:      opts.TimeBudget,
		MetricDirection: opts.MetricDirection,
		DriverAgent:     opts.DriverAgent,
	}
	res, err := RunAutoresearchWithResult(ctx, cliOpts, r.application, out)
	if err != nil {
		return runner.AutoresearchResult{}, err
	}
	return runner.AutoresearchResult{
		RunID:             res.RunID,
		TerminationReason: res.TerminationReason,
		TotalTrials:       res.TotalTrials,
		Converged:         res.Converged,
		BestScore:         res.BestScore,
		BestCandidateSHA:  res.BestCandidateSHA,
		BestCommitSHA:     res.BestCommitSHA,
	}, nil
}

// NewAutoresearchAppRunner creates a runner.AutoresearchRunner backed by
// the given App. The CLI root command calls this and passes the result to
// app.SetAutoresearchRunner to wire the autoresearch_run engine tool.
func NewAutoresearchAppRunner(application *app.App) runner.AutoresearchRunner {
	return &autoresearchAppRunner{application: application}
}
