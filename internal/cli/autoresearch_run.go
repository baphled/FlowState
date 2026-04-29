// Package cli's autoresearch_run.go extracts RunAutoresearchWithResult —
// a library entry point for the autoresearch harness that is callable
// without a cobra.Command. This enables the engine tool layer
// (internal/engine/autoresearch_run_tool.go) to launch runs as
// background tasks and read structured results from the coord-store.
//
// The function preserves the identical semantics as the cobra-facing
// runAutoresearch, including the commitTrials substrate branch.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// AutoresearchOptions holds operator-settable inputs for a programmatic run.
type AutoresearchOptions struct {
	Surface         string
	DriverScript    string
	EvaluatorScript string
	RunID           string
	MaxTrials       int
	TimeBudget      time.Duration
	MetricDirection string
	CommitTrials    bool
	// DriverAgent is forwarded to the driver subprocess as
	// FLOWSTATE_AUTORESEARCH_DRIVER_AGENT. Empty = driver uses its own default.
	DriverAgent string
}

// AutoresearchResult is the structured summary of a completed run.
type AutoresearchResult struct {
	RunID             string
	TerminationReason string
	TotalTrials       int
	Converged         bool
	BestScore         float64
	BestCandidateSHA  string
	BestCommitSHA     string
}

// RunAutoresearchWithResult executes an autoresearch run and returns a
// structured result alongside the error. Out receives one progress line
// per trial.
//
// Expected:
//   - ctx is a valid context that may be cancelled to interrupt the run.
//   - opts contains the run configuration (surface path required).
//   - application is a non-nil App with Config.DataDir set.
//   - out receives human-readable progress lines; may be io.Discard.
//
// Returns:
//   - A populated AutoresearchResult on success (including partial results
//     when the run terminates early via context cancellation).
//   - An error if option resolution or any harness precondition fails.
//
// Side effects:
//   - Writes manifest, trial, best, and result records to the coord-store.
//   - Under CommitTrials=true: creates and removes a git worktree.
func RunAutoresearchWithResult(
	ctx context.Context,
	opts AutoresearchOptions,
	application *app.App,
	out io.Writer,
) (AutoresearchResult, error) {
	if opts.Surface == "" {
		return AutoresearchResult{}, fmt.Errorf("autoresearch: Surface is required")
	}

	privOpts := toPrivateOpts(opts)

	resolved, err := resolveAutoresearchOptions(application, privOpts)
	if err != nil {
		return AutoresearchResult{}, err
	}

	// Apply the N12 de-dup check (Slice 6); suppress log noise when
	// there is no output writer configured.
	resolved.programDeduplicated = applyCallingAgentDeDup(resolved, out)

	if !resolved.commitTrials {
		if err := runAutoresearchContentTo(ctx, out, application, resolved); err != nil {
			return AutoresearchResult{}, err
		}
	} else {
		if err := runAutoresearchCommitTrialsTo(ctx, out, application, resolved); err != nil {
			return AutoresearchResult{}, err
		}
	}

	return readAutoresearchResult(application, resolved.runID)
}

// syntheticCmd returns a minimal *cobra.Command whose only purpose is
// to carry a custom output writer. The cobra SetOut/SetErr surface is
// the only interface runAutoresearchContent and runAutoresearchCommitTrials
// use from the command (via cmd.OutOrStdout()). Flags() is not consulted
// by the content/commitTrials paths once rejectGitModeFlagsWithoutCommitTrials
// has already run (or is skipped here).
func syntheticCmd(out io.Writer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd
}

// runAutoresearchContentTo is the io.Writer-accepting variant of
// runAutoresearchContent; it avoids the cobra.Command dependency so
// RunAutoresearchWithResult can call it directly.
func runAutoresearchContentTo(
	ctx context.Context,
	out io.Writer,
	application *app.App,
	resolved autoresearchRunOptions,
) error {
	cmd := syntheticCmd(out)
	return runAutoresearchContent(ctx, cmd, application, resolved)
}

