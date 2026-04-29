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
// Slice 5 formalises the evaluator contract: `--evaluator-script`
// gains up-front path validation (regular file + executable bit),
// `--evaluator-timeout` caps wall-clock per invocation, and
// non-conforming stdout / non-zero exit / timeout all collapse onto
// `evaluator-contract-violation` with the three-strikes
// `evaluator-contract-failure-rate` hard stop. The full operator-
// facing contract lives in skills/autoresearch/SKILL.md "Writing an
// evaluator" and plan v3.1 § 4.6; the runEvaluatorScript doc-comment
// in autoresearch_loop.go pins the same contract at the seam.
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
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// autoresearchRunOptions holds the parsed flag values for one run.
//
// Slice 6 fields:
//   - program              — operator-supplied skill name or path
//   - callingAgentManifest — best-effort path to the calling agent's
//     manifest, used for the N12 de-dup check
//
// Resolved-state fields populated during resolveAutoresearchOptions:
//   - programResolvedPath   — absolute path the program string resolves to
//   - programIsSkillName    — true when the operator supplied a registry
//     name (no '/' and no '.md' suffix)
//   - programDeduplicated   — true when the resolved program skill name
//     matches an entry in the calling agent's always_active_skills
type autoresearchRunOptions struct {
	surface              string
	surfaceType          SurfaceType
	maxTrials            int
	metricDirection      string
	timeBudget           time.Duration
	runID                string
	worktreeBase         string
	noImproveWindow      int
	driverScript         string
	driverTimeout        time.Duration
	driverMaxTurns       int
	promptHistoryWindow  int
	evaluatorScript      string
	evaluatorTimeout     time.Duration
	program              string
	callingAgentManifest string
	programResolvedPath  string
	programIsSkillName   bool
	programDeduplicated  bool
	// keepWorktree opts out of the end-of-run worktree removal on a
	// clean termination (lifecycle plan Slice 2). Default false; the
	// branch is always preserved regardless of this flag. Requires
	// --commit-trials in the content default substrate (April 2026).
	keepWorktree bool
	// allowDirty opts out of the clean-tree precondition (lifecycle
	// plan Slice 3). When set, the harness stashes the parent's
	// uncommitted state at run start and restores it on exit so the
	// trial loop runs against an effectively-clean tree without
	// forcing the operator to commit unrelated edits. Requires
	// --commit-trials in the content default substrate.
	allowDirty bool
	// commitTrials toggles the trial substrate. Default false enables
	// the content loop (April 2026 In-Memory Default plan): the
	// harness reads the surface once, drives the driver via stdin,
	// captures the candidate string from stdout, pipes it to the
	// evaluator, and persists `{candidate_content, candidate_content_sha,
	// score, kept, reason}` per trial — no worktree, no branch, no
	// commit, no `git reset` in the default code path. When true the
	// harness restores today's git-mediated behaviour byte-for-byte
	// (worktree, named branches, per-trial commits, ratchet via
	// reset, promote/list/--allow-dirty/--keep-worktree). The flag is
	// the single switch the pivot's breaking-change strategy hangs on.
	commitTrials bool
	// maxCandidateBytes caps the bytes of candidate content the
	// harness persists on each trial record. Larger candidates are
	// recorded with `candidate_content_truncated=true` and the SHA
	// preserved. Default 256 KiB per the plan; raise for surfaces
	// where full audit trails matter more than coord-store size.
	maxCandidateBytes int
	// programBody is the program-of-record skill body, read once at
	// run start from programResolvedPath. The synthesiser embeds it
	// verbatim in every per-trial prompt so the off-limits constraints
	// reach the live driver without an extra disk read per trial.
	// Live-driver Slice 1.
	programBody string
	// driverAgent is the agent ID forwarded to the driver subprocess
	// as FLOWSTATE_AUTORESEARCH_DRIVER_AGENT. When non-empty the driver
	// script uses this agent instead of its own default. Empty means the
	// driver script falls back to its built-in default (default-assistant).
	driverAgent string
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
	cmd.AddCommand(newAutoresearchPromoteCmd(getApp))
	cmd.AddCommand(newAutoresearchListCmd(getApp))
	cmd.AddCommand(newAutoresearchApplyCmd(getApp))
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
			// Install a signal-linked context so SIGTERM / SIGINT
			// terminates the trial loop with reason=signal and a
			// best-effort result-record write — mirrors the pattern
			// in runPrompt added by f2a23be (see § 4.7 termination
			// matrix). signal.NotifyContext delivers a single
			// cancellation; a second Ctrl-C still kills the process
			// so operators retain the usual escape hatch.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return runAutoresearch(ctx, cmd, getApp(), opts)
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
		"Worktree parent directory; empty → <surfaceRepoRoot>/.flowstate/autoresearch/<runID>/worktree")
	flags.IntVar(&opts.noImproveWindow, "no-improve-window", 5,
		"Consecutive non-improving trials before terminating with reason=converged")
	flags.StringVar(&opts.driverScript, "driver-script", "",
		"Driver script path. In default content mode (April 2026 In-Memory Default plan) the harness pipes the synthesised prompt to the driver via stdin and captures the candidate string from stdout; FLOWSTATE_AUTORESEARCH_PROMPT_FILE is populated as a parallel channel. Under --commit-trials the driver is invoked inside the worktree and edits the surface in place. See scripts/autoresearch-drivers/default-assistant-driver.sh for the canonical reference driver (content mode shape) or default-assistant-driver-commit.sh for the legacy shape.")
	flags.DurationVar(&opts.driverTimeout, "driver-timeout", 3*time.Minute,
		"Per-invocation driver wall-clock cap. SIGTERM at deadline, SIGKILL 30s later. A timeout records `validator-io-error` with a timeout marker and does NOT count toward no-improve-window. Live-driver plan § 4.6 (R1.1).")
	flags.IntVar(&opts.driverMaxTurns, "driver-max-turns", 10,
		"Cap on the agent turns the driver subprocess may use (propagated via FLOWSTATE_AUTORESEARCH_DRIVER_MAX_TURNS). Default 10 covers a reasonable read-prompt → optionally one tool roundtrip → produce edit; raise it for drivers that legitimately investigate.")
	flags.IntVar(&opts.promptHistoryWindow, "prompt-history-window", driverPromptHistoryDefault,
		"Number of recent trial outcomes embedded in the synthesised driver prompt's # HISTORY section (default 5). Higher values show the driver more trajectory at the cost of prompt size.")
	flags.StringVar(&opts.evaluatorScript, "evaluator-script", "",
		"Evaluator script path. Any executable that satisfies the contract in plan v3.1 § 4.6 (one non-negative integer to stdout, exit 0; see skills/autoresearch/SKILL.md \"Writing an evaluator\"). Default: scripts/validate-harness.sh --score")
	flags.DurationVar(&opts.evaluatorTimeout, "evaluator-timeout", 5*time.Minute,
		"Per-invocation evaluator wall-clock cap. SIGTERM at deadline, SIGKILL 30s later. A timeout records `evaluator_timeout_ms` and counts toward `evaluator-contract-failure-rate`")
	flags.StringVar(&opts.program, "program", "autoresearch",
		"Program-of-record for this run. Either a registry skill name (resolved as `skills/<name>/SKILL.md` under the repo root) or a path (anything containing '/' or ending in '.md', resolved relative to the repo root or absolute). Default: `autoresearch`. Pluggable per Slice 6 of the autoresearch plan v3.1.")
	flags.StringVar(&opts.callingAgentManifest, "calling-agent", "",
		"Path to the calling agent's manifest (.json or .md). When supplied AND --program resolves to a registry skill name, the harness consults the manifest's `always_active_skills` for the N12 de-dup check; a match logs a de-dup line and annotates the run's manifest record. Best-effort: missing or unreadable manifests are ignored without error.")
	flags.BoolVar(&opts.keepWorktree, "keep-worktree", false,
		"Preserve the trial worktree directory on a clean termination (default: remove). The branch autoresearch/<run-id-short> is always preserved regardless of this flag — it is the durable kept-commit anchor. Requires --commit-trials in the content default substrate (April 2026); hard-error otherwise. Lifecycle plan (April 2026) Slice 2.")
	flags.BoolVar(&opts.allowDirty, "allow-dirty", false,
		"Bypass the clean-tree precondition by stashing the parent's uncommitted state at run start and restoring it on exit. The trial worktree itself is unaffected — autoresearch always works in an isolated branch. Requires --commit-trials in the content default substrate; hard-error otherwise. Lifecycle plan (April 2026) Slice 3.")
	flags.BoolVar(&opts.commitTrials, "commit-trials", false,
		"Opt in to the legacy git-mediated substrate: trial worktree, named branches, per-trial commits with --no-verify, ratchet via `git reset --hard HEAD~1`, promote/list/--allow-dirty/--keep-worktree. Default off — the harness runs the content loop (read surface once, drive via stdin, score candidates as strings) per the April 2026 In-Memory Default plan.")
	flags.IntVar(&opts.maxCandidateBytes, "max-candidate-bytes", 256*1024,
		"Cap on the candidate-content bytes persisted per trial in the content default substrate. Larger candidates are recorded with `candidate_content_truncated=true` and the content SHA is preserved. Ignored under --commit-trials.")
	flags.StringVar(&opts.driverAgent, "driver-agent", "",
		"Agent to use inside the driver script. Sets FLOWSTATE_AUTORESEARCH_DRIVER_AGENT env var. "+
			"Empty = driver script uses its own default (default-assistant).")

	return cmd
}

