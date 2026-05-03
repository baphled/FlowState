// Package cli's autoresearch_prune.go implements the `flowstate autoresearch
// prune` subcommand — housekeeping for the coordination store, deleting all
// keys belonging to autoresearch runs that are older than --older-than
// (default 7 days).
//
// Key structure deleted per run:
//
//	autoresearch/<runID>/manifest
//	autoresearch/<runID>/trial-N   (all matching keys discovered via List)
//	autoresearch/<runID>/best
//	autoresearch/<runID>/seen-candidates
//	autoresearch/<runID>/result

package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/spf13/cobra"
)

// autoresearchPruneOptions holds the parsed flag values for one prune invocation.
type autoresearchPruneOptions struct {
	olderThan time.Duration
	all       bool
	dryRun    bool
	yes       bool
}

// newAutoresearchPruneCmd creates the `autoresearch prune` subcommand.
//
// Expected:
//   - getApp is a non-nil function returning the application instance.
//
// Returns:
//   - A configured cobra.Command for the prune subcommand.
//
// Side effects:
//   - Opens the coord-store at <DataDir>/coordination.json.
//   - Deletes keys from the coord-store (unless --dry-run).
//   - Reads from stdin for the --all confirmation prompt (unless --yes).
func newAutoresearchPruneCmd(getApp func() *app.App) *cobra.Command {
	var opts autoresearchPruneOptions

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete coordination store keys for old autoresearch runs",
		Long: "Scan the coordination store for autoresearch run records and " +
			"delete all keys belonging to runs whose manifest `started_at` " +
			"timestamp is older than --older-than (default 168h = 7 days).\n\n" +
			"Pass --all to prune every run regardless of age (confirmation " +
			"prompt unless --yes is also set). Pass --dry-run to see what " +
			"would be deleted without making any changes.\n\n" +
			"Keys deleted per run: manifest, trial-N (all), best, " +
			"seen-candidates, result.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAutoresearchPrune(cmd.InOrStdin(), cmd.OutOrStdout(), getApp(), opts)
		},
	}

	cmd.Flags().DurationVar(&opts.olderThan, "older-than", 168*time.Hour,
		"Prune runs older than this duration (default 168h = 7 days)")
	cmd.Flags().BoolVar(&opts.all, "all", false,
		"Prune all runs regardless of age")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false,
		"Show what would be pruned without deleting")
	cmd.Flags().BoolVar(&opts.yes, "yes", false,
		"Skip confirmation prompt (for use in scripts)")

	return cmd
}

// runAutoresearchPrune drives one prune invocation end-to-end.
//
// Expected:
//   - in is the reader used for the --all confirmation prompt.
//   - w is the writer the summary is rendered to.
//   - application is a non-nil App with Config.DataDir resolved.
//   - opts contains the parsed flag values.
//
// Returns:
//   - nil on success, including when no runs qualify for pruning.
//   - non-nil error if the coord-store cannot be opened or a delete fails.
//
// Side effects:
//   - Reads the coord-store.
//   - Deletes matching keys from the coord-store (unless --dry-run).
//   - May read from in for the --all confirmation prompt.
func runAutoresearchPrune(in io.Reader, w io.Writer, application *app.App, opts autoresearchPruneOptions) error {
	if application.Config == nil || application.Config.DataDir == "" {
		return errors.New("autoresearch prune requires a DataDir-configured app")
	}

	store, err := coordination.NewFileStore(filepath.Join(application.Config.DataDir, "coordination.json"))
	if err != nil {
		return fmt.Errorf("opening coord-store: %w", err)
	}

	keys, err := store.List("autoresearch/")
	if err != nil {
		return fmt.Errorf("listing coord-store: %w", err)
	}

	runIDs := uniqueAutoresearchRunIDs(keys)
	if len(runIDs) == 0 {
		_, perr := fmt.Fprintln(w, "No autoresearch runs in coord-store.")
		return perr
	}

	cutoff := time.Now().UTC().Add(-opts.olderThan)

	// Identify runs that qualify for pruning.
	candidates := pruneEligibleRuns(store, runIDs, opts.all, cutoff)
	if len(candidates) == 0 {
		_, perr := fmt.Fprintln(w, "No runs match the prune criteria.")
		return perr
	}

	// --all requires confirmation unless --yes is set.
	if opts.all && !opts.yes && !opts.dryRun {
		if confirmed, promptErr := confirmPruneAll(in, w, len(candidates)); promptErr != nil {
			return promptErr
		} else if !confirmed {
			_, _ = fmt.Fprintln(w, "Prune cancelled.")
			return nil
		}
	}

	// Collect all keys to delete across all candidate runs.
	var plan []runKeys
	for _, runID := range candidates {
		rk := runKeys{id: runID}
		rk.keys = keysForRun(store, runID)
		if len(rk.keys) > 0 {
			plan = append(plan, rk)
		}
	}

	if opts.dryRun {
		return printDryRunPlan(w, plan)
	}

	return executePrune(w, store, plan)
}

