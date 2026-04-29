// Package cli's autoresearch_promote.go implements the `flowstate
// autoresearch promote <run-id>` subcommand — Slice 4 of the
// [[Autoresearch Harness Lifecycle Fix (April 2026)]] plan.
//
// promote cherry-picks the best-scoring kept commit from a finished
// run's branch (`autoresearch/<run-id-short>`, the named-branch
// convention introduced in Slice 1) onto the parent branch. The
// default is dry-run (mirrors `coordination prune` shape); pass
// --apply to actually cherry-pick.
//
// promote refuses to run when the parent's HEAD is detached unless
// --target <branch> is supplied — cherry-picking onto a detached HEAD
// would re-create the orphaned-commit failure mode this plan is
// fixing.

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/spf13/cobra"
)

// autoresearchPromoteOptions holds the parsed flag values for one
// promote invocation.
type autoresearchPromoteOptions struct {
	apply  bool
	target string
}

// newAutoresearchPromoteCmd creates the `autoresearch promote`
// subcommand.
//
// Expected:
//   - getApp is a non-nil function returning the application instance.
//
// Returns:
//   - A configured cobra.Command for the promote subcommand.
//
// Side effects:
//   - Reads the coord-store at <DataDir>/coordination.json.
//   - On --apply: shells `git cherry-pick` against the parent repo.
func newAutoresearchPromoteCmd(getApp func() *app.App) *cobra.Command {
	var opts autoresearchPromoteOptions

	cmd := &cobra.Command{
		Use:   "promote <run-id>",
		Short: "Cherry-pick the best kept commit from a run onto the parent branch",
		Long: "Read the run's `best` pointer from the coord-store and " +
			"cherry-pick that commit from the run's branch " +
			"(autoresearch/<run-id-short>) onto the parent branch.\n\n" +
			"Dry-run by default — pass --apply to actually cherry-pick. " +
			"When the parent's HEAD is detached, --target <branch> is " +
			"required (cherry-picking onto a detached HEAD would " +
			"re-create the orphan-commit failure mode this command exists " +
			"to fix).\n\n" +
			"Lifecycle plan (April 2026) Slice 4.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAutoresearchPromote(cmd, getApp(), args[0], opts)
		},
	}

	cmd.Flags().BoolVar(&opts.apply, "apply", false,
		"Actually cherry-pick the best commit (default is dry-run)")
	cmd.Flags().StringVar(&opts.target, "target", "",
		"Cherry-pick onto this branch instead of the current parent HEAD; required when the parent's HEAD is detached")

	return cmd
}

// runAutoresearchPromote drives one promote invocation end-to-end.
//
// Expected:
//   - cmd is a non-nil cobra.Command with an initialised output writer.
//   - application is a non-nil App with Config.DataDir resolved.
//   - runID is the operator-supplied run identifier (matches the
//     coord-store key under `autoresearch/<runID>/`).
//
// Returns:
//   - nil on a successful promote (dry-run or apply).
//   - non-nil error if the coord-store can't be opened, the best
//     pointer is missing, the parent is detached without --target, or
//     the cherry-pick fails.
//
// Side effects:
//   - Reads the coord-store.
//   - On --apply: runs `git cherry-pick <sha>` against the parent.
//   - Writes a human-readable report to cmd.OutOrStdout().
func runAutoresearchPromote(cmd *cobra.Command, application *app.App, runID string, opts autoresearchPromoteOptions) error {
	w := cmd.OutOrStdout()
	if application.Config == nil || application.Config.DataDir == "" {
		return errors.New("autoresearch promote requires a DataDir-configured app")
	}
	coordPath := filepath.Join(application.Config.DataDir, "coordination.json")

	store, err := coordination.NewFileStore(coordPath)
	if err != nil {
		return fmt.Errorf("opening coord-store: %w", err)
	}

	best, err := readBestPointer(store, runID)
	if err != nil {
		return err
	}

	manifest, err := readPromoteManifestRecord(store, runID)
	if err != nil {
		return err
	}

	parentRepoRoot, err := surfaceRepoRoot(manifest.Surface)
	if err != nil {
		return fmt.Errorf("resolving surface repo root: %w", err)
	}

	target, err := resolvePromoteTarget(parentRepoRoot, opts.target)
	if err != nil {
		return err
	}

	branchName := autoresearchBranchName(runID)

	if !opts.apply {
		_, _ = fmt.Fprintf(w,
			"autoresearch promote %s: dry-run cherry-pick of %s (score=%g, trial_n=%d) "+
				"from branch %s onto target %s — pass --apply to execute\n",
			runID, best.CommitSHA, best.Score, best.TrialN, branchName, target)
		return nil
	}

	if err := requireCleanTree(parentRepoRoot); err != nil {
		return fmt.Errorf("promote requires a clean parent tree: %w", err)
	}

	if err := checkoutBranch(parentRepoRoot, target); err != nil {
		return err
	}

	cherryCmd := observedCommand("git", "-C", parentRepoRoot, "cherry-pick", best.CommitSHA)
	if output, err := cherryCmd.CombinedOutput(); err != nil {
		return fmt.Errorf(
			"cherry-pick %s onto %s failed: %w\n%s\nresolve manually with `git cherry-pick --abort` or `git cherry-pick --continue`",
			best.CommitSHA, target, err, strings.TrimSpace(string(output)))
	}

	_, _ = fmt.Fprintf(w,
		"autoresearch promote %s: cherry-picked %s (score=%g) onto %s\n",
		runID, best.CommitSHA, best.Score, target)
	return nil
}

