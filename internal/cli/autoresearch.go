// Package cli's autoresearch.go implements the `flowstate autoresearch`
// command group and its `run` subcommand — the MVP loop spine for the
// Karpathy-style ratchet harness (vault plan: Autoresearch Loop
// Integration (April 2026), § 5.5).
//
// Slice 1 hard-codes the MVP shape: surface defaults to
// `internal/app/agents/planner.md` under cfg.AgentDir, evaluator
// defaults to `scripts/validate-harness.sh --score`, program is the
// `autoresearch` skill (authored in Slice 2). Flexibility flags
// (`--surface` arbitrary path, `--evaluator <path>`, `--program
// <skill|path>`) arrive in Slices 4–6 — the plumbing landed here is
// intentionally restrictive so future slices extend, not retrofit.
//
// The harness owns the worktree's git history end-to-end. Per-trial
// commits inside the worktree MUST use `--no-verify` (plan § 5.5 N13
// + [[make check Gate Structurally Broken on Origin (April 2026)]]);
// operators never see the per-trial commits except via the kept-
// commit cherry-pick at run end (Slice 1d closes that loop).
//
// Concurrency: Slice 1 takes the engineer-choice route on advisory
// locking — full file-lock plumbing is deferred. Operators are
// expected not to mutate the surface from another tool while
// `flowstate autoresearch run` is iterating; this is documented in
// the run command's Long help. The trial worktree is isolated, so
// the operator's tree is safe from harness-side mutation.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// autoresearchRunOptions holds the parsed flag values for one run.
type autoresearchRunOptions struct {
	surface         string
	maxTrials       int
	metricDirection string
	timeBudget      time.Duration
	runID           string
	worktreeBase    string
	noImproveWindow int
	driverScript    string
	evaluatorScript string
}

// metric direction enumeration.
const (
	metricDirectionMin = "min"
	metricDirectionMax = "max"
)

// newAutoresearchCmd creates the `autoresearch` parent command group.
//
// Expected:
//   - getApp is a non-nil function returning the application instance.
//
// Returns:
//   - A configured cobra.Command group with the run subcommand attached.
//
// Side effects:
//   - Registers the run subcommand.
func newAutoresearchCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "autoresearch",
		Short: "Run the autoresearch ratchet loop",
		Long: "Karpathy-style optimisation loop that ratchets a surface " +
			"file (manifest, skill body, source) against an integer " +
			"scalar evaluator. Each trial runs in an isolated git " +
			"worktree; kept commits cherry-pick back at run end.\n\n" +
			"This is the MVP spine — surface, evaluator, and program " +
			"are hard-coded to the planner.md ratchet shape; " +
			"flexibility lands in later slices.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newAutoresearchRunCmd(getApp))
	return cmd
}

// newAutoresearchRunCmd creates the `autoresearch run` subcommand.
//
// Expected:
//   - getApp is a non-nil function returning the application instance.
//
// Returns:
//   - A configured cobra.Command with all Slice 1 flags wired.
//
// Side effects:
//   - Reads the parent repo's git status to enforce the clean-tree
//     precondition (§ 5.5).
//   - Creates a worktree under <worktree-base>/<runID>/worktree.
//   - Writes manifest record to coord-store.
func newAutoresearchRunCmd(getApp func() *app.App) *cobra.Command {
	var opts autoresearchRunOptions

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run a one-shot autoresearch ratchet against the surface",
		Long: "Execute the loop synchronously: clean-tree precondition, " +
			"worktree create, candidate edit (driver), manifest gate, " +
			"score, ratchet, repeat until termination. The harness " +
			"is stateless between trials; all state lives in the " +
			"worktree's git history and the coord-store.\n\n" +
			"Operators MUST NOT mutate the surface file from another " +
			"tool while `autoresearch run` is iterating — Slice 1 " +
			"defers full advisory locking.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAutoresearch(cmd.Context(), cmd, getApp(), opts)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.surface, "surface", "",
		"Path to the surface file (default: <agents-dir>/planner.md)")
	flags.IntVar(&opts.maxTrials, "max-trials", 10,
		"Maximum number of trials before terminating with reason=max-trials")
	flags.StringVar(&opts.metricDirection, "metric-direction", metricDirectionMin,
		"Score-direction: 'min' keeps trials that lower the score; 'max' keeps trials that raise it")
	flags.DurationVar(&opts.timeBudget, "time-budget", 5*time.Minute,
		"Wall-clock budget; exceeded → terminate with reason=time-budget")
	flags.StringVar(&opts.runID, "run-id", "",
		"Run identifier; empty → generated UUID")
	flags.StringVar(&opts.worktreeBase, "worktree-base", "",
		"Worktree parent directory; empty → <DataDir>/autoresearch/<runID>/worktree")
	flags.IntVar(&opts.noImproveWindow, "no-improve-window", 5,
		"Consecutive non-improving trials before terminating with reason=converged")
	flags.StringVar(&opts.driverScript, "driver-script", "",
		"Fixture driver script path (testing only; produces candidate edits in the worktree)")
	flags.StringVar(&opts.evaluatorScript, "evaluator-script", "",
		"Evaluator script path (default: scripts/validate-harness.sh --score)")

	return cmd
}

