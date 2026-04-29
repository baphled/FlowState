// Package cli's autoresearch_list.go implements the `flowstate
// autoresearch list` subcommand — Slice 5 of the
// [[Autoresearch Harness Lifecycle Fix (April 2026)]] plan.
//
// The shape mirrors `flowstate session list` (internal/cli/session.go):
// columnar stdout, no flags, one row per run, joining the coord-store
// run record with `git worktree list` to derive a 4-value
// worktree-status enum (R1.5):
//
//   - absent          — worktree removed, branch present (the post-
//     Slice-2 default after a clean termination).
//   - present         — both worktree and branch exist (e.g. a run
//     started with --keep-worktree, or a non-clean termination).
//   - missing-branch  — worktree exists but its branch was deleted
//     (operator surgery; surfaces a recoverable inconsistency).
//   - legacy-detached — worktree on detached HEAD, no branch ref.
//     Pre-Slice-1 runs that operators inherit; the six leftover
//     worktrees from this session land here.

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/spf13/cobra"
)

// runListEntry is one row in the list output, joined from coord-store
// keys and the surface repo's `git worktree list`.
type runListEntry struct {
	RunIDShort  string
	Surface     string
	StartedAt   string
	LastTrialN  int
	KeptCount   int
	BestScore   float64
	BestSet     bool
	Status      string
	WorktreeAt  string
	BranchName  string
	RawRunID    string
}

// Worktree-status enum values per R1.5.
const (
	worktreeStatusAbsent          = "absent"
	worktreeStatusPresent         = "present"
	worktreeStatusMissingBranch   = "missing-branch"
	worktreeStatusLegacyDetached  = "legacy-detached"
)

// newAutoresearchListCmd creates the `autoresearch list` subcommand.
//
// Expected:
//   - getApp is a non-nil function returning the application instance.
//
// Returns:
//   - A configured cobra.Command for the list subcommand.
//
// Side effects:
//   - Reads the coord-store at <DataDir>/coordination.json.
//   - Shells `git worktree list --porcelain` (best-effort) when at
//     least one record points at a surface repo.
func newAutoresearchListCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List autoresearch runs and the state of their worktrees",
		Long: "Enumerate autoresearch runs from the coord-store, joining " +
			"the manifest record with the surface repo's worktree state. " +
			"Status values: absent | present | missing-branch | " +
			"legacy-detached (a pre-Slice-1 run on detached HEAD).\n\n" +
			"Lifecycle plan (April 2026) Slice 5.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAutoresearchList(cmd.OutOrStdout(), getApp())
		},
	}
}

// runAutoresearchList drives one list invocation end-to-end.
//
// Expected:
//   - w is the writer the table is rendered to.
//   - application is a non-nil App with Config.DataDir resolved.
//
// Returns:
//   - nil on success, including when the coord-store has no
//     autoresearch keys (the table renders a no-runs notice).
//   - non-nil error if the coord-store cannot be opened.
//
// Side effects:
//   - Reads the coord-store.
//   - Best-effort `git worktree list --porcelain` against each
//     unique surface repo to derive the status enum.
func runAutoresearchList(w io.Writer, application *app.App) error {
	if application.Config == nil || application.Config.DataDir == "" {
		return errors.New("autoresearch list requires a DataDir-configured app")
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

	entries := make([]runListEntry, 0, len(runIDs))
	worktreeIndex := map[string]map[string]worktreeInfo{}
	branchIndex := map[string]map[string]struct{}{}

	for _, runID := range runIDs {
		entry, ok := buildRunListEntry(store, runID)
		if !ok {
			continue
		}
		// Lazily probe each unique repo root once per run.
		repoRoot, _ := surfaceRepoRoot(entry.Surface)
		if repoRoot != "" {
			if _, seen := worktreeIndex[repoRoot]; !seen {
				worktreeIndex[repoRoot] = parseWorktreeList(repoRoot)
				branchIndex[repoRoot] = parseAutoresearchBranches(repoRoot)
			}
			entry.Status = classifyWorktreeStatus(entry, worktreeIndex[repoRoot], branchIndex[repoRoot])
		} else {
			entry.Status = worktreeStatusAbsent
		}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].StartedAt > entries[j].StartedAt
	})

	renderRunListTable(w, entries)
	return nil
}

// uniqueAutoresearchRunIDs returns the deduplicated set of run IDs
// derived from coord-store keys of shape `autoresearch/<runID>/...`.
func uniqueAutoresearchRunIDs(keys []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, k := range keys {
		if !strings.HasPrefix(k, "autoresearch/") {
			continue
		}
		rest := strings.TrimPrefix(k, "autoresearch/")
		idx := strings.Index(rest, "/")
		if idx <= 0 {
			continue
		}
		runID := rest[:idx]
		if _, dup := seen[runID]; dup {
			continue
		}
		seen[runID] = struct{}{}
		out = append(out, runID)
	}
	return out
}