// runAutoresearch drives one autoresearch run end-to-end.
//
// April 2026 content substrate: the default code path
// (commit-trials=false) reads the surface once, runs the trial loop
// in memory (driver/evaluator exchange candidate strings via
// stdin/stdout), and never spawns a `git` subprocess. The legacy
// git-mediated substrate (worktree, branches, per-trial commits,
// ratchet via reset) is preserved verbatim behind --commit-trials.
//
// Slice 1b established the spine: option resolution, surface
// validation, clean-tree precondition, worktree creation, manifest
// record write, run-id generation. The trial loop and termination
// matrix landed in Slice 1c; the final summary in Slice 1d.
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
//   - In default mode: writes the manifest record to
//     <DataDir>/coordination.json.
//   - Under --commit-trials: additionally creates a worktree under
//     <worktree-base>/<runID>/worktree.
func runAutoresearch(ctx context.Context, cmd *cobra.Command, application *app.App, opts autoresearchRunOptions) error {
	if err := rejectGitModeFlagsWithoutCommitTrials(cmd, opts); err != nil {
		return err
	}

	resolved, err := resolveAutoresearchOptions(application, opts)
	if err != nil {
		return err
	}

	// Slice 6 — log the N12 de-dup decision before any worktree work
	// so the operator-visible record matches the trial loop's actual
	// behaviour. The log line goes to stdout (same writer used by the
	// run summary) and the boolean lands on the manifest record's
	// program_resolved annotation.
	resolved.programDeduplicated = applyCallingAgentDeDup(resolved, cmd.OutOrStdout())

	if !resolved.commitTrials {
		return runAutoresearchContent(ctx, cmd, application, resolved)
	}
	return runAutoresearchCommitTrials(ctx, cmd, application, resolved)
}

// rejectGitModeFlagsWithoutCommitTrials enforces the content default
// substrate's hard-error contract on git-mode-only flags. --allow-dirty,
// --keep-worktree, and --worktree-base are meaningful only under the
// legacy substrate; passing them without --commit-trials is a
// configuration error the operator must resolve before any side
// effects fire.
//
// Implementation note: cobra's `Flags().Changed(name)` distinguishes
// "explicitly set" from "unset; default-zero", so the guard does not
// trigger on the default zero values produced by the flagset.
func rejectGitModeFlagsWithoutCommitTrials(cmd *cobra.Command, opts autoresearchRunOptions) error {
	if opts.commitTrials {
		return nil
	}
	flags := cmd.Flags()
	for _, name := range []string{"allow-dirty", "keep-worktree", "worktree-base"} {
		if flags == nil {
			break
		}
		if flags.Changed(name) {
			return fmt.Errorf(
				"--%s is meaningful only with --commit-trials; default content mode does not touch the parent tree",
				name)
		}
	}
	return nil
}