// runAutoresearch drives one autoresearch run end-to-end.
//
// Slice 1b establishes the spine: option resolution, surface
// validation, clean-tree precondition, worktree creation, manifest
// record write, run-id generation. The trial loop and termination
// matrix arrive in Slice 1c; the final summary in Slice 1d.
//
// Expected:
//   - cmd is a non-nil cobra.Command with an initialised output writer.
//   - application is a non-nil App with Config.DataDir resolved.
//   - opts contains the parsed flag values.
//
// Returns:
//   - nil on a successful setup-only run (max-trials=0).
//   - non-nil error if any precondition fails.
//
// Side effects:
//   - Creates a worktree under <worktree-base>/<runID>/worktree.
//   - Writes the manifest record to <DataDir>/coordination.json.
func runAutoresearch(_ context.Context, cmd *cobra.Command, application *app.App, opts autoresearchRunOptions) error {
	resolved, err := resolveAutoresearchOptions(application, opts)
	if err != nil {
		return err
	}

	surfaceRepoRoot, err := surfaceRepoRoot(resolved.surface)
	if err != nil {
		return fmt.Errorf("resolving surface repo root: %w", err)
	}

	if err := requireCleanTree(surfaceRepoRoot); err != nil {
		return err
	}

	worktreePath := filepath.Join(resolved.worktreeBase, resolved.runID, "worktree")
	if err := createTrialWorktree(surfaceRepoRoot, worktreePath); err != nil {
		return fmt.Errorf("creating worktree: %w", err)
	}

	store, err := openCoordStore(application)
	if err != nil {
		return err
	}

	if err := writeManifestRecord(store, resolved, worktreePath); err != nil {
		return fmt.Errorf("writing manifest record: %w", err)
	}

	// Slice 1b stops here — Slice 1c wires the trial loop, Slice 1d the
	// final summary. A max-trials=0 run is a clean spine smoke that
	// proves the precondition + worktree + record path.
	if resolved.maxTrials == 0 {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"autoresearch run %s: setup complete (max-trials=0; no trials run)\n",
			resolved.runID)
		return nil
	}

	// Slice 1c will replace this stub with the trial loop.
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"autoresearch run %s: setup complete (trial loop lands in Slice 1c)\n",
		resolved.runID)
	return nil
}

// resolveAutoresearchOptions normalises CLI flags into the run shape:
// fills defaults, generates the run-id, and validates the metric
// direction and surface path. Surface defaults to
// <agents-dir>/planner.md per the MVP hard-coding.
//
// Expected:
//   - application is a non-nil App.
//   - opts contains the parsed flag values.
//
// Returns:
//   - The resolved options with all defaults filled.
//   - An error if any flag is invalid.
//
// Side effects:
//   - Stat-checks the surface path.
func resolveAutoresearchOptions(application *app.App, opts autoresearchRunOptions) (autoresearchRunOptions, error) {
	if opts.metricDirection != metricDirectionMin && opts.metricDirection != metricDirectionMax {
		return opts, fmt.Errorf(
			"invalid --metric-direction %q: must be %q or %q",
			opts.metricDirection, metricDirectionMin, metricDirectionMax,
		)
	}

	if opts.surface == "" {
		opts.surface = filepath.Join(application.Config.AgentDir, "planner.md")
	}
	if info, err := os.Stat(opts.surface); err != nil {
		return opts, fmt.Errorf("--surface %q: %w", opts.surface, err)
	} else if info.IsDir() {
		return opts, fmt.Errorf("--surface %q: not a regular file", opts.surface)
	}

	if opts.runID == "" {
		opts.runID = uuid.NewString()
	}

	if opts.worktreeBase == "" {
		opts.worktreeBase = filepath.Join(application.Config.DataDir, "autoresearch")
	}

	return opts, nil
}

// surfaceRepoRoot returns the parent git repository root for the
// surface path. The harness invariant is that the surface lives
// inside a git working tree the operator controls; we walk upwards
// from the surface until we find a `.git` directory or file (the
// latter for worktrees themselves).
//
// Expected:
//   - surface is an absolute or relative path to an existing file.
//
// Returns:
//   - The repo root path containing the surface.
//   - An error if no enclosing git repo is found.
//
// Side effects:
//   - Stat-checks ancestors of the surface path.
func surfaceRepoRoot(surface string) (string, error) {
	abs, err := filepath.Abs(surface)
	if err != nil {
		return "", fmt.Errorf("absolute path: %w", err)
	}
	dir := filepath.Dir(abs)
	for {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			_ = info
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no enclosing git repository for %s", surface)
		}
		dir = parent
	}
}

