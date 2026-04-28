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
	"os/exec"
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
	// programBody is the program-of-record skill body, read once at
	// run start from programResolvedPath. The synthesiser embeds it
	// verbatim in every per-trial prompt so the off-limits constraints
	// reach the live driver without an extra disk read per trial.
	// Live-driver Slice 1.
	programBody string
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
		"Worktree parent directory; empty → <DataDir>/autoresearch/<runID>/worktree")
	flags.IntVar(&opts.noImproveWindow, "no-improve-window", 5,
		"Consecutive non-improving trials before terminating with reason=converged")
	flags.StringVar(&opts.driverScript, "driver-script", "",
		"Driver script path. The harness invokes this script once per trial inside the worktree; the script is responsible for editing the surface in place. The synthesised prompt-file path is exposed via FLOWSTATE_AUTORESEARCH_PROMPT_FILE. See scripts/autoresearch-drivers/default-assistant-driver.sh for the canonical reference driver.")
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
func runAutoresearch(ctx context.Context, cmd *cobra.Command, application *app.App, opts autoresearchRunOptions) error {
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

	surfaceRepoRoot, err := surfaceRepoRoot(resolved.surface)
	if err != nil {
		return fmt.Errorf("resolving surface repo root: %w", err)
	}

	if err := requireCleanTree(surfaceRepoRoot); err != nil {
		return err
	}

	worktreePath := filepath.Join(resolved.worktreeBase, resolved.runID, "worktree")
	branchName := autoresearchBranchName(resolved.runID)
	if err := createTrialWorktree(surfaceRepoRoot, worktreePath, branchName); err != nil {
		return fmt.Errorf("creating worktree: %w", err)
	}

	store, err := openCoordStore(application)
	if err != nil {
		return err
	}

	// max-trials=0 is the smoke path — set up, write the manifest
	// record (without baseline data), exit cleanly without running
	// trials. Useful for integration tests that exercise the
	// precondition + worktree + record sequence without
	// provider/script dependencies.
	if resolved.maxTrials == 0 {
		if err := writeManifestRecord(store, resolved, worktreePath, 0, ""); err != nil {
			return fmt.Errorf("writing manifest record: %w", err)
		}
		// Setup-only path still surfaces the detected type so an
		// operator running `--max-trials 0` (the smoke probe used
		// by the spec) can confirm the gate would behave as
		// expected before committing to a real trial.
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
		// Baseline contract violation is a hard fail — the
		// evaluator is broken before any trial has run, so
		// reporting `evaluator-contract-failure-rate` later in the
		// loop would be noise. Surface the underlying reason now.
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

	terminationReason, lastOutcome, err := runTrialLoop(ctx, resolved, worktreePath, store, cmd.OutOrStdout(), state)
	if err != nil {
		return err
	}

	printRunSummary(cmd.OutOrStdout(), resolved, state, lastOutcome, terminationReason, worktreePath)
	return nil
}

// worktreeHeadSHA returns the worktree's current HEAD SHA via
// `git -C <worktree> rev-parse HEAD`.
func worktreeHeadSHA(worktreePath string) (string, error) {
	cmd := exec.Command("git", "-C", worktreePath, "rev-parse", "HEAD")
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

	if opts.worktreeBase == "" {
		opts.worktreeBase = filepath.Join(application.Config.DataDir, "autoresearch")
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
	cmd := exec.Command("git", "-C", repoRoot, "worktree", "add", "-b", branchName, worktreePath, "HEAD")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add -b %s: %w (output: %s)", branchName, err, strings.TrimSpace(string(output)))
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