// runAutoresearchContent implements the default substrate (April 2026
// content mode). The surface bytes are read once, the trial
// loop drives the driver via stdin/stdout, and candidates flow as
// strings — no worktree, no commits, no `git` subprocesses in this
// code path.
func runAutoresearchContent(ctx context.Context, cmd *cobra.Command, application *app.App, resolved autoresearchRunOptions) error {
	store, err := openCoordStore(application)
	if err != nil {
		return err
	}

	// max-trials=0 is the smoke path — write a manifest record with
	// no baseline data and return cleanly. The content record
	// leaves baseline_commit and worktree_path empty.
	if resolved.maxTrials == 0 {
		if err := writeManifestRecord(store, resolved, "", 0, ""); err != nil {
			return fmt.Errorf("writing manifest record: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"autoresearch run %s: setup complete (max-trials=0; no trials run; mode=content) surface_type=%s\n",
			resolved.runID, string(resolved.surfaceType))
		return nil
	}

	// Read the surface once into memory — the immutable task the
	// driver trains against. Subsequent reads from disk would defeat
	// the substrate swap.
	surfaceBytes, err := os.ReadFile(resolved.surface)
	if err != nil {
		return fmt.Errorf("reading surface: %w", err)
	}
	relSurface, err := surfaceRelativeToRepo(resolved.surface)
	if err != nil {
		return fmt.Errorf("resolving surface relative path: %w", err)
	}

	// Baseline-score the unmodified surface bytes via the content
	// evaluator channel. The harness still treats a baseline contract
	// violation as a hard fail so a broken evaluator is surfaced
	// before any trial runs.
	baseline, err := runEvaluatorContent(ctx, resolved.evaluatorScript, resolved.runID, surfaceBytes, resolved.evaluatorTimeout)
	if err != nil {
		return fmt.Errorf("baseline evaluator: %w", err)
	}
	if baseline.ContractViolation {
		return fmt.Errorf("baseline evaluator: contract violation: %s", baseline.Reason)
	}
	baselineScore := baseline.Score
	if err := writeManifestRecord(store, resolved, "", baselineScore, ""); err != nil {
		return fmt.Errorf("writing manifest record: %w", err)
	}

	baselineSHA := contentSHA(surfaceBytes)
	state := &trialLoopState{
		bestScore:      baselineScore,
		bestScoreSet:   true,
		bestContentSHA: baselineSHA,
		surfaceBytes:   surfaceBytes,
	}
	state.seenCandidates = appendSeenRing(state.seenCandidates, seenCandidate{
		CandidateSHA: baselineSHA, TrialN: 0, Score: baselineScore,
	})
	if err := writeSeenCandidates(store, resolved.runID, state.seenCandidates); err != nil {
		return fmt.Errorf("seeding seen-candidates: %w", err)
	}

	terminationReason, lastOutcome, err := runTrialLoop(ctx, resolved, "", store, cmd.OutOrStdout(), state, relSurface)
	if err != nil {
		return err
	}
	printRunSummary(cmd.OutOrStdout(), resolved, state, lastOutcome, terminationReason, "")
	return nil
}

// runAutoresearchCommitTrials implements the legacy git-mediated
// substrate. Behaviour preserved byte-for-byte from the pre-pivot
// runAutoresearch — only the entry-point branching is new.
func runAutoresearchCommitTrials(ctx context.Context, cmd *cobra.Command, application *app.App, resolved autoresearchRunOptions) error {
	surfaceRepoRoot, err := surfaceRepoRoot(resolved.surface)
	if err != nil {
		return fmt.Errorf("resolving surface repo root: %w", err)
	}

	// Clean-tree precondition. --allow-dirty (lifecycle plan Slice 3)
	// stashes the parent's uncommitted state before the loop and
	// restores it on every exit path; without the flag a dirty tree
	// is rejected as before.
	stashRef, err := preflightCleanTree(cmd.OutOrStdout(), surfaceRepoRoot, resolved.allowDirty)
	if err != nil {
		return err
	}
	defer restoreParentStash(cmd.OutOrStdout(), surfaceRepoRoot, stashRef)

	worktreePath := filepath.Join(resolved.worktreeBase, resolved.runID, "worktree")
	branchName := autoresearchBranchName(resolved.runID)
	if err := createTrialWorktree(surfaceRepoRoot, worktreePath, branchName); err != nil {
		return fmt.Errorf("creating worktree: %w", err)
	}

	store, err := openCoordStore(application)
	if err != nil {
		return err
	}

	if resolved.maxTrials == 0 {
		if err := writeManifestRecord(store, resolved, worktreePath, 0, ""); err != nil {
			return fmt.Errorf("writing manifest record: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"autoresearch run %s: setup complete (max-trials=0; no trials run) surface_type=%s\n",
			resolved.runID, string(resolved.surfaceType))
		return nil
	}

	// Baseline-score the unmodified surface so the manifest record
	// captures both baseline_score and baseline_commit before the
	// trial loop mutates the worktree.
	relSurface, err := relativeSurfacePath(resolved.surface, worktreePath)
	if err != nil {
		return err
	}
	worktreeSurface := filepath.Join(worktreePath, relSurface)

	baseline, err := runEvaluatorScript(resolved.evaluatorScript, worktreePath, relSurface, resolved.runID, resolved.evaluatorTimeout)
	if err != nil {
		return fmt.Errorf("baseline evaluator: %w", err)
	}
	if baseline.ContractViolation {
		return fmt.Errorf("baseline evaluator: contract violation: %s", baseline.Reason)
	}
	baselineScore := baseline.Score
	baselineCommit, err := worktreeHeadSHA(worktreePath)
	if err != nil {
		return fmt.Errorf("baseline commit: %w", err)
	}

	if err := writeManifestRecord(store, resolved, worktreePath, baselineScore, baselineCommit); err != nil {
		return fmt.Errorf("writing manifest record: %w", err)
	}

	baselineSHA, err := surfaceSHA(worktreeSurface)
	if err != nil {
		return fmt.Errorf("hashing baseline surface: %w", err)
	}

	state := &trialLoopState{
		bestScore:    baselineScore,
		bestScoreSet: true,
	}
	state.seenCandidates = appendSeenRing(state.seenCandidates, seenCandidate{
		CandidateSHA: baselineSHA, TrialN: 0, Score: baselineScore,
	})
	if err := writeSeenCandidates(store, resolved.runID, state.seenCandidates); err != nil {
		return fmt.Errorf("seeding seen-candidates: %w", err)
	}

	terminationReason, lastOutcome, err := runTrialLoop(ctx, resolved, worktreePath, store, cmd.OutOrStdout(), state, relSurface)
	if err != nil {
		return err
	}

	printRunSummary(cmd.OutOrStdout(), resolved, state, lastOutcome, terminationReason, worktreePath)
	cleanupTrialWorktree(cmd.OutOrStdout(), surfaceRepoRoot, worktreePath, terminationReason, resolved.keepWorktree)
	return nil
}

// surfaceRelativeToRepo returns the surface path relative to its
// enclosing git repository. Used by the content substrate to give
// the synthesiser a stable relSurface anchor without requiring a
// worktree.
func surfaceRelativeToRepo(surface string) (string, error) {
	repoRoot, err := surfaceRepoRoot(surface)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(surface)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(repoRoot, abs)
	if err != nil {
		return "", fmt.Errorf("surface relative to repo root: %w", err)
	}
	return rel, nil
}

// worktreeHeadSHA returns the worktree's current HEAD SHA via
// `git -C <worktree> rev-parse HEAD`.
func worktreeHeadSHA(worktreePath string) (string, error) {
	cmd := observedCommand("git", "-C", worktreePath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// printRunSummary writes the human-readable end-of-run summary to the
// configured writer. Per § 5.5 the summary lists trials run, kept and
// reverted counts, best score and commit SHA, termination reason,
// run-id, and worktree path so the operator has the breadcrumbs to
// inspect kept commits or trigger Slice 1d's cherry-pick (deferred
// to Slice 4+ — Slice 1's summary is informational only).
func printRunSummary(
	w io.Writer,
	resolved autoresearchRunOptions,
	state *trialLoopState,
	last trialOutcome,
	terminationReason string,
	worktreePath string,
) {
	totalTrials := last.N
	bestSHA := state.bestCommitSHA
	bestScore := state.bestScore
	if !state.bestScoreSet {
		bestSHA = ""
		bestScore = 0
	}
	_, _ = fmt.Fprintf(w,
		"autoresearch run %s: summary trials_run=%d kept=%d reverted=%d "+
			"best_score=%g best_commit=%s surface_type=%s "+
			"termination_reason=%s worktree=%s\n",
		resolved.runID,
		totalTrials,
		state.keptCount,
		state.revertedCount,
		bestScore,
		bestSHA,
		string(resolved.surfaceType),
		terminationReason,
		worktreePath,
	)
}

// resolveAutoresearchOptions normalises CLI flags into the run shape:
// fills defaults, generates the run-id, validates the metric
// direction and surface path, and runs surface-type detection so
// later phases (manifest record write, manifest gate, summary)
// agree on a single classification.
//
// Surface validation (Slice 4 per § 5.8):
//   - Exists, regular file (not a directory).
//   - Readable — opening for reading must succeed; this is the
//     contract the harness assumes when it stages per-trial edits
//     against the worktree mirror of the surface.
//
// Surface type detection per § 4.4:
//   - Path under cfg.AgentDir / cfg.AgentDirs        → manifest
//   - .md with capabilities.tools / delegation key   → manifest
//   - SKILL.md under skills/                         → skill
//   - else                                           → source
//
// Expected:
//   - application is a non-nil App.
//   - opts contains the parsed flag values.
//
// Returns:
//   - The resolved options with all defaults filled and
//     surface_type set.
//   - An error if any flag is invalid.
//
// Side effects:
//   - Stat-checks the surface path.
//   - Opens the surface briefly for the readability probe.
//   - Reads up to 8 KiB of the surface for the frontmatter probe
//     (only when rule 2 is consulted).
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
	// Readability probe — Slice 4 widens the surface contract to
	// any reachable file, so we explicitly verify the harness can
	// actually read the path before any worktree work begins.
	if f, err := os.Open(opts.surface); err != nil {
		return opts, fmt.Errorf("--surface %q: not readable: %w", opts.surface, err)
	} else {
		_ = f.Close()
	}

	agentDirs := agentDirsFromConfig(application)
	surfaceType, err := detectSurfaceType(opts.surface, agentDirs)
	if err != nil {
		return opts, fmt.Errorf("detecting surface type for %q: %w", opts.surface, err)
	}
	opts.surfaceType = surfaceType

	// Slice 5 — explicit `--evaluator-script` paths are validated up
	// front so the operator gets a clear error before any worktree
	// work begins. Empty path falls through to the MVP default.
	if opts.evaluatorScript != "" {
		if err := validateEvaluatorScriptPath(opts.evaluatorScript); err != nil {
			return opts, err
		}
	}

	// Slice 6 — resolve `--program` to a concrete file path before any
	// worktree work begins. Failure here is operator-facing (typo'd
	// skill name, missing path) and must reject the run before the
	// clean-tree precondition fires so the operator sees one clear
	// error rather than a chain of misleading downstream symptoms.
	repoRoot, repoErr := surfaceRepoRoot(opts.surface)
	if repoErr != nil {
		return opts, fmt.Errorf("resolving repo root for --program: %w", repoErr)
	}
	resolvedProgram, isSkillName, programErr := resolveProgram(opts.program, repoRoot)
	if programErr != nil {
		return opts, programErr
	}
	opts.programResolvedPath = resolvedProgram
	opts.programIsSkillName = isSkillName

	// Live-driver Slice 1 — read the program body once at run start so
	// the synthesiser does not need to re-stat the file each trial.
	// A read failure here is operator-facing in the same shape as the
	// resolution error above; the synthesiser tolerates an empty body
	// (it emits a literal placeholder) but a stat-but-cannot-read path
	// is a misconfiguration we surface up front.
	if body, readErr := os.ReadFile(resolvedProgram); readErr != nil {
		return opts, fmt.Errorf("--program %q: reading body for synthesiser: %w", opts.program, readErr)
	} else {
		opts.programBody = string(body)
	}

	// Live-driver Slice 1 — surface a sane default for
	// --prompt-history-window when the operator omits the flag.
	// resolveAutoresearchOptions is the single entry point all callers
	// share, so defaulting here keeps the synthesiser's contract
	// uniform across the cobra path and the test harness.
	if opts.promptHistoryWindow <= 0 {
		opts.promptHistoryWindow = driverPromptHistoryDefault
	}

	if opts.runID == "" {
		opts.runID = uuid.NewString()
	}

	// Default --worktree-base to a project-local path under the
	// surface's repo root rather than the FlowState DataDir. The repo
	// root is already in hand from the --program resolution above; we
	// reuse it verbatim so a single absent flag yields a discoverable
	// worktree at <surfaceRepoRoot>/.flowstate/autoresearch/<runID>/.
	// An explicitly supplied --worktree-base is honoured verbatim,
	// preserving the operator escape hatch (e.g. running against a
	// read-only repo, or a CI sandbox that mounts the source tree
	// read-only and writes elsewhere). FlowState's own .gitignore
	// excludes .flowstate/; downstream consumers wire the same one-line
	// entry once.
	if opts.worktreeBase == "" {
		opts.worktreeBase = filepath.Join(repoRoot, ".flowstate", "autoresearch")
	}

	return opts, nil
}

// programIsPathForm returns true when the operator-supplied `--program`
// value should be interpreted as a path rather than a registry skill
// name. Per plan § 5.10, anything containing a path separator or
// ending in `.md` is a path; bare identifiers are skill names. This
// rule keeps the surface predictable: skill names live in
// `skills/<name>/SKILL.md` and never carry a slash; ad-hoc programs
// are operator-authored markdown files.
func programIsPathForm(value string) bool {
	if strings.ContainsRune(value, '/') {
		return true
	}
	if strings.EqualFold(filepath.Ext(value), ".md") {
		return true
	}
	return false
}

// resolveProgram converts the operator-supplied `--program` value into
// an absolute path on disk. The two resolution forms follow plan
// § 5.10:
//
//   - Skill name (no '/' and no '.md' suffix): looked up as
//     `<repoRoot>/skills/<name>/SKILL.md`. The skill body is read by
//     the registry; the harness only confirms the file exists and is
//     a regular file — content validation belongs to the engine's
//     skill loader, not to the autoresearch surface gate.
//   - Path (contains '/' or ends in '.md'): resolved as an absolute
//     path when given absolute, otherwise relative to the repo root.
//     The path must point at an existing regular file.
//
// Expected:
//   - value is a non-empty operator-supplied program identifier.
//   - repoRoot is an absolute path to the surface's enclosing git repo,
//     used as the search base for skill-name lookups and relative
//     paths.
//
// Returns:
//   - The absolute resolved path on success.
//   - isSkillName: true when the input was treated as a registry skill
//     name (drives the N12 de-dup detection in applyCallingAgentDeDup).
//   - A descriptive error mentioning `--program` and the failure mode
//     when the path does not exist, is not a regular file, or is not
//     readable.
//
// Side effects:
//   - Stat-checks the resolved path.
func resolveProgram(value, repoRoot string) (resolved string, isSkillName bool, err error) {
	if value == "" {
		return "", false, errors.New("--program: must not be empty (default is `autoresearch`)")
	}
	if programIsPathForm(value) {
		candidate := value
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(repoRoot, candidate)
		}
		info, statErr := os.Stat(candidate)
		if statErr != nil {
			return "", false, fmt.Errorf("--program %q: %w", value, statErr)
		}
		if !info.Mode().IsRegular() {
			return "", false, fmt.Errorf("--program %q: not a regular file", value)
		}
		abs, absErr := filepath.Abs(candidate)
		if absErr != nil {
			return "", false, fmt.Errorf("--program %q: resolving absolute path: %w", value, absErr)
		}
		return abs, false, nil
	}
	// Skill-name form — look up `skills/<name>/SKILL.md` under the
	// repo root. The harness deliberately does NOT search the user-
	// global skill registry (~/.claude/skills/...); registry-named
	// programs must live alongside the surface so kept commits the
	// harness cherry-picks back are reproducible across machines.
	skillPath := filepath.Join(repoRoot, "skills", value, "SKILL.md")
	info, statErr := os.Stat(skillPath)
	if statErr != nil {
		return "", false, fmt.Errorf("--program %q: skill not found at %s: %w", value, skillPath, statErr)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("--program %q: %s is not a regular file", value, skillPath)
	}
	abs, absErr := filepath.Abs(skillPath)
	if absErr != nil {
		return "", false, fmt.Errorf("--program %q: resolving absolute path: %w", value, absErr)
	}
	return abs, true, nil
}

// applyCallingAgentDeDup implements the N12 contract from plan § 5.10.
//
// When --program resolves to a registry skill name AND --calling-agent
// points at a loadable manifest whose `always_active_skills` list
// contains the program name, the harness logs a de-dup decision and
// returns true so the caller can annotate the run's manifest record.
// Path-based programs never trigger de-dup — they are anonymous
// surfaces with no registry-name to match. Missing / unparseable
// calling-agent manifests are ignored silently per the best-effort
// contract: operators driving the harness directly from a shell never
// see spurious errors about manifests they did not supply.
//
// Expected:
//   - opts has been resolved (programResolvedPath, programIsSkillName,
//     program, callingAgentManifest all populated).
//   - w is the destination for the human-readable de-dup log line.
//
// Returns:
//   - true when a de-dup match was logged; false otherwise.
//
// Side effects:
//   - Reads the calling-agent manifest from disk.
//   - Writes a single de-dup log line to w when a match fires.
func applyCallingAgentDeDup(opts autoresearchRunOptions, w io.Writer) bool {
	if !opts.programIsSkillName {
		return false
	}
	if opts.callingAgentManifest == "" {
		return false
	}
	manifest, err := agent.LoadManifest(opts.callingAgentManifest)
	if err != nil || manifest == nil {
		// Best-effort — a missing or unparseable calling-agent manifest
		// is not a hard fail. Operators may invoke the harness from a
		// shell with no agent context at all, in which case there is
		// no manifest to consult.
		return false
	}
	for _, name := range manifest.Capabilities.AlwaysActiveSkills {
		if name == opts.program {
			// Single quotes (rather than %q) are the documented form
			// in plan v3.1 § 5.10. Operators grepping the run output
			// for the de-dup line should match the literal sentence
			// from the plan, not the Go-canonical %q rendering.
			_, _ = fmt.Fprintf(w,
				"autoresearch: program skill '%s' already loaded by calling agent; skipping re-injection\n",
				opts.program,
			)
			return true
		}
	}
	return false
}

// agentDirsFromConfig assembles the union of cfg.AgentDir and
// cfg.AgentDirs into a single slice for the path heuristic. An
// empty primary AgentDir is dropped so the heuristic does not
// silently match every path under the empty string.
func agentDirsFromConfig(application *app.App) []string {
	if application == nil || application.Config == nil {
		return nil
	}
	dirs := make([]string, 0, 1+len(application.Config.AgentDirs))
	if application.Config.AgentDir != "" {
		dirs = append(dirs, application.Config.AgentDir)
	}
	dirs = append(dirs, application.Config.AgentDirs...)
	return dirs
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
	cmd := observedCommand("git", "-C", repoRoot, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("running git status in %s: %w", repoRoot, err)
	}
	if len(strings.TrimSpace(string(output))) > 0 {
		return fmt.Errorf(
			"surface working tree at %s is dirty; commit or stash before running autoresearch (or pass --allow-dirty):\n%s",
			repoRoot, strings.TrimSpace(string(output)))
	}
	return nil
}

// autoresearchStashMessage tags every harness-managed stash so
// `git stash list` makes the provenance obvious to operators.
const autoresearchStashMessage = "flowstate-autoresearch-allow-dirty"

// preflightCleanTree enforces the clean-tree precondition (lifecycle
// plan Slice 3). When allowDirty is false, behaviour mirrors the
// previous direct call to requireCleanTree.
//
// When allowDirty is true and the tree is non-empty, the helper runs
// `git stash push --include-untracked` so the loop runs against an
// effectively-clean parent. The returned stash ref is the SHA of the
// stash commit (`stash@{0}` resolved at create time) and is consumed
// by restoreParentStash on every exit path. An empty return value
// indicates "nothing to restore".
//
// Expected:
//   - w is the writer for operator-visible status lines.
//   - repoRoot is the parent repo root.
//   - allowDirty is the operator's --allow-dirty flag.
//
// Returns:
//   - The stash ref (SHA) when a stash was created; "" when the tree
//     was clean to begin with or allowDirty was false on a clean tree.
//   - An error if the precondition fails (allowDirty=false + dirty)
//     or the stash command itself fails.
//
// Side effects:
//   - Shells `git status --porcelain`.
//   - Shells `git stash push -u -m <tag>` and `git rev-parse stash@{0}`
//     when allowDirty is true and the tree is dirty.
//   - Logs a one-line "stashing/preserved" notice to w.
func preflightCleanTree(w io.Writer, repoRoot string, allowDirty bool) (string, error) {
	statusCmd := observedCommand("git", "-C", repoRoot, "status", "--porcelain")
	statusOut, err := statusCmd.Output()
	if err != nil {
		return "", fmt.Errorf("running git status in %s: %w", repoRoot, err)
	}
	clean := len(strings.TrimSpace(string(statusOut))) == 0

	if clean {
		return "", nil
	}
	if !allowDirty {
		return "", fmt.Errorf(
			"surface working tree at %s is dirty; commit or stash before running autoresearch (or pass --allow-dirty):\n%s",
			repoRoot, strings.TrimSpace(string(statusOut)))
	}

	// Stash everything (tracked + untracked) so the worktree branch
	// branches off a clean HEAD-equivalent state. The stash is
	// restored on every exit path via restoreParentStash.
	stashCmd := observedCommand("git", "-C", repoRoot, "stash", "push",
		"--include-untracked", "-m", autoresearchStashMessage)
	if pushOut, err := stashCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("stashing parent state: %w (output: %s)",
			err, strings.TrimSpace(string(pushOut)))
	}

	// Resolve the stash to a stable SHA — `stash@{0}` shifts each
	// time another stash is pushed, but the SHA is permanent.
	revCmd := observedCommand("git", "-C", repoRoot, "rev-parse", "stash@{0}")
	revOut, err := revCmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolving stash sha: %w", err)
	}
	ref := strings.TrimSpace(string(revOut))
	_, _ = fmt.Fprintf(w,
		"autoresearch preflight: --allow-dirty stashed parent state as %s (%s); restored on exit\n",
		ref, autoresearchStashMessage)
	return ref, nil
}