// pruneEligibleRuns returns the run IDs that qualify for pruning.
//
// When all is true every run qualifies regardless of age. When all is
// false only runs whose manifest started_at is before cutoff qualify.
// Runs with an unreadable or unparseable manifest are silently skipped —
// they are not deleted without an explicit decision by the operator.
//
// Expected:
//   - store is an opened coordination store.
//   - runIDs is the full list of run IDs present in the store.
//   - all determines whether age filtering applies.
//   - cutoff is the threshold; runs before this time qualify.
//
// Returns:
//   - A slice of run IDs eligible for pruning (may be empty).
func pruneEligibleRuns(store coordination.Store, runIDs []string, all bool, cutoff time.Time) []string {
	var eligible []string
	for _, runID := range runIDs {
		if all {
			eligible = append(eligible, runID)
			continue
		}
		startedAt, ok := readRunStartedAt(store, runID)
		if !ok {
			// Cannot parse the manifest — skip rather than silently delete.
			continue
		}
		if startedAt.Before(cutoff) {
			eligible = append(eligible, runID)
		}
	}
	return eligible
}

// readRunStartedAt reads the manifest record for runID and returns its
// started_at timestamp. Returns false when the manifest is missing or
// the timestamp field cannot be parsed.
//
// Expected:
//   - store is an opened coordination store.
//   - runID is the run identifier to look up.
//
// Returns:
//   - The parsed time.Time and true on success.
//   - Zero time and false when the manifest is unreadable or unparseable.
func readRunStartedAt(store coordination.Store, runID string) (time.Time, bool) {
	raw, err := store.Get(manifestKey(runID))
	if err != nil {
		return time.Time{}, false
	}
	var rec manifestRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return time.Time{}, false
	}
	if rec.StartedAt == "" {
		return time.Time{}, false
	}
	t, parseErr := time.Parse(time.RFC3339, rec.StartedAt)
	if parseErr != nil {
		return time.Time{}, false
	}
	return t, true
}

// keysForRun returns all coord-store keys that belong to runID.
//
// The known fixed keys (manifest, best, seen-candidates, result) are
// checked and included only when present in the store. Trial keys are
// discovered via List so no fixed count is assumed.
//
// Expected:
//   - store is an opened coordination store.
//   - runID is the run identifier to enumerate keys for.
//
// Returns:
//   - A slice of keys that exist in the store for this run.
func keysForRun(store coordination.Store, runID string) []string {
	prefix := "autoresearch/" + runID + "/"
	allKeys, _ := store.List(prefix)

	// Return all discovered keys — trial-N, manifest, best, result, etc.
	return allKeys
}

// confirmPruneAll writes a confirmation prompt and reads one line from
// in. Returns true when the response is "y" or "yes" (case-insensitive).
//
// Expected:
//   - in is the reader to read the confirmation from.
//   - w is the writer the prompt is rendered to.
//   - count is the number of runs to be pruned (used in the prompt text).
//
// Returns:
//   - true when the operator confirmed, false otherwise.
//   - non-nil error on read failure.
func confirmPruneAll(in io.Reader, w io.Writer, count int) (bool, error) {
	_, _ = fmt.Fprintf(w, "About to prune ALL %d autoresearch run(s). This cannot be undone.\nContinue? [y/N] ", count)
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return false, nil
	}
	response := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return response == "y" || response == "yes", nil
}

// printDryRunPlan writes a dry-run listing to w without deleting anything.
//
// Expected:
//   - w is the writer the listing is rendered to.
//   - plan is the list of runs and their keys that would be deleted.
//
// Returns:
//   - nil on success.
func printDryRunPlan(w io.Writer, plan []runKeys) error {
	totalKeys := 0
	for _, rk := range plan {
		totalKeys += len(rk.keys)
	}
	_, _ = fmt.Fprintf(w, "Dry run — would prune %d run(s), %d key(s):\n", len(plan), totalKeys)
	for _, rk := range plan {
		_, _ = fmt.Fprintf(w, "  run %s: %d key(s)\n", shortenRunID(rk.id), len(rk.keys))
		for _, k := range rk.keys {
			_, _ = fmt.Fprintf(w, "    - %s\n", k)
		}
	}
	return nil
}

// executePrune deletes all keys in plan from the store and prints a
// summary to w.
//
// Expected:
//   - w is the writer the summary is rendered to.
//   - store is an opened coordination store.
//   - plan is the list of runs and their keys to delete.
//
// Returns:
//   - nil on success.
//   - non-nil error if any deletion fails.
func executePrune(w io.Writer, store coordination.Store, plan []runKeys) error {
	totalKeys := 0
	for _, rk := range plan {
		for _, k := range rk.keys {
			if err := store.Delete(k); err != nil && !errors.Is(err, coordination.ErrKeyNotFound) {
				return fmt.Errorf("deleting key %q: %w", k, err)
			}
			totalKeys++
		}
	}
	_, perr := fmt.Fprintf(w, "Pruned %d run(s), %d key(s).\n", len(plan), totalKeys)
	return perr
}

// runKeys is a helper grouping a run ID with its discovered store keys.
type runKeys struct {
	id   string
	keys []string
}