// runAutoresearchCommitTrialsTo is the io.Writer-accepting variant of
// runAutoresearchCommitTrials.
func runAutoresearchCommitTrialsTo(
	ctx context.Context,
	out io.Writer,
	application *app.App,
	resolved autoresearchRunOptions,
) error {
	cmd := syntheticCmd(out)
	return runAutoresearchCommitTrials(ctx, cmd, application, resolved)
}

// readAutoresearchResult reads the result and best records from the
// coord-store and populates an AutoresearchResult. Missing records
// (e.g. max-trials=0 runs that write no result record) produce a
// zero-value struct without an error.
func readAutoresearchResult(application *app.App, runID string) (AutoresearchResult, error) {
	store, err := openCoordStore(application)
	if err != nil {
		return AutoresearchResult{}, fmt.Errorf("reading result: %w", err)
	}

	res := AutoresearchResult{RunID: runID}

	// Read the result record (termination reason, total trials, converged).
	resultKey := fmt.Sprintf("autoresearch/%s/result", runID)
	resultRaw, resultErr := store.Get(resultKey)
	if resultErr == nil && len(resultRaw) > 0 {
		var rec resultRecord
		if jsonErr := json.Unmarshal(resultRaw, &rec); jsonErr == nil {
			res.TerminationReason = rec.TerminationReason
			res.TotalTrials = rec.TotalTrials
			res.Converged = rec.Converged
			res.BestScore = rec.FinalScore
			res.BestCommitSHA = rec.FinalCommit
		}
	}

	// Read the best record (content SHA / commit SHA for best candidate).
	bestKey := fmt.Sprintf("autoresearch/%s/best", runID)
	bestRaw, bestErr := store.Get(bestKey)
	if bestErr == nil && len(bestRaw) > 0 {
		var best bestRecord
		if jsonErr := json.Unmarshal(bestRaw, &best); jsonErr == nil {
			res.BestCandidateSHA = best.CandidateContentSHA
			if best.CommitSHA != "" {
				res.BestCommitSHA = best.CommitSHA
			}
			if best.Score != 0 {
				res.BestScore = best.Score
			}
		}
	}

	return res, nil
}

// toPrivateOpts converts a public AutoresearchOptions into the internal
// autoresearchRunOptions shape understood by the existing implementation.
// Fields not exposed in AutoresearchOptions are filled with their defaults
// so resolveAutoresearchOptions can proceed normally.
func toPrivateOpts(pub AutoresearchOptions) autoresearchRunOptions {
	metricDir := pub.MetricDirection
	if metricDir == "" {
		metricDir = metricDirectionMin
	}
	return autoresearchRunOptions{
		surface:         pub.Surface,
		driverScript:    pub.DriverScript,
		evaluatorScript: pub.EvaluatorScript,
		runID:           pub.RunID,
		maxTrials:       pub.MaxTrials,
		timeBudget:      pub.TimeBudget,
		metricDirection: metricDir,
		commitTrials:    pub.CommitTrials,
		driverAgent:     pub.DriverAgent,
		// program defaults to the canonical skill name so
		// resolveAutoresearchOptions can resolve it against the repo root.
		// Callers of RunAutoresearchWithResult that do not need to
		// override the program leave Program empty; this default mirrors
		// the cobra flag default set in newAutoresearchRunCmd.
		program: "autoresearch",
	}
}

// toPublicOpts converts an internal autoresearchRunOptions into the
// public AutoresearchOptions shape.
func toPublicOpts(priv autoresearchRunOptions) AutoresearchOptions {
	return AutoresearchOptions{
		Surface:         priv.surface,
		DriverScript:    priv.driverScript,
		EvaluatorScript: priv.evaluatorScript,
		RunID:           priv.runID,
		MaxTrials:       priv.maxTrials,
		TimeBudget:      priv.timeBudget,
		MetricDirection: priv.metricDirection,
		CommitTrials:    priv.commitTrials,
		DriverAgent:     priv.driverAgent,
	}
}