// restoreParentStash pops the harness-managed stash recorded by
// preflightCleanTree. It is safe to call with an empty stashRef (the
// no-stash case) and emits a non-fatal warning if the apply step
// fails — at which point the operator inherits the stash entry and
// can recover via `git stash list`/`git stash pop`.
//
// The implementation uses `git stash apply <sha>` followed by
// `git stash drop <ref>` rather than `git stash pop` because the
// latter is keyed by the now-shifted `stash@{N}` index.
//
// Expected:
//   - w is the writer for operator-visible status lines.
//   - repoRoot is the parent repo root.
//   - stashRef is the stash SHA returned by preflightCleanTree (or
//     empty when no stash was created).
//
// Side effects:
//   - When stashRef is non-empty: shells `git stash apply <sha>` and,
//     on success, walks `git stash list` to find the matching entry
//     by SHA and drops it.
//   - Logs the outcome to w.
func restoreParentStash(w io.Writer, repoRoot, stashRef string) {
	if stashRef == "" {
		return
	}
	applyCmd := observedCommand("git", "-C", repoRoot, "stash", "apply", stashRef)
	if applyOut, err := applyCmd.CombinedOutput(); err != nil {
		_, _ = fmt.Fprintf(w,
			"autoresearch cleanup: restoring --allow-dirty stash %s failed: %v (output: %s) — recover via `git stash list && git stash pop`\n",
			stashRef, err, strings.TrimSpace(string(applyOut)))
		return
	}

	// Walk `git stash list --format=%H` to find the index that maps
	// to our SHA, then drop it. This is robust to other stashes the
	// operator may have pushed between preflight and restore.
	listCmd := observedCommand("git", "-C", repoRoot, "stash", "list", "--format=%H")
	listOut, err := listCmd.Output()
	if err != nil {
		_, _ = fmt.Fprintf(w,
			"autoresearch cleanup: stash applied but list failed (%v); harness-managed stash entry remains\n", err)
		return
	}
	for i, line := range strings.Split(strings.TrimSpace(string(listOut)), "\n") {
		if strings.TrimSpace(line) != stashRef {
			continue
		}
		dropCmd := observedCommand("git", "-C", repoRoot, "stash", "drop", fmt.Sprintf("stash@{%d}", i))
		if dropOut, dropErr := dropCmd.CombinedOutput(); dropErr != nil {
			_, _ = fmt.Fprintf(w,
				"autoresearch cleanup: stash applied but drop failed: %v (output: %s)\n",
				dropErr, strings.TrimSpace(string(dropOut)))
			return
		}
		_, _ = fmt.Fprintf(w,
			"autoresearch cleanup: --allow-dirty stash %s restored and dropped\n", stashRef)
		return
	}
	_, _ = fmt.Fprintf(w,
		"autoresearch cleanup: stash applied but no list entry matched %s; harness-managed stash entry may remain\n", stashRef)
}

