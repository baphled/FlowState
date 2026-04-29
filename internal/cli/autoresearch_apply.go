// Package cli's autoresearch_apply.go implements the `flowstate
// autoresearch apply <run-id>` subcommand — Slice 2 of the April 2026
// In-Memory Default plan.
//
// apply materialises the best content candidate from a finished run.
// Default behaviour: print the candidate content to stdout. With
// --write <path>: write the candidate to an operator-chosen
// destination, refusing inside-repo paths unless --force-inside-repo
// overrides. The architectural intent of the content substrate is
// to leave the project tree untouched; the inside-repo guard makes
// that intent operator-visible rather than implicit.
//
// Runs that used --commit-trials carry no content candidate body —
// the candidate lives as a git commit on the run's branch. apply
// refuses such runs and redirects to `flowstate autoresearch promote`,
// which is the right tool for the cherry-pick workflow those runs
// produce.

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/spf13/cobra"
)

// autoresearchApplyOptions holds the parsed flag values for one apply
// invocation.
type autoresearchApplyOptions struct {
	writePath       string
	forceInsideRepo bool
}

// newAutoresearchApplyCmd creates the `autoresearch apply` subcommand.
//
// Expected:
//   - getApp is a non-nil function returning the application instance.
//
// Returns:
//   - A configured cobra.Command for the apply subcommand.
//
// Side effects:
//   - Reads the coord-store at <DataDir>/coordination.json.
//   - On --write: writes a single file to the operator-supplied path.
func newAutoresearchApplyCmd(getApp func() *app.App) *cobra.Command {
	var opts autoresearchApplyOptions

	cmd := &cobra.Command{
		Use:   "apply <run-id>",
		Short: "Materialise the best content candidate from a finished run",
		Long: "Read the run's `best` pointer from the coord-store and " +
			"emit the content candidate. With no flags, " +
			"the candidate is written to stdout — pipe / redirect / " +
			"inspect as you would any text. With --write <path>, the " +
			"candidate is written to a file at <path>.\n\n" +
			"--write paths inside the surface repo are refused unless " +
			"--force-inside-repo is set; the content substrate is " +
			"intentionally non-mutating, and a forced inside-repo write " +
			"is the explicit override for the rare case where you want " +
			"the candidate landed alongside the surface.\n\n" +
			"Runs created with --commit-trials carry the kept commit on " +
			"their branch, not as content bytes; apply refuses such " +
			"runs and redirects to `flowstate autoresearch promote " +
			"<run-id> --apply`.\n\n" +
			"April 2026 In-Memory Default plan, Slice 2.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAutoresearchApply(cmd, getApp(), args[0], opts)
		},
	}

	cmd.Flags().StringVar(&opts.writePath, "write", "",
		"Destination path for the candidate (default: print to stdout)")
	cmd.Flags().BoolVar(&opts.forceInsideRepo, "force-inside-repo", false,
		"Override the inside-repo refusal for --write; required when the destination sits under the surface repo's working tree")

	return cmd
}

// runAutoresearchApply drives one apply invocation end-to-end.
//
// Expected:
//   - cmd is a non-nil cobra.Command with an initialised output writer.
//   - application is a non-nil App with Config.DataDir resolved.
//   - runID is the operator-supplied run identifier.
//
// Returns:
//   - nil on a successful materialisation (stdout or file).
//   - non-nil error if the coord-store cannot be opened, the best
//     pointer is missing, the run was --commit-trials (no content
//     candidate), or the inside-repo guard refused the destination.
//
// Side effects:
//   - Reads the coord-store.
//   - On --write: writes the candidate file.
//   - Writes a one-line confirmation to cmd.OutOrStdout() on --write.
func runAutoresearchApply(cmd *cobra.Command, application *app.App, runID string, opts autoresearchApplyOptions) error {
	w := cmd.OutOrStdout()
	if application.Config == nil || application.Config.DataDir == "" {
		return errors.New("autoresearch apply requires a DataDir-configured app")
	}
	coordPath := filepath.Join(application.Config.DataDir, "coordination.json")

	store, err := coordination.NewFileStore(coordPath)
	if err != nil {
		return fmt.Errorf("opening coord-store: %w", err)
	}

	manifest, err := readApplyManifestRecord(store, runID)
	if err != nil {
		return err
	}

	if manifest.CommitTrials {
		return fmt.Errorf(
			"run %s was created with --commit-trials; the kept candidate is a git commit, not a content body — use `flowstate autoresearch promote %s --apply` instead",
			runID, runID)
	}

	best, err := readApplyBestPointer(store, runID)
	if err != nil {
		return err
	}
	if best.CandidateContentSHA == "" {
		return fmt.Errorf(
			"best pointer for run %s has empty candidate_content_sha — the run produced no kept content candidate", runID)
	}

	candidate, err := readCandidateContentFromTrial(store, runID, best)
	if err != nil {
		return err
	}

	if opts.writePath == "" {
		_, perr := io.WriteString(w, candidate)
		return perr
	}

	if err := guardInsideRepo(opts.writePath, manifest.Surface, opts.forceInsideRepo); err != nil {
		return err
	}

	if err := os.WriteFile(opts.writePath, []byte(candidate), 0o600); err != nil {
		return fmt.Errorf("writing candidate to %s: %w", opts.writePath, err)
	}

	_, _ = fmt.Fprintf(w,
		"autoresearch apply %s: wrote best candidate (score=%g, trial_n=%d, sha=%s) to %s\n",
		runID, best.Score, best.TrialN, best.CandidateContentSHA, opts.writePath)
	return nil
}