// buildRunListEntry assembles one row from the coord-store records
// associated with a run. Returns false when the manifest record is
// absent or unparseable — the row is skipped rather than crashing.
func buildRunListEntry(store coordination.Store, runID string) (runListEntry, bool) {
	manifestRaw, err := store.Get(manifestKey(runID))
	if err != nil {
		return runListEntry{}, false
	}
	var manifest manifestRecord
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return runListEntry{}, false
	}

	entry := runListEntry{
		RawRunID:   runID,
		RunIDShort: shortenRunID(runID),
		Surface:    manifest.Surface,
		StartedAt:  manifest.StartedAt,
		WorktreeAt: manifest.WorktreePath,
		BranchName: autoresearchBranchName(runID),
	}

	// best — set whenever either pointer (commit SHA in --commit-trials
	// mode or content SHA in default content mode) is populated.
	if bestRaw, err := store.Get(fmt.Sprintf("autoresearch/%s/best", runID)); err == nil {
		var best bestRecord
		if json.Unmarshal(bestRaw, &best) == nil {
			entry.BestScore = best.Score
			entry.BestSet = strings.TrimSpace(best.CommitSHA) != "" ||
				strings.TrimSpace(best.CandidateContentSHA) != ""
		}
	}

	// trial- records: count and find max trial-N.
	keys, _ := store.List(fmt.Sprintf("autoresearch/%s/", runID))
	for _, k := range keys {
		if !strings.Contains(k, "/trial-") {
			continue
		}
		tail := k[strings.LastIndex(k, "/trial-")+len("/trial-"):]
		n, convErr := strconv.Atoi(tail)
		if convErr != nil {
			continue
		}
		entry.KeptCount++
		if n > entry.LastTrialN {
			entry.LastTrialN = n
		}
	}

	return entry, true
}

// shortenRunID returns the first 8 characters of a run-id (matching
// the branch-naming convention). Shorter IDs are returned verbatim.
func shortenRunID(runID string) string {
	if len(runID) <= 8 {
		return runID
	}
	return runID[:8]
}

// worktreeInfo carries the bits parsed from `git worktree list
// --porcelain` rows the list command consumes.
type worktreeInfo struct {
	Path     string
	Branch   string // empty for detached
	Detached bool
}

// parseWorktreeList shells `git worktree list --porcelain` against
// repoRoot and returns a map keyed by absolute worktree path. Best-
// effort: git failures yield an empty map so the list still renders.
func parseWorktreeList(repoRoot string) map[string]worktreeInfo {
	out := map[string]worktreeInfo{}
	cmd := observedCommand("git", "-C", repoRoot, "worktree", "list", "--porcelain")
	raw, err := cmd.Output()
	if err != nil {
		return out
	}
	var current worktreeInfo
	flush := func() {
		if current.Path != "" {
			out[current.Path] = current
		}
		current = worktreeInfo{}
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			current.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(line, "branch ")
		case line == "detached":
			current.Detached = true
		}
	}
	flush()
	return out
}

// parseAutoresearchBranches returns the set of branch ref names that
// match `autoresearch/*` in repoRoot. Best-effort: git failures yield
// an empty map.
func parseAutoresearchBranches(repoRoot string) map[string]struct{} {
	out := map[string]struct{}{}
	cmd := observedCommand("git", "-C", repoRoot, "branch", "--list", "autoresearch/*", "--format=%(refname:short)")
	raw, err := cmd.Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			out[name] = struct{}{}
		}
	}
	return out
}

// classifyWorktreeStatus derives the 4-value status enum from a
// joined view of (a) the run's coord-store-recorded worktree path,
// (b) the surface repo's `git worktree list`, and (c) the surface
// repo's `autoresearch/*` branch refs.
func classifyWorktreeStatus(entry runListEntry, worktrees map[string]worktreeInfo, branches map[string]struct{}) string {
	info, present := worktrees[entry.WorktreeAt]
	_, branchPresent := branches[entry.BranchName]
	switch {
	case !present:
		return worktreeStatusAbsent
	case present && info.Detached && !branchPresent:
		return worktreeStatusLegacyDetached
	case present && !branchPresent:
		return worktreeStatusMissingBranch
	default:
		return worktreeStatusPresent
	}
}

// renderRunListTable writes a header line and one row per entry to w.
// The columns mirror `flowstate session list`'s readability shape.
func renderRunListTable(w io.Writer, entries []runListEntry) {
	_, _ = fmt.Fprintf(w, "%-9s  %-50s  %-25s  %-10s  %-10s  %-12s  %s\n",
		"RUN-ID", "SURFACE", "STARTED-AT", "LAST-TRIAL", "KEPT", "BEST-SCORE", "STATUS")
	for _, e := range entries {
		bestCol := "-"
		if e.BestSet {
			bestCol = strconv.FormatFloat(e.BestScore, 'f', -1, 64)
		}
		surface := e.Surface
		if len(surface) > 50 {
			surface = "..." + surface[len(surface)-47:]
		}
		_, _ = fmt.Fprintf(w, "%-9s  %-50s  %-25s  %-10d  %-10d  %-12s  %s\n",
			e.RunIDShort, surface, e.StartedAt, e.LastTrialN, e.KeptCount, bestCol, e.Status)
	}
}