// autoresearchBranchName returns the branch name a run's worktree is
// created on. Per the lifecycle plan (April 2026) § Branch-naming
// convention, every run gets a branch named
// `autoresearch/<run-id-short>` where <run-id-short> is the first 8
// characters of the run ID. UUID4 8-char prefix collision probability
// is ~2.3e-10 per run; below the noise floor for any realistic run
// count. The 8-char prefix matches the convention seen in
// `.claude/worktrees/agent-<8hex>/` worktrees.
//
// Expected:
//   - runID is the resolved run identifier (UUID4 or operator-supplied).
//
// Returns:
//   - The branch name string.
func autoresearchBranchName(runID string) string {
	short := runID
	if len(short) > 8 {
		short = short[:8]
	}
	return "autoresearch/" + short
}

// createTrialWorktree adds a git worktree at worktreePath off the
// surface repo's HEAD on a named branch. The worktree is the harness's
// owned playground for per-trial commits; the operator's tree is never
// touched.
//
// The branch is created as `autoresearch/<run-id-short>` (lifecycle
// plan Slice 1) so kept commits are reachable as a real branch ref
// after the worktree is removed — not as bare detached SHAs reachable
// only via the worktree checkout.
//
// Expected:
//   - repoRoot is the parent repo root.
//   - worktreePath is the desired worktree path; its parent
//     directories are created as needed.
//   - branchName is the branch the worktree is created on; the branch
//     is created off the parent repo's current HEAD.
//
// Returns:
//   - nil on a successful `git worktree add`.
//   - non-nil error if the worktree or branch cannot be created (e.g.
//     branch name collision when re-running with an existing run-id).
//
// Side effects:
//   - Creates parent directories of worktreePath.
//   - Calls `git worktree add -b <branch>` to materialise the worktree
//     and the branch atomically.
func createTrialWorktree(repoRoot, worktreePath, branchName string) error {
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return fmt.Errorf("creating worktree parent: %w", err)
	}
	// Create the branch and worktree atomically off the parent's
	// HEAD. The branch persists after the worktree is removed (Slice
	// 2 auto-prune); kept commits are reachable as branch refs. If
	// the branch already exists (operator re-ran with an existing
	// --run-id), git's error is propagated verbatim — the operator
	// is directed at `flowstate autoresearch list` (Slice 5) by the
	// surrounding command's documentation.
	cmd := observedCommand("git", "-C", repoRoot, "worktree", "add", "-b", branchName, worktreePath, "HEAD")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add -b %s: %w (output: %s)", branchName, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// cleanExitTerminationReasons enumerates the termination reasons that