// requireCleanTree enforces the dirty-tree precondition (§ 5.5):
// the parent working tree must be clean before a run starts. The
// harness creates a worktree off the same repo, so a dirty tree
// risks mixing operator-staged changes into a trial.
//
// Expected:
//   - repoRoot is the repo root path returned by surfaceRepoRoot.
//
// Returns:
//   - nil if `git status --porcelain` is empty.
//   - An error mentioning "dirty" otherwise.
//
// Side effects:
//   - Shells `git status --porcelain` against the repoRoot.
func requireCleanTree(repoRoot string) error {
	cmd := exec.Command("git", "-C", repoRoot, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("running git status in %s: %w", repoRoot, err)
	}
	if len(strings.TrimSpace(string(output))) > 0 {
		return fmt.Errorf(
			"surface working tree at %s is dirty; commit or stash before running autoresearch:\n%s",
			repoRoot, strings.TrimSpace(string(output)))
	}
	return nil
}

// createTrialWorktree adds a git worktree at worktreePath off the
// surface repo's HEAD. The worktree is the harness's owned playground
// for per-trial commits; the operator's tree is never touched.
//
// Expected:
//   - repoRoot is the parent repo root.
//   - worktreePath is the desired worktree path; its parent
//     directories are created as needed.
//
// Returns:
//   - nil on a successful `git worktree add`.
//   - non-nil error if the worktree cannot be created.
//
// Side effects:
//   - Creates parent directories of worktreePath.
//   - Calls `git worktree add` to materialise the worktree.
func createTrialWorktree(repoRoot, worktreePath string) error {
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return fmt.Errorf("creating worktree parent: %w", err)
	}
	// Reuse the parent's HEAD; no branch is created. The harness
	// will commit per-trial against the detached HEAD inside the
	// worktree (Slice 1c). Slice 1d cherry-picks kept commits back.
	cmd := exec.Command("git", "-C", repoRoot, "worktree", "add", "--detach", worktreePath, "HEAD")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add: %w (output: %s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// openCoordStore opens (or creates) the coord-store file at
// <DataDir>/coordination.json and returns a Store handle. Mirrors the
// pattern used by the coordination prune command at coordination.go:171.
//
// Expected:
//   - application is a non-nil App with a non-empty DataDir.
//
// Returns:
//   - A coordination.Store on success.
//   - An error if the store cannot be opened.
//
// Side effects:
//   - May create the coord-store file on first use.
func openCoordStore(application *app.App) (coordination.Store, error) {
	if application.Config == nil || application.Config.DataDir == "" {
		return nil, errors.New("autoresearch requires a DataDir-configured app")
	}
	coordPath := filepath.Join(application.Config.DataDir, "coordination.json")
	store, err := coordination.NewFileStore(coordPath)
	if err != nil {
		return nil, fmt.Errorf("opening coord-store at %s: %w", coordPath, err)
	}
	return store, nil
}

// manifestRecord is the shape persisted at autoresearch/<runID>/manifest.
// Schema per plan § 4.2: surface, evaluator, program, surface_type,
// metric_direction, max_trials, time_budget, no_improve_window,
// baseline_score, baseline_commit, started_at, worktree_path. Slice 1b
// fills the fields it knows; surface_type/baseline_score/baseline_commit
// are pinned in later slices. evaluator and program carry the MVP
// hard-coded defaults so the record is consistent.
type manifestRecord struct {
	Surface         string `json:"surface"`
	Evaluator       string `json:"evaluator"`
	Program         string `json:"program"`
	MetricDirection string `json:"metric_direction"`
	MaxTrials       int    `json:"max_trials"`
	TimeBudget      string `json:"time_budget"`
	NoImproveWindow int    `json:"no_improve_window"`
	StartedAt       string `json:"started_at"`
	WorktreePath    string `json:"worktree_path"`
}

// writeManifestRecord serialises the run config to the coord-store at
// `autoresearch/<runID>/manifest`.
//
// Expected:
//   - store is a non-nil coordination.Store.
//   - opts is the resolved-options struct.
//   - worktreePath is the absolute worktree path.
//
// Returns:
//   - nil on successful Set.
//   - An error wrapping the underlying store failure.
//
// Side effects:
//   - Writes one entry to the coord-store.
func writeManifestRecord(store coordination.Store, opts autoresearchRunOptions, worktreePath string) error {
	rec := manifestRecord{
		Surface:         opts.surface,
		Evaluator:       defaultEvaluator(opts.evaluatorScript),
		Program:         "autoresearch",
		MetricDirection: opts.metricDirection,
		MaxTrials:       opts.maxTrials,
		TimeBudget:      opts.timeBudget.String(),
		NoImproveWindow: opts.noImproveWindow,
		StartedAt:       time.Now().UTC().Format(time.RFC3339),
		WorktreePath:    worktreePath,
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshalling manifest record: %w", err)
	}
	key := manifestKey(opts.runID)
	if err := store.Set(key, raw); err != nil {
		return fmt.Errorf("setting %s: %w", key, err)
	}
	return nil
}

// manifestKey returns the coord-store key for a run's manifest record.
func manifestKey(runID string) string {
	return "autoresearch/" + runID + "/manifest"
}

// defaultEvaluator returns the configured evaluator command label:
// the explicit --evaluator-script when supplied, otherwise the
// hard-coded MVP default `scripts/validate-harness.sh --score`.
func defaultEvaluator(supplied string) string {
	if supplied != "" {
		return supplied
	}
	return "scripts/validate-harness.sh --score"
}