// readApplyBestPointer reads the run's best pointer and surfaces a
// clear error when the pointer is missing or empty. Distinguishes
// "no kept candidate" (operator-facing) from a generic store failure.
func readApplyBestPointer(store coordination.Store, runID string) (bestRecord, error) {
	key := fmt.Sprintf("autoresearch/%s/best", runID)
	raw, err := store.Get(key)
	if err != nil {
		if errors.Is(err, coordination.ErrKeyNotFound) {
			return bestRecord{}, fmt.Errorf(
				"no best pointer for run %s — the run produced no kept candidates; "+
					"inspect with `flowstate autoresearch list`", runID)
		}
		return bestRecord{}, fmt.Errorf("reading %s: %w", key, err)
	}
	var rec bestRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return bestRecord{}, fmt.Errorf("decoding %s: %w", key, err)
	}
	return rec, nil
}

// readApplyManifestRecord reads the manifest so apply can inspect the
// `commit_trials` flag and resolve the surface repo for the
// inside-repo guard. Wraps the missing-record error in operator-
// facing prose.
func readApplyManifestRecord(store coordination.Store, runID string) (manifestRecord, error) {
	raw, err := store.Get(manifestKey(runID))
	if err != nil {
		if errors.Is(err, coordination.ErrKeyNotFound) {
			return manifestRecord{}, fmt.Errorf(
				"no manifest record for run %s — run id may be wrong or the record was pruned", runID)
		}
		return manifestRecord{}, fmt.Errorf("reading %s: %w", manifestKey(runID), err)
	}
	var rec manifestRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return manifestRecord{}, fmt.Errorf("decoding %s: %w", manifestKey(runID), err)
	}
	return rec, nil
}

// readCandidateContentFromTrial walks the trial records under the run
// and returns the candidate_content of the trial whose
// candidate_content_sha matches the best pointer. The walk is linear
// in trial count — runs ship with O(10s) of trials, well below any
// performance threshold that would justify an index.
//
// A best pointer with no matching trial body is an operator-facing
// inconsistency (e.g. the trial record was hand-pruned or
// candidate_content_truncated=true and the cap dropped the body).
// Surface a descriptive error so the operator knows to widen the cap
// or re-run.
func readCandidateContentFromTrial(store coordination.Store, runID string, best bestRecord) (string, error) {
	keys, err := store.List(fmt.Sprintf("autoresearch/%s/", runID))
	if err != nil {
		return "", fmt.Errorf("listing trials for %s: %w", runID, err)
	}
	for _, k := range keys {
		if !strings.Contains(k, "/trial-") {
			continue
		}
		raw, getErr := store.Get(k)
		if getErr != nil {
			continue
		}
		var trial trialOutcome
		if json.Unmarshal(raw, &trial) != nil {
			continue
		}
		if trial.CandidateContentSHA != best.CandidateContentSHA {
			continue
		}
		if trial.CandidateContent == "" {
			return "", fmt.Errorf(
				"trial %d for run %s has empty candidate_content (truncated=%t); raise --max-candidate-bytes and re-run, or use a separate write-the-candidate evaluator path",
				trial.N, runID, trial.CandidateContentTruncated)
		}
		return trial.CandidateContent, nil
	}
	return "", fmt.Errorf(
		"no trial record matches best.candidate_content_sha=%s for run %s — coord-store inconsistency",
		best.CandidateContentSHA, runID)
}

// guardInsideRepo refuses --write paths that resolve under the
// surface repo's working tree, unless force is true. The check is
// path-based: we walk the destination's parents looking for a `.git`
// marker; if any parent matches the surface's repo root the
// destination is "inside-repo".
//
// This is the architectural guard for the content substrate's
// non-mutating contract — without it, the operator's first instinct
// (pipe `apply` to a path next to the surface) silently re-engages
// the disk-write workflow `--commit-trials` exists to handle.
func guardInsideRepo(writePath, surfacePath string, force bool) error {
	if force {
		return nil
	}
	absDest, err := filepath.Abs(writePath)
	if err != nil {
		return fmt.Errorf("resolving --write path %q: %w", writePath, err)
	}
	surfaceRepo, err := surfaceRepoRoot(surfacePath)
	if err != nil {
		// If the surface itself is no longer in a git repo, there is
		// nothing to guard against; allow the write.
		return nil
	}
	if pathContains(surfaceRepo, absDest) {
		return fmt.Errorf(
			"--write %q is inside the surface repo at %s; pass --force-inside-repo to override (the content substrate keeps the project tree untouched by default)",
			writePath, surfaceRepo)
	}
	return nil
}