// trigger end-of-run worktree removal (lifecycle plan Slice 2 § Auto-
// prune contract). Anything outside this set — signal terminations,
// gate/contract failures, process death — preserves the worktree so
// the operator can inspect the in-progress state.
//
// The branch (`autoresearch/<run-id-short>`) is preserved on every
// termination; it is the durable kept-commit anchor.
var cleanExitTerminationReasons = map[string]struct{}{
	"max-trials":            {},
	"time-budget":           {},
	"converged":             {},
	"fixed-point-saturated": {},
}

// cleanupTrialWorktree removes the run's worktree on a clean
// termination unless the operator passed --keep-worktree. The branch
// is never removed by this helper — it is the durable kept-commit
// anchor and operators remove it via `git branch -d` when they no
// longer need it.
//
// 4-state contract (lifecycle plan § Auto-prune contract):
//
//  1. Clean termination (`max-trials`, `time-budget`, `converged`,
//     `fixed-point-saturated`) → remove worktree (and run
//     `git worktree prune` to unwedge git's internal view of the
//     gone worktree).
//  2. `--keep-worktree` set → preserve worktree even on clean exit.
//  3. Signal termination (`signal`) → preserve worktree so the
//     operator can inspect mid-run state.
//  4. Failure / unknown / empty terminationReason → preserve worktree
//     for forensic inspection (gate failures, evaluator-contract
//     failures, process death with no result key).
//
// Cleanup failure is non-fatal: the caller has already written the
// result record and rendered the summary; the run is successful. The
// branch is the durable artefact. Any cleanup error is reported to
// stdout so the operator can manually `flowstate autoresearch prune`
// (Slice 5 in the broader plan) or `git worktree remove --force` the
// residue.
//
// Expected:
//   - w is the writer the run summary was rendered to.
//   - repoRoot is the parent repo root (target of `git worktree`).
//   - worktreePath is the worktree directory to remove.
//   - terminationReason is the loop's terminationReason ("" if the
//     loop never started a trial — treated as preserve).
//   - keepWorktree is the operator's --keep-worktree flag.
//
// Side effects:
//   - On clean exit + !keepWorktree: shells `git worktree remove` and
//     `git worktree prune` against repoRoot.
//   - Logs a one-line status message to w describing the action taken.
func cleanupTrialWorktree(w io.Writer, repoRoot, worktreePath, terminationReason string, keepWorktree bool) {
	if _, clean := cleanExitTerminationReasons[terminationReason]; !clean {
		// Non-clean termination — preserve everything for inspection.
		_, _ = fmt.Fprintf(w,
			"autoresearch cleanup: worktree preserved (termination_reason=%s) at %s\n",
			terminationReason, worktreePath)
		return
	}
	if keepWorktree {
		_, _ = fmt.Fprintf(w,
			"autoresearch cleanup: worktree preserved (--keep-worktree) at %s\n",
			worktreePath)
		return
	}

	// `--force` is required to clear the worktree's untracked scratch
	// files (notably .autoresearch/prompt-* written by the live-driver
	// synthesiser). The branch is the durable artefact; uncommitted
	// scratch state inside the worktree is by design ephemeral.
	removeCmd := observedCommand("git", "-C", repoRoot, "worktree", "remove", "--force", worktreePath)
	if output, err := removeCmd.CombinedOutput(); err != nil {
		_, _ = fmt.Fprintf(w,
			"autoresearch cleanup: worktree removal failed at %s: %v (output: %s) — branch is preserved\n",
			worktreePath, err, strings.TrimSpace(string(output)))
		return
	}
	// `git worktree prune` reaps stale administrative records left
	// behind under .git/worktrees/ when remove succeeds; without it
	// `git worktree list` continues to mention the removed entry.
	pruneCmd := observedCommand("git", "-C", repoRoot, "worktree", "prune")
	if output, err := pruneCmd.CombinedOutput(); err != nil {
		_, _ = fmt.Fprintf(w,
			"autoresearch cleanup: worktree removed but prune failed: %v (output: %s)\n",
			err, strings.TrimSpace(string(output)))
		return
	}
	_, _ = fmt.Fprintf(w,
		"autoresearch cleanup: worktree removed at %s; branch preserved\n",
		worktreePath)
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
// baseline_score, baseline_commit, started_at, worktree_path. Slice 4
// fills surface_type via detectSurfaceType (§ 4.4). evaluator and
// program carry the MVP hard-coded defaults so the record is
// consistent across runs.
//
// EvaluatorScript carries the resolved `--evaluator-script` path when
// the operator supplied one (Slice 5); empty when the MVP default
// fires. The field is `omitempty` so older readers parsing
// pre-Slice-5 records continue to work unchanged.
type manifestRecord struct {
	Surface         string  `json:"surface"`
	SurfaceType     string  `json:"surface_type"`
	Evaluator       string  `json:"evaluator"`
	EvaluatorScript string  `json:"evaluator_script,omitempty"`
	Program         string  `json:"program"`
	ProgramResolved string  `json:"program_resolved"`
	MetricDirection string  `json:"metric_direction"`
	MaxTrials       int     `json:"max_trials"`
	TimeBudget      string  `json:"time_budget"`
	NoImproveWindow int     `json:"no_improve_window"`
	BaselineScore   float64 `json:"baseline_score"`
	BaselineCommit  string  `json:"baseline_commit"`
	StartedAt       string  `json:"started_at"`
	WorktreePath    string  `json:"worktree_path"`
	// Live-driver Slice 1 (plan § 4.4 R1.2): all four new fields are
	// optional on read — predecessor records lack them, and consumers
	// MUST treat absence as zero-value strings/ints. `omitempty` keeps
	// older readers parsing unchanged.
	DriverMode          string `json:"driver_mode,omitempty"`
	DriverScript        string `json:"driver_script,omitempty"`
	DriverTimeoutMS     int64  `json:"driver_timeout_ms,omitempty"`
	PromptHistoryWindow int    `json:"prompt_history_window,omitempty"`
	// AllowDirty is true when the run was started against a dirty
	// parent tree via --allow-dirty. Lifecycle plan Slice 3.
	AllowDirty bool `json:"allow_dirty,omitempty"`
	// CommitTrials is true when the run was started with
	// --commit-trials, restoring the legacy git-mediated substrate.
	// April 2026 In-Memory Default plan Slice 1. The field is keyed
	// on the explicit name rather than `omitempty` so consumers
	// reading older records reliably observe the absence as the
	// default-mode (content) substrate.
	CommitTrials bool `json:"commit_trials"`
}

// writeManifestRecord serialises the run config to the coord-store at
// `autoresearch/<runID>/manifest`. The baseline values are 0/"" when
// the spine is exited via max-trials=0 (no trials, no baseline scoring).
//
// Expected:
//   - store is a non-nil coordination.Store.
//   - opts is the resolved-options struct.
//   - worktreePath is the absolute worktree path.
//   - baselineScore is the integer-as-float64 score of the unmodified
//     surface (0 when baseline scoring did not run).
//   - baselineCommit is the worktree HEAD SHA at run start (empty
//     when baseline scoring did not run).
//
// Returns:
//   - nil on successful Set.
//   - An error wrapping the underlying store failure.
//
// Side effects:
//   - Writes one entry to the coord-store.
func writeManifestRecord(
	store coordination.Store,
	opts autoresearchRunOptions,
	worktreePath string,
	baselineScore float64,
	baselineCommit string,
) error {
	rec := manifestRecord{
		Surface:             opts.surface,
		SurfaceType:         string(opts.surfaceType),
		Evaluator:           defaultEvaluator(opts.evaluatorScript),
		EvaluatorScript:     opts.evaluatorScript,
		Program:             opts.program,
		ProgramResolved:     programResolvedRecord(opts),
		MetricDirection:     opts.metricDirection,
		MaxTrials:           opts.maxTrials,
		TimeBudget:          opts.timeBudget.String(),
		NoImproveWindow:     opts.noImproveWindow,
		BaselineScore:       baselineScore,
		BaselineCommit:      baselineCommit,
		StartedAt:           time.Now().UTC().Format(time.RFC3339),
		WorktreePath:        worktreePath,
		DriverMode:          driverModeForRecord(opts),
		DriverScript:        opts.driverScript,
		DriverTimeoutMS:     opts.driverTimeout.Milliseconds(),
		PromptHistoryWindow: opts.promptHistoryWindow,
		AllowDirty:          opts.allowDirty,
		CommitTrials:        opts.commitTrials,
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

// driverModeForRecord returns the manifest record's `driver_mode`
// label. Live-driver Slice 1 ships only the script mode; Slice 4 of
// the plan adds an in-engine alternative behind a flag. Empty
// driver-script means no driver runs (the harness's max-trials=0
// smoke path or a fixed-point-only fixture run); the field is left
// empty so downstream readers can distinguish "no driver" from
// "script driver".
func driverModeForRecord(opts autoresearchRunOptions) string {
	if opts.driverScript == "" {
		return ""
	}
	return "script"
}

// programResolvedRecord renders the manifest record's `program_resolved`
// field. By default it is the absolute resolved path; when the N12
// de-dup fired, the path is suffixed with " (deduplicated against
// calling agent)" so an auditor can reconcile the logged line with
// the persisted record without cross-referencing stdout.
func programResolvedRecord(opts autoresearchRunOptions) string {
	if opts.programDeduplicated {
		return opts.programResolvedPath + " (deduplicated against calling agent)"
	}
	return opts.programResolvedPath
}

// validateEvaluatorScriptPath enforces the operator-facing half of
// the evaluator contract from plan v3.1 § 4.6: the path the operator
// hands to `--evaluator-script` must exist, must be a regular file,
// and must be executable. Failures here produce a clear error before
// any worktree is created — operators don't have to wait for the
// baseline-scoring step to find out their script was mis-typed.
//
// Expected:
//   - path is a non-empty operator-supplied evaluator path
//     (absolute or relative to the process cwd).
//
// Returns:
//   - nil on a regular, executable file.
//   - A descriptive error mentioning `--evaluator-script` and the
//     specific failure mode (missing, not regular, not executable).
//
// Side effects:
//   - Stat-checks the path.
func validateEvaluatorScriptPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("--evaluator-script %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("--evaluator-script %q: not a regular file", path)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("--evaluator-script %q: not executable (chmod +x)", path)
	}
	return nil
}