// readBestPointer reads the run's best pointer from the coord-store
// at autoresearch/<runID>/best. A missing pointer is operator-facing
// — surface a hint pointing at `autoresearch list` (Slice 5) so the
// operator can confirm whether the run produced any kept candidates.
func readBestPointer(store coordination.Store, runID string) (bestRecord, error) {
	key := fmt.Sprintf("autoresearch/%s/best", runID)
	raw, err := store.Get(key)
	if err != nil {
		// Distinguish "no kept candidate" from a generic store error.
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
	if strings.TrimSpace(rec.CommitSHA) == "" {
		return bestRecord{}, fmt.Errorf(
			"best pointer for run %s has empty commit_sha — the run produced no kept candidates", runID)
	}
	return rec, nil
}

// readPromoteManifestRecord reads the manifest record so promote can
// resolve the parent repo root via the recorded `surface` field.
func readPromoteManifestRecord(store coordination.Store, runID string) (manifestRecord, error) {
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

// resolvePromoteTarget validates the promotion target. When --target
// is supplied it is used verbatim. Otherwise we read the parent's
// HEAD; if HEAD is detached, the function refuses with a clear
// error (R1.4 — promote-from-detached-HEAD is a hard refusal).
func resolvePromoteTarget(parentRepoRoot, explicitTarget string) (string, error) {
	if explicitTarget != "" {
		return explicitTarget, nil
	}
	headCmd := observedCommand("git", "-C", parentRepoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	headOut, err := headCmd.Output()
	if err != nil {
		return "", fmt.Errorf("reading parent HEAD: %w", err)
	}
	head := strings.TrimSpace(string(headOut))
	if head == "" || head == "HEAD" {
		return "", fmt.Errorf(
			"parent HEAD is detached; pass --target <branch> to promote — cherry-picking onto a detached HEAD would orphan the kept commit (the failure mode this command exists to fix)")
	}
	return head, nil
}

// checkoutBranch switches the parent repo to the target branch when
// the current HEAD is something else. This is a best-effort
// convenience so the operator can promote with `--target main`
// without manually checking it out first; failures propagate as
// errors.
func checkoutBranch(parentRepoRoot, target string) error {
	headCmd := observedCommand("git", "-C", parentRepoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	headOut, err := headCmd.Output()
	if err != nil {
		return fmt.Errorf("reading parent HEAD pre-checkout: %w", err)
	}
	if strings.TrimSpace(string(headOut)) == target {
		return nil
	}
	checkoutCmd := observedCommand("git", "-C", parentRepoRoot, "checkout", target)
	if output, err := checkoutCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout %s: %w (output: %s)", target, err, strings.TrimSpace(string(output)))
	}
	return nil
}
