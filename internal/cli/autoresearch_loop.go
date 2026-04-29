// Trial-loop primitives for `flowstate autoresearch run` (Slice 1c
// of the autoresearch MVP plan v3.1).
//
// Each trial in this MVP is a fixed sequence:
//
//  1. Driver — invoke the configured driver script in the worktree
//     so the calling agent (or a fixture) edits the surface in place.
//  2. Fixed-point gate — SHA-256 the new surface content; if the SHA
//     is already in the seen-candidates ring, record
//     `fixed-point-skipped` and revert any uncommitted edit.
//  3. Manifest gate — when the surface is classified as type
//     "manifest" (per detectSurfaceType / § 4.4), validate it via
//     agent.LoadAndValidateManifest. Failure records
//     `manifest-validate-failed` and reverts the uncommitted edit.
//     Skill and source surfaces skip the gate.
//  4. Commit candidate — git commit -a --no-verify (per § 5.5 N13
//     and [[make check Gate Structurally Broken on Origin (April 2026)]]).
//  5. Evaluator — invoke the evaluator script with the worktree as
//     cwd; parse the integer scalar from stdout.
//  6. Ratchet — keep the commit if the score improves under
//     --metric-direction; otherwise `git reset --hard HEAD~1`.
//  7. Record — write the trial record + update best/seen-candidates
//     in the coord-store.
//
// Termination matrix (plan § 4.7):
//   - max-trials         : counter reaches --max-trials
//   - time-budget        : wall-clock exceeds --time-budget
//   - converged          : --no-improve-window consecutive
//                          non-improving trials (excluding
//                          fixed-point-skipped/manifest-validate-failed
//                          per the spec)
//   - fixed-point-saturated      : K=10 consecutive fixed-point hits
//   - manifest-gate-failure-rate : 3 consecutive manifest-validate
//                                  failures (driver is producing
//                                  nonsense)
//
// The harness owns the worktree's git history end-to-end — every
// per-trial commit uses --no-verify; operators never see the
// per-trial commits except via the kept-commit cherry-pick that
// Slice 1d wires up at run end.

package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
)

// trialOutcome captures the per-trial decision. The reason string is
// the canonical taxonomy from plan § 4.2. SurfaceType is stamped on
// every record (Slice 4) so an operator can audit which gate fired
// without having to cross-reference the manifest record.
//
// EvaluatorTimeoutMS is set only when the evaluator wall-clock
// exceeded `--evaluator-timeout` for this trial; the field is
// `omitempty` so older readers parsing pre-Slice-5 records continue
// to work unchanged.
//
// Autoresearch content mode (April 2026): default content mode
// adds CandidateContent + CandidateContentSHA so the trial record
// carries the candidate string directly (the substrate, no longer git
// commits). Both fields are `omitempty` so git-mode (`--commit-trials`)
// records remain byte-shape identical to pre-pivot records.
type trialOutcome struct {
	N                  int     `json:"n"`
	CommitSHA          string  `json:"commit_sha"`
	CandidateSHA       string  `json:"candidate_sha"`
	Score              float64 `json:"score"`
	Kept               bool    `json:"kept"`
	Reason             string  `json:"reason"`
	SurfaceType        string  `json:"surface_type"`
	DurationS          float64 `json:"duration_s"`
	StartedAt          string  `json:"started_at"`
	EndedAt            string  `json:"ended_at"`
	EvaluatorTimeoutMS int64   `json:"evaluator_timeout_ms,omitempty"`
	// Live-driver Slice 1 (plan § 4.4 R1.2): all four fields are
	// optional on read — predecessor trial records lack them and
	// consumers must treat absence as zero-value. `omitempty` keeps
	// older readers parsing unchanged.
	PromptFile      string `json:"prompt_file,omitempty"`
	PromptSHA       string `json:"prompt_sha,omitempty"`
	DriverSessionID string `json:"driver_session_id,omitempty"`
	DriverTurns     int    `json:"driver_turns,omitempty"`
	// Content default substrate (Autoresearch In-Memory Default,
	// April 2026). CandidateContent is the full candidate string
	// produced by the driver (capped at maxCandidateBytes; truncation
	// flagged via CandidateContentTruncated). CandidateContentSHA is
	// sha256(full content) regardless of truncation, so the candidate
	// remains uniquely identified even when the body is too large to
	// persist verbatim.
	CandidateContent          string `json:"candidate_content,omitempty"`
	CandidateContentTruncated bool   `json:"candidate_content_truncated,omitempty"`
	CandidateContentSHA       string `json:"candidate_content_sha,omitempty"`
}

// trialReason values — pinned per plan § 4.2.
const (
	reasonImproved              = "improved"
	reasonNoImproveWindow       = "no-improve-window"
	reasonRegression            = "regression"
	reasonManifestValidateFail  = "manifest-validate-failed"
	reasonFixedPointSkipped     = "fixed-point-skipped"
	reasonRateLimitDeferred     = "rate-limit-deferred"
	reasonValidatorIOError      = "validator-io-error"
	reasonEvaluatorContractFail = "evaluator-contract-violation"

	// Termination reasons (plan § 4.7).
	terminationConverged                = "converged"
	terminationMaxTrials                = "max-trials"
	terminationTimeBudget               = "time-budget"
	terminationFixedPointSaturated      = "fixed-point-saturated"
	terminationManifestGateFailureRate  = "manifest-gate-failure-rate"
	terminationEvaluatorContractFailure = "evaluator-contract-failure-rate"
	terminationSignal                   = "signal"

	// Threshold constants from plan § 4.7.
	fixedPointSaturationLimit     = 10
	manifestGateFailureLimit      = 3
	evaluatorContractFailureLimit = 3
	seenCandidatesRingCapacity    = 20

	// evaluatorTermGracePeriod is the wall-clock granted between
	// SIGTERM and SIGKILL when --evaluator-timeout fires (plan § 4.6).
	evaluatorTermGracePeriod = 30 * time.Second

	// trialStdoutCaptureLimit caps the bytes captured from the
	// evaluator's stdout to avoid pathological evaluators bloating
	// memory. The contract is one integer line; megabytes of stdout
	// would itself be a violation.
	trialStdoutCaptureLimit = 4096
)

// trialLoopState carries the mutable counters used by termination
// rules across trials. Kept separate from autoresearchRunOptions so
// the option type stays a pure config bag.
type trialLoopState struct {
	consecutiveFixedPoint     int
	consecutiveManifestFails  int
	consecutiveEvaluatorFails int
	consecutiveNoImprove      int
	bestScore                 float64
	bestScoreSet              bool
	bestCommitSHA             string
	bestContentSHA            string
	bestTrialN                int
	seenCandidates            []seenCandidate
	keptCount                 int
	revertedCount             int
	// recentOutcomes carries the last-N trial outcomes the synthesiser
	// renders into the # HISTORY section of the per-trial driver
	// prompt. Live-driver Slice 1. Capped softly by the loop using
	// promptHistoryWindow; bounded by the number of trials actually
	// run so the slice never exceeds maxTrials.
	recentOutcomes []trialOutcome
	// Content default substrate (April 2026). The current surface
	// bytes (read once at run start in default mode) are the immutable
	// task the driver trains against. Empty in --commit-trials mode
	// where the substrate is the worktree's surface file.
	surfaceBytes []byte
}

// commandRunner is the factory the harness uses to construct
// `*exec.Cmd` instances. Tests swap it via SetCommandRunnerForTest to
// observe every subprocess the harness builds — the
// "no git in default mode" assertion (April 2026 In-Memory Default
// plan, R1.2 spec) depends on this seam.
//
// Default behaviour mirrors `exec.CommandContext`. The hook fires
// before the *exec.Cmd is returned so test observers can inspect
// (name, args) without affecting subprocess execution.
type commandRunner func(name string, args ...string)

var (
	currentCommandRunner   commandRunner
	defaultMaxCandidateCap = 256 * 1024
)

// SetCommandRunnerForTest installs an observer the harness calls
// every time it builds an `*exec.Cmd`. The observer receives (binary
// name, args) and MUST NOT mutate them. Used by the content mode spec
// to assert the harness never spawns `git` in default mode.
//
// Reset via ResetCommandRunnerForTest in a DeferCleanup so adjacent
// tests are not affected.
func SetCommandRunnerForTest(observer commandRunner) {
	currentCommandRunner = observer
}

// ResetCommandRunnerForTest clears any observer installed by
// SetCommandRunnerForTest.
func ResetCommandRunnerForTest() {
	currentCommandRunner = nil
}

// observeCommand notifies the test observer (if installed) that a
// subprocess command is being built. Always safe to call — a nil
// observer is a no-op.
func observeCommand(name string, args ...string) {
	if currentCommandRunner != nil {
		currentCommandRunner(name, args...)
	}
}

// observedCommand is the harness-internal wrapper around exec.Command
// that fires the test observer on every construction. Used by every
// subprocess invocation under the autoresearch command tree so the
// "no git in default mode" assertion (April 2026 In-Memory Default
// plan, R1.2) covers every code path.
func observedCommand(name string, args ...string) *exec.Cmd {
	observeCommand(name, args...)
	return exec.Command(name, args...)
}

// observedCommandContext is the context-bound twin of observedCommand.
// Used by subprocess invocations that honour ctx cancellation
// (driver, evaluator).
func observedCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	observeCommand(name, args...)
	return exec.CommandContext(ctx, name, args...)
}

// seenCandidate is one entry in the SHA ring. Stored as a slice and
// truncated to capacity at append time.
type seenCandidate struct {
	CandidateSHA string  `json:"candidate_sha"`
	TrialN       int     `json:"trial_n"`
	Score        float64 `json:"score"`
}

// resultRecord is the shape persisted at autoresearch/<runID>/result.
type resultRecord struct {
	Converged         bool    `json:"converged"`
	TotalTrials       int     `json:"total_trials"`
	FinalScore        float64 `json:"final_score"`
	FinalCommit       string  `json:"final_commit"`
	EndedAt           string  `json:"ended_at"`
	TerminationReason string  `json:"termination_reason"`
}

// bestRecord is the shape persisted at autoresearch/<runID>/best.
//
// Content default (Autoresearch In-Memory Default, April 2026):
// CandidateContentSHA identifies the best candidate by content rather
// than by commit. In `--commit-trials` mode CommitSHA is populated
// alongside (today's behaviour preserved verbatim); in default mode
// CommitSHA stays empty and CandidateContentSHA is the load-bearing
// pointer used by `apply` (Slice 2 of the pivot).
type bestRecord struct {
	CommitSHA           string  `json:"commit_sha"`
	CandidateContentSHA string  `json:"candidate_content_sha,omitempty"`
	Score               float64 `json:"score"`
	TrialN              int     `json:"trial_n"`
}

// runTrialLoop executes the per-trial loop until a termination
// condition fires, then writes the result record.
//
// Expected:
//   - resolved is the validated options struct.
//   - worktreePath is the absolute worktree directory.
//   - store is the open coord-store handle.
//   - out is the writer for human-readable progress lines.
//
// Returns:
//   - The termination reason and final outcome on success.
//   - An error if a non-recoverable harness-side failure occurs
//     (driver/git plumbing failure, coord-store write failure).
//
// Side effects:
//   - Writes per-trial records, best pointer, seen-candidates ring,
//     and the final result record to the coord-store.
//   - Mutates the worktree's git history (kept commits stay; reverts
//     restore HEAD~1 inside the worktree only).
//
// runTrialLoop is invoked by runAutoresearch with a pre-seeded state
// (baseline score + seen-candidates ring + best). The caller owns
// baseline scoring and manifest-record persistence; the loop owns
// per-trial driving, ratchet, and result-record finalisation.
//
// Returns the termination reason, the last outcome (for the result
// record + summary), and any harness-side error.
func runTrialLoop(
	ctx context.Context,
	resolved autoresearchRunOptions,
	worktreePath string,
	store coordination.Store,
	out io.Writer,
	state *trialLoopState,
	relSurface string,
) (string, trialOutcome, error) {
	deadline := time.Now().Add(resolved.timeBudget)

	worktreeSurface := ""
	if resolved.commitTrials {
		// In git-mode the worktree-anchored path is the substrate the
		// driver writes to; the content mode branch never touches disk.
		worktreeSurface = filepath.Join(worktreePath, relSurface)
	}

	terminationReason := ""

	var lastOutcome trialOutcome
	for n := 1; n <= resolved.maxTrials; n++ {
		// Context cancellation (SIGTERM/SIGINT) takes precedence over
		// time-budget per § 4.7 termination matrix. The loop writes a
		// best-effort result record before returning so partial state
		// is recoverable.
		select {
		case <-ctx.Done():
			terminationReason = terminationSignal
		default:
		}
		if terminationReason != "" {
			break
		}
		if time.Now().After(deadline) {
			terminationReason = terminationTimeBudget
			break
		}

		var (
			outcome trialOutcome
			err     error
		)
		if resolved.commitTrials {
			outcome, err = runOneTrial(n, resolved, worktreePath, worktreeSurface, relSurface, state)
		} else {
			outcome, err = runOneTrialContent(ctx, n, resolved, relSurface, state)
		}
		if err != nil {
			return "", lastOutcome, fmt.Errorf("trial %d: %w", n, err)
		}
		lastOutcome = outcome

		// Live-driver Slice 1 — recentOutcomes feeds the next trial's
		// synthesised prompt # HISTORY section; appending here keeps
		// the slice consistent regardless of which runOneTrial path
		// produced the outcome.
		state.recentOutcomes = appendRecentOutcomes(state.recentOutcomes, outcome, resolved.promptHistoryWindow)

		if outcome.Kept {
			state.keptCount++
		} else {
			state.revertedCount++
		}

		if err := writeTrialRecord(store, resolved.runID, outcome); err != nil {
			return "", lastOutcome, fmt.Errorf("writing trial-%d record: %w", n, err)
		}
		if err := writeSeenCandidates(store, resolved.runID, state.seenCandidates); err != nil {
			return "", lastOutcome, fmt.Errorf("writing seen-candidates: %w", err)
		}
		if outcome.Kept {
			if err := writeBestRecord(store, resolved.runID, state); err != nil {
				return "", lastOutcome, fmt.Errorf("writing best record: %w", err)
			}
		}

		_, _ = fmt.Fprintf(out,
			"trial %d: kept=%v reason=%s score=%g\n",
			outcome.N, outcome.Kept, outcome.Reason, outcome.Score)

		// Termination checks (post-trial). The
		// manifest-gate-failure-rate exit is git-mode-only per the
		// April 2026 In-Memory Default plan — content mode still
		// fires the per-trial manifest gate (records
		// `manifest-validate-failed` reasons) but does not abort the
		// run on a streak of them.
		if state.consecutiveFixedPoint >= fixedPointSaturationLimit {
			terminationReason = terminationFixedPointSaturated
			break
		}
		if resolved.commitTrials && state.consecutiveManifestFails >= manifestGateFailureLimit {
			terminationReason = terminationManifestGateFailureRate
			break
		}
		if state.consecutiveEvaluatorFails >= evaluatorContractFailureLimit {
			terminationReason = terminationEvaluatorContractFailure
			break
		}
		if state.consecutiveNoImprove >= resolved.noImproveWindow {
			terminationReason = terminationConverged
			break
		}
	}

	if terminationReason == "" {
		terminationReason = terminationMaxTrials
	}

	if err := writeResultRecord(store, resolved.runID, terminationReason, state, lastOutcome); err != nil {
		return terminationReason, lastOutcome, fmt.Errorf("writing result record: %w", err)
	}

	return terminationReason, lastOutcome, nil
}

// runOneTrial drives a single trial: driver edit, fixed-point gate,
// manifest gate, commit, score, ratchet. The state's seen-candidates
// ring and termination counters are mutated by reference.
//
// Expected:
//   - n is the 1-based trial counter.
//   - resolved is the validated options.
//   - worktreePath is the worktree root.
//   - worktreeSurface is the absolute path to the surface inside the
//     worktree (worktreePath / relSurface).
//   - relSurface is the surface path relative to the worktree.
//   - state is the cross-trial counter struct (mutated).
//
// Returns:
//   - The trial outcome (kept/reason/score/sha).
//   - An error if a harness-plumbing primitive fails.
//
// Side effects:
//   - Invokes the driver and evaluator scripts.
//   - Mutates the worktree's git history.
//   - Updates state.seenCandidates and termination counters.
func runOneTrial(
	n int,
	resolved autoresearchRunOptions,
	worktreePath, worktreeSurface, relSurface string,
	state *trialLoopState,
) (trialOutcome, error) {
	startedAt := time.Now()
	outcome := trialOutcome{
		N:           n,
		StartedAt:   startedAt.UTC().Format(time.RFC3339),
		SurfaceType: string(resolved.surfaceType),
	}

	// Live-driver Slice 1 — synthesise the per-trial prompt, write it
	// under the worktree's `.autoresearch/` scratch directory, and
	// expose the path to the driver subprocess via env. The synthesiser
	// is deterministic so two consecutive trials with identical inputs
	// produce byte-identical prompts (operators see this via the
	// recorded `prompt_sha`).
	promptFilePath := ""
	promptSHA := ""
	if resolved.driverScript != "" {
		surfaceBytes, err := os.ReadFile(worktreeSurface)
		if err != nil {
			return outcome, fmt.Errorf("reading surface for prompt synthesis: %w", err)
		}
		promptBytes, bErr := BuildDriverPrompt(
			resolved.programBody,
			relSurface,
			surfaceBytes,
			state.recentOutcomes,
			resolved.promptHistoryWindow,
		)
		if bErr != nil {
			return outcome, fmt.Errorf("building driver prompt: %w", bErr)
		}
		promptDir := filepath.Join(worktreePath, ".autoresearch")
		if mkErr := os.MkdirAll(promptDir, 0o755); mkErr != nil {
			return outcome, fmt.Errorf("creating prompt dir: %w", mkErr)
		}
		promptFilePath = filepath.Join(promptDir, fmt.Sprintf("trial-%d-prompt.txt", n))
		if wErr := os.WriteFile(promptFilePath, promptBytes, 0o600); wErr != nil {
			return outcome, fmt.Errorf("writing prompt file: %w", wErr)
		}
		promptSHA = driverPromptSHA(promptBytes)
		outcome.PromptFile = promptFilePath
		outcome.PromptSHA = promptSHA
	}

	timedOut, dErr := runDriverScript(driverInvocation{
		driverPath:     resolved.driverScript,
		worktreePath:   worktreePath,
		relSurface:     relSurface,
		runID:          resolved.runID,
		trialN:         n,
		promptFilePath: promptFilePath,
		timeout:        resolved.driverTimeout,
		maxTurns:       resolved.driverMaxTurns,
	})
	if dErr != nil {
		// Live-driver Slice 1 — driver failures (non-zero exit or
		// timeout) collapse onto `validator-io-error` per plan § 4.5.
		// The trial is recorded but does NOT count toward
		// no-improve-window; the loop carries on so a transient
		// provider blip does not prematurely converge the run.
		if rErr := gitCheckoutSurface(worktreePath, relSurface); rErr != nil {
			return outcome, fmt.Errorf("reverting after driver failure: %w (driver error: %v)", rErr, dErr)
		}
		outcome.Kept = false
		outcome.Reason = reasonValidatorIOError
		if timedOut {
			outcome.EvaluatorTimeoutMS = resolved.driverTimeout.Milliseconds()
		}
		state.consecutiveFixedPoint = 0
		state.consecutiveManifestFails = 0
		// Do not increment evaluator/no-improve counters — driver I/O
		// errors are explicitly outside both per § 4.5.
		// We have no candidate SHA at this point; record the outcome
		// without one so the trial trajectory is auditable.
		finishOutcome(&outcome, startedAt)
		_ = dErr // surfaced via reason; harness continues to next trial
		return outcome, nil
	}

	candidateSHA, err := surfaceSHA(worktreeSurface)
	if err != nil {
		return outcome, fmt.Errorf("hashing candidate surface: %w", err)
	}
	outcome.CandidateSHA = candidateSHA

	if isFixedPoint(state.seenCandidates, candidateSHA) {
		// Restore the worktree surface — driver may have written a
		// no-op, but be defensive in case a duplicate was reached
		// after a substantive edit.
		if err := gitCheckoutSurface(worktreePath, relSurface); err != nil {
			return outcome, fmt.Errorf("reverting fixed-point candidate: %w", err)
		}
		outcome.Kept = false
		outcome.Reason = reasonFixedPointSkipped
		state.consecutiveFixedPoint++
		state.consecutiveManifestFails = 0
		// fixed-point-skipped does NOT count toward
		// no-improve-window per § 4.7.
		state.seenCandidates = appendSeenRing(state.seenCandidates, seenCandidate{
			CandidateSHA: candidateSHA, TrialN: n, Score: 0,
		})
		finishOutcome(&outcome, startedAt)
		return outcome, nil
	}

	// Slice 4 — manifest gate fires only when the detected
	// surface type is "manifest" (path heuristic OR frontmatter
	// probe per § 4.4). Skill and source surfaces skip the gate
	// and proceed straight to scoring.
	if resolved.surfaceType == SurfaceTypeManifest {
		if _, vErr := agent.LoadAndValidateManifest(worktreeSurface); vErr != nil {
			if err := gitCheckoutSurface(worktreePath, relSurface); err != nil {
				return outcome, fmt.Errorf("reverting manifest-gate failure: %w", err)
			}
			outcome.Kept = false
			outcome.Reason = reasonManifestValidateFail
			state.consecutiveManifestFails++
			state.consecutiveFixedPoint = 0
			// manifest-validate-failed does NOT count toward
			// no-improve-window per § 4.7.
			state.seenCandidates = appendSeenRing(state.seenCandidates, seenCandidate{
				CandidateSHA: candidateSHA, TrialN: n, Score: 0,
			})
			finishOutcome(&outcome, startedAt)
			return outcome, nil
		}
	}

	// Commit the candidate inside the worktree before scoring so a
	// score-then-revert is a clean reset --hard HEAD~1. Per § 5.5
	// N13, --no-verify is mandatory here.
	commitSHA, err := gitCommitTrial(worktreePath, n)
	if err != nil {
		return outcome, fmt.Errorf("committing candidate: %w", err)
	}
	outcome.CommitSHA = commitSHA

	evalRes, err := runEvaluatorScript(resolved.evaluatorScript, worktreePath, relSurface, resolved.runID, resolved.evaluatorTimeout)
	if err != nil {
		return outcome, fmt.Errorf("evaluator harness failure: %w", err)
	}
	if evalRes.ContractViolation {
		// Slice 5 — formal contract enforcement (plan § 4.6). Any
		// deviation is recorded as evaluator-contract-violation;
		// three consecutive violations trip the
		// evaluator-contract-failure-rate hard stop in the loop.
		if rErr := gitResetHard(worktreePath); rErr != nil {
			return outcome, fmt.Errorf("evaluator failure recovery: %w", rErr)
		}
		outcome.Kept = false
		outcome.Reason = reasonEvaluatorContractFail
		outcome.Score = 0
		// The candidate commit was rolled back; do not advertise
		// the SHA on the trial record.
		outcome.CommitSHA = ""
		if evalRes.TimedOut {
			outcome.EvaluatorTimeoutMS = evalRes.TimeoutMS
		}
		state.consecutiveFixedPoint = 0
		state.consecutiveManifestFails = 0
		state.consecutiveEvaluatorFails++
		// evaluator-contract-violation does NOT count toward
		// no-improve-window per § 4.7.
		state.seenCandidates = appendSeenRing(state.seenCandidates, seenCandidate{
			CandidateSHA: candidateSHA, TrialN: n, Score: 0,
		})
		finishOutcome(&outcome, startedAt)
		return outcome, nil
	}
	outcome.Score = evalRes.Score
	score := evalRes.Score

	improved := isImprovement(state, score, resolved.metricDirection)
	if improved {
		outcome.Kept = true
		outcome.Reason = reasonImproved
		state.bestScore = score
		state.bestScoreSet = true
		state.bestCommitSHA = commitSHA
		state.bestTrialN = n
		state.consecutiveNoImprove = 0
	} else {
		// Revert the candidate commit. Per the plan,
		// regression and no-improve are recorded as
		// `regression` for now (Slice 1c keeps the taxonomy
		// simple — Slice 5 distinguishes regression vs
		// no-improve-window in the trial reason).
		if err := gitResetHard(worktreePath); err != nil {
			return outcome, fmt.Errorf("reverting regression: %w", err)
		}
		outcome.Kept = false
		outcome.Reason = reasonRegression
		outcome.CommitSHA = ""
		state.consecutiveNoImprove++
	}

	state.consecutiveFixedPoint = 0
	state.consecutiveManifestFails = 0
	state.consecutiveEvaluatorFails = 0
	state.seenCandidates = appendSeenRing(state.seenCandidates, seenCandidate{
		CandidateSHA: candidateSHA, TrialN: n, Score: score,
	})
	finishOutcome(&outcome, startedAt)
	return outcome, nil
}

// finishOutcome closes out the timing fields on a trial outcome.
func finishOutcome(o *trialOutcome, startedAt time.Time) {
	end := time.Now()
	o.EndedAt = end.UTC().Format(time.RFC3339)
	o.DurationS = end.Sub(startedAt).Seconds()
}

// runOneTrialContent drives a single trial under the content substrate
// (April 2026 In-Memory Default plan):
//
//  1. Synthesise the per-trial prompt against state.surfaceBytes.
//  2. Pipe the prompt to the driver via stdin; capture the candidate
//     string from stdout.
//  3. Fixed-point gate by sha256(candidate_content); skip and revert
//     no on-disk state (there is none to revert).
//  4. Manifest gate by writing the candidate to a tempfile and
//     calling agent.LoadAndValidateManifest. Per-trial gate fires;
//     run-level manifest-gate-failure-rate termination is git-mode-only.
//  5. Pipe the candidate to the evaluator via stdin AND a tempfile
//     exposed as FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE.
//  6. Ratchet on the score; persist {candidate_content,
//     candidate_content_sha, score, kept, reason} on the outcome.
//
// The function never spawns `git` and never writes to the surface.
// runEvaluatorContent and runDriverContent enforce that contract at
// the seam.
//
// Expected:
//   - ctx cancellation propagates to the driver and evaluator
//     subprocesses.
//   - n is the 1-based trial counter.
//   - resolved is the validated options.
//   - relSurface is the surface path relative to the surface's repo
//     root — passed to the synthesiser verbatim.
//   - state.surfaceBytes carries the immutable surface content read
//     once at run start.
func runOneTrialContent(
	ctx context.Context,
	n int,
	resolved autoresearchRunOptions,
	relSurface string,
	state *trialLoopState,
) (trialOutcome, error) {
	startedAt := time.Now()
	outcome := trialOutcome{
		N:           n,
		StartedAt:   startedAt.UTC().Format(time.RFC3339),
		SurfaceType: string(resolved.surfaceType),
	}

	// Synthesise the per-trial prompt. Determinism contract preserved
	// — same inputs produce byte-identical prompts. The prompt SHA is
	// recorded on the outcome so the operator can detect stuck-prompt
	// patterns post-hoc (LD1 in plan § 6.2).
	promptBytes, bErr := BuildDriverPrompt(
		resolved.programBody,
		relSurface,
		state.surfaceBytes,
		state.recentOutcomes,
		resolved.promptHistoryWindow,
	)
	if bErr != nil {
		return outcome, fmt.Errorf("building driver prompt: %w", bErr)
	}
	outcome.PromptSHA = driverPromptSHA(promptBytes)

	candidateBytes, timedOut, dErr := runDriverContent(ctx, driverInvocation{
		driverPath: resolved.driverScript,
		runID:      resolved.runID,
		trialN:     n,
		relSurface: relSurface,
		timeout:    resolved.driverTimeout,
		maxTurns:   resolved.driverMaxTurns,
	}, promptBytes)
	if dErr != nil {
		// Driver failure (non-zero exit, timeout) collapses onto
		// validator-io-error per plan § 4.5; the trial is recorded
		// but does NOT count toward no-improve-window.
		outcome.Kept = false
		outcome.Reason = reasonValidatorIOError
		if timedOut {
			outcome.EvaluatorTimeoutMS = resolved.driverTimeout.Milliseconds()
		}
		state.consecutiveFixedPoint = 0
		state.consecutiveManifestFails = 0
		_ = dErr
		finishOutcome(&outcome, startedAt)
		return outcome, nil
	}

	candidateSHA := contentSHA(candidateBytes)
	outcome.CandidateSHA = candidateSHA
	outcome.CandidateContentSHA = candidateSHA
	stored, truncated := truncateCandidate(candidateBytes, resolved.maxCandidateBytes)
	outcome.CandidateContent = string(stored)
	outcome.CandidateContentTruncated = truncated

	if isFixedPoint(state.seenCandidates, candidateSHA) {
		outcome.Kept = false
		outcome.Reason = reasonFixedPointSkipped
		state.consecutiveFixedPoint++
		state.consecutiveManifestFails = 0
		state.seenCandidates = appendSeenRing(state.seenCandidates, seenCandidate{
			CandidateSHA: candidateSHA, TrialN: n, Score: 0,
		})
		finishOutcome(&outcome, startedAt)
		return outcome, nil
	}

	// Manifest gate (per-trial). Write the candidate to a tempfile
	// purely for the validator's path-based API; no surface mutation.
	if resolved.surfaceType == SurfaceTypeManifest {
		if err := validateManifestCandidateContent(candidateBytes, relSurface); err != nil {
			outcome.Kept = false
			outcome.Reason = reasonManifestValidateFail
			state.consecutiveManifestFails++
			state.consecutiveFixedPoint = 0
			state.seenCandidates = appendSeenRing(state.seenCandidates, seenCandidate{
				CandidateSHA: candidateSHA, TrialN: n, Score: 0,
			})
			finishOutcome(&outcome, startedAt)
			return outcome, nil
		}
	}

	evalRes, err := runEvaluatorContent(ctx, resolved.evaluatorScript, resolved.runID, candidateBytes, resolved.evaluatorTimeout)
	if err != nil {
		return outcome, fmt.Errorf("evaluator harness failure: %w", err)
	}
	if evalRes.ContractViolation {
		outcome.Kept = false
		outcome.Reason = reasonEvaluatorContractFail
		outcome.Score = 0
		if evalRes.TimedOut {
			outcome.EvaluatorTimeoutMS = evalRes.TimeoutMS
		}
		state.consecutiveFixedPoint = 0
		state.consecutiveManifestFails = 0
		state.consecutiveEvaluatorFails++
		state.seenCandidates = appendSeenRing(state.seenCandidates, seenCandidate{
			CandidateSHA: candidateSHA, TrialN: n, Score: 0,
		})
		finishOutcome(&outcome, startedAt)
		return outcome, nil
	}
	outcome.Score = evalRes.Score
	score := evalRes.Score

	improved := isImprovement(state, score, resolved.metricDirection)
	if improved {
		outcome.Kept = true
		outcome.Reason = reasonImproved
		state.bestScore = score
		state.bestScoreSet = true
		state.bestContentSHA = candidateSHA
		state.bestCommitSHA = "" // content mode never sets a commit
		state.bestTrialN = n
		state.consecutiveNoImprove = 0
	} else {
		outcome.Kept = false
		outcome.Reason = reasonRegression
		state.consecutiveNoImprove++
	}

	state.consecutiveFixedPoint = 0
	state.consecutiveManifestFails = 0
	state.consecutiveEvaluatorFails = 0
	state.seenCandidates = appendSeenRing(state.seenCandidates, seenCandidate{
		CandidateSHA: candidateSHA, TrialN: n, Score: score,
	})
	finishOutcome(&outcome, startedAt)
	return outcome, nil
}

// contentSHA returns the lowercase hex SHA-256 of the supplied bytes.
// Shared by the content baseline seeding and the per-trial candidate
// hashing — the SHA is the canonical content identifier under the
// April 2026 substrate swap.
func contentSHA(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// truncateCandidate enforces the harness-side `--max-candidate-bytes`
// cap. Returns (stored, truncated) where stored is at most
// maxBytes long and truncated is true when the input exceeded the
// cap. A non-positive maxBytes falls back to defaultMaxCandidateCap.
func truncateCandidate(content []byte, maxBytes int) ([]byte, bool) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxCandidateCap
	}
	if len(content) <= maxBytes {
		return content, false
	}
	return content[:maxBytes], true
}

// validateManifestCandidateContent writes the candidate bytes to a
// tempfile and runs the agent.LoadAndValidateManifest gate against
// it. The tempfile is removed on every exit path; the surface file
// on disk is never touched.
//
// relSurface is used to derive a temp filename suffix that mirrors
// the surface basename, so any error message the validator emits
// references the operator's mental model rather than an opaque
// generated path.
func validateManifestCandidateContent(candidate []byte, relSurface string) error {
	base := filepath.Base(relSurface)
	if base == "" {
		base = "candidate.md"
	}
	tmp, err := os.CreateTemp("", "autoresearch-candidate-*-"+base)
	if err != nil {
		return fmt.Errorf("staging candidate tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, wErr := tmp.Write(candidate); wErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing candidate tempfile: %w", wErr)
	}
	if cErr := tmp.Close(); cErr != nil {
		return fmt.Errorf("closing candidate tempfile: %w", cErr)
	}
	if _, vErr := agent.LoadAndValidateManifest(tmpPath); vErr != nil {
		return vErr
	}
	return nil
}

// runDriverContent invokes the driver subprocess with the prompt
// bytes piped to stdin and captures the candidate from stdout. A
// parallel FLOWSTATE_AUTORESEARCH_PROMPT_FILE tempfile is populated
// for drivers that prefer a path-based input channel; both shapes are
// always available so script authors choose by convenience. The
// surface env var is exposed RELATIVE to the operator's invocation
// directory and is documented as read-only — the harness no longer
// owns a worktree to enforce immutability, so this is a contract
// rather than an enforcement.
//
// Expected:
//   - inv.driverPath is the executable. Empty falls back to a no-op
//     driver (returns the surface bytes unchanged) so fixture-driven
//     tests can exercise the content loop without authoring a
//     fixture script. The surface bytes are not available here so
//     the empty-driverPath path returns an empty candidate; callers
//     in production always supply a driver.
//   - prompt is the synthesised per-trial prompt; piped to stdin.
//
// Returns:
//   - candidate bytes captured from stdout.
//   - timedOut flag when the driver wall-clock cap fired.
//   - non-nil error on non-zero exit, timeout, or harness-side I/O
//     failure.
func runDriverContent(ctx context.Context, inv driverInvocation, prompt []byte) (candidate []byte, timedOut bool, err error) {
	if inv.driverPath == "" {
		// Empty driver path is treated as a configuration error in
		// the content substrate: the caller cannot infer a "no
		// edit" candidate without a substrate file to copy from.
		// Surface a clean error so the operator gets the failure
		// mode pinned at the seam rather than a downstream
		// hash-empty surprise.
		return nil, false, errors.New("driver script path is empty (content mode requires a driver)")
	}
	driverCtx := ctx
	var cancel context.CancelFunc
	if inv.timeout > 0 {
		driverCtx, cancel = context.WithTimeout(ctx, inv.timeout)
		defer cancel()
	}
	cmd := observedCommandContext(driverCtx, inv.driverPath)
	// Operator's invocation cwd — leave Dir unset so the subprocess
	// inherits whichever directory `flowstate autoresearch run` was
	// invoked from. The substrate swap deliberately removes the
	// implicit "cwd is the worktree" contract.
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = evaluatorTermGracePeriod

	// Mirror channel: write the prompt to a tempfile and expose its
	// path via FLOWSTATE_AUTORESEARCH_PROMPT_FILE. Drivers may use
	// stdin or the file; the harness populates both.
	promptFile, perr := os.CreateTemp("", fmt.Sprintf("autoresearch-prompt-%s-trial-%d-*.txt", inv.runID, inv.trialN))
	if perr != nil {
		return nil, false, fmt.Errorf("staging prompt tempfile: %w", perr)
	}
	promptPath := promptFile.Name()
	defer func() { _ = os.Remove(promptPath) }()
	if _, wErr := promptFile.Write(prompt); wErr != nil {
		_ = promptFile.Close()
		return nil, false, fmt.Errorf("writing prompt tempfile: %w", wErr)
	}
	if cErr := promptFile.Close(); cErr != nil {
		return nil, false, fmt.Errorf("closing prompt tempfile: %w", cErr)
	}

	env := append(os.Environ(),
		"FLOWSTATE_AUTORESEARCH_RUN_ID="+inv.runID,
		"FLOWSTATE_AUTORESEARCH_SURFACE="+inv.relSurface,
		fmt.Sprintf("FLOWSTATE_AUTORESEARCH_TRIAL=%d", inv.trialN),
		"FLOWSTATE_AUTORESEARCH_PROMPT_FILE="+promptPath,
	)
	if inv.maxTurns > 0 {
		env = append(env, fmt.Sprintf("FLOWSTATE_AUTORESEARCH_DRIVER_MAX_TURNS=%d", inv.maxTurns))
	}
	cmd.Env = env

	stdin, pipeErr := cmd.StdinPipe()
	if pipeErr != nil {
		return nil, false, fmt.Errorf("driver stdin pipe: %w", pipeErr)
	}
	stdoutPipe, pipeErr := cmd.StdoutPipe()
	if pipeErr != nil {
		_ = stdin.Close()
		return nil, false, fmt.Errorf("driver stdout pipe: %w", pipeErr)
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if startErr := cmd.Start(); startErr != nil {
		return nil, false, fmt.Errorf("starting driver %q: %w", inv.driverPath, startErr)
	}

	// Write the prompt to stdin in a goroutine so a slow driver does
	// not deadlock the harness; close stdin when done so the driver
	// sees EOF.
	writeErrCh := make(chan error, 1)
	go func() {
		defer close(writeErrCh)
		_, wErr := stdin.Write(prompt)
		_ = stdin.Close()
		if wErr != nil {
			writeErrCh <- wErr
		}
	}()

	stdout, readErr := io.ReadAll(stdoutPipe)
	if readErr != nil {
		_ = cmd.Wait()
		return nil, false, fmt.Errorf("reading driver stdout: %w", readErr)
	}
	waitErr := cmd.Wait()
	if wErr := <-writeErrCh; wErr != nil {
		// stdin write errors are usually noise (driver exited
		// before consuming the full prompt); surface only if the
		// command itself failed.
		if waitErr != nil {
			return nil, false, fmt.Errorf("driver %q stdin: %w (wait: %v) (stderr: %s)",
				inv.driverPath, wErr, waitErr, strings.TrimSpace(stderrBuf.String()))
		}
	}
	if waitErr != nil {
		if driverCtx.Err() == context.DeadlineExceeded {
			return nil, true, fmt.Errorf("driver %q timed out after %s (stderr: %s)",
				inv.driverPath, inv.timeout, strings.TrimSpace(stderrBuf.String()))
		}
		return nil, false, fmt.Errorf("driver %q: %w (stderr: %s)",
			inv.driverPath, waitErr, strings.TrimSpace(stderrBuf.String()))
	}
	return stdout, false, nil
}

// runEvaluatorContent invokes the evaluator subprocess with the
// candidate bytes piped to stdin. A parallel FLOWSTATE_AUTORESEARCH_
// CANDIDATE_FILE tempfile carries the same bytes for evaluators that
// prefer a path-based channel; both shapes are always available.
// FLOWSTATE_AUTORESEARCH_SURFACE is NOT exposed in default mode — the
// candidate IS the surface; reading the on-disk surface would defeat
// the substrate swap.
//
// Contract enforcement is identical to runEvaluatorScript (plan v3.1
// § 4.6): one non-negative integer to stdout, exit 0, SIGTERM-then-
// SIGKILL on timeout. The shared parseEvaluatorStdout enforces the
// stdout shape.
func runEvaluatorContent(ctx context.Context, evaluatorPath, runID string, candidate []byte, timeout time.Duration) (evaluatorResult, error) {
	if evaluatorPath == "" {
		evaluatorPath = "scripts/validate-harness.sh"
	}

	evalCtx := ctx
	timedOut := false
	timeoutMS := int64(0)
	var cancel context.CancelFunc
	if timeout > 0 {
		evalCtx, cancel = context.WithCancel(ctx)
		defer cancel()
	}

	// Mirror channel: write the candidate to a tempfile.
	candFile, perr := os.CreateTemp("", fmt.Sprintf("autoresearch-candidate-%s-*.txt", runID))
	if perr != nil {
		return evaluatorResult{}, fmt.Errorf("staging candidate tempfile: %w", perr)
	}
	candPath := candFile.Name()
	defer func() { _ = os.Remove(candPath) }()
	if _, wErr := candFile.Write(candidate); wErr != nil {
		_ = candFile.Close()
		return evaluatorResult{}, fmt.Errorf("writing candidate tempfile: %w", wErr)
	}
	if cErr := candFile.Close(); cErr != nil {
		return evaluatorResult{}, fmt.Errorf("closing candidate tempfile: %w", cErr)
	}

	cmd := observedCommandContext(evalCtx, evaluatorPath)
	cmd.Env = append(os.Environ(),
		"FLOWSTATE_AUTORESEARCH_RUN_ID="+runID,
		"FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE="+candPath,
	)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return cmd.Process.Signal(syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = evaluatorTermGracePeriod

	if timeout > 0 {
		timeoutMS = timeout.Milliseconds()
		timer := time.AfterFunc(timeout, cancel)
		defer timer.Stop()
	}

	cmd.Stdin = strings.NewReader(string(candidate))
	startedAt := time.Now()
	stdout, runErr := cmd.Output()

	if timeout > 0 && evalCtx.Err() != nil && time.Since(startedAt) >= timeout {
		timedOut = true
	}
	if timedOut {
		return evaluatorResult{
			ContractViolation: true,
			Reason:            fmt.Sprintf("evaluator %q exceeded --evaluator-timeout %s", evaluatorPath, timeout),
			TimedOut:          true,
			TimeoutMS:         timeoutMS,
		}, nil
	}
	if runErr != nil {
		return evaluatorResult{
			ContractViolation: true,
			Reason:            fmt.Sprintf("evaluator %q exited non-zero: %v", evaluatorPath, runErr),
		}, nil
	}
	if len(stdout) > trialStdoutCaptureLimit {
		stdout = stdout[:trialStdoutCaptureLimit]
	}
	score, parseErr := parseEvaluatorStdout(string(stdout))
	if parseErr != nil {
		return evaluatorResult{
			ContractViolation: true,
			Reason:            fmt.Sprintf("evaluator %q: %s", evaluatorPath, parseErr.Error()),
		}, nil
	}
	return evaluatorResult{Score: float64(score)}, nil
}

// driverInvocation bundles the per-trial inputs runDriverScript needs.
// Live-driver Slice 1 grew the call site enough that a positional
// signature was getting unwieldy; the struct keeps callers readable
// and lets future flags ride along without further breakage.
type driverInvocation struct {
	driverPath     string
	worktreePath   string
	relSurface     string
	runID          string
	trialN         int
	promptFilePath string
	timeout        time.Duration
	maxTurns       int
}

// runDriverScript invokes the driver script with the worktree as cwd
// and the per-trial environment. Live-driver Slice 1 wires the prompt
// file and the timeout/turn caps through to the subprocess.
//
// Expected:
//   - inv.driverPath may be empty (no-op driver — useful for
//     fixed-point trajectories in tests where the driver makes no
//     edit). When empty, the function is a no-op and returns nil.
//   - inv.worktreePath is the worktree root.
//   - inv.relSurface is the surface path relative to the worktree.
//   - inv.runID is propagated via FLOWSTATE_AUTORESEARCH_RUN_ID.
//   - inv.trialN is the 1-based trial counter, exposed via
//     FLOWSTATE_AUTORESEARCH_TRIAL.
//   - inv.promptFilePath is the absolute path to the synthesised
//     per-trial prompt (may be empty when --driver-script is empty;
//     real driver runs always populate it).
//   - inv.timeout is the per-invocation wall-clock cap. A zero
//     duration disables the timeout (useful for fixture drivers in
//     tests where the deadline would race the subprocess).
//   - inv.maxTurns is propagated via
//     FLOWSTATE_AUTORESEARCH_DRIVER_MAX_TURNS so script drivers can
//     forward the cap to whatever underlying agent loop they wrap.
//
// Returns:
//   - (nil, nil) on a 0-exit invocation or when driverPath is empty.
//   - (nil, error) with timedOut=true wrapped in the error message
//     when the timeout fires.
//   - (nil, non-nil error) on a non-zero exit.
//
// Side effects:
//   - Executes the driver script as a subprocess; the script may
//     mutate any file under the worktree (including the surface).
//   - On timeout the subprocess is sent SIGTERM at the deadline and
//     SIGKILL `evaluatorTermGracePeriod` later (mirrors the evaluator
//     wall-clock pattern in runEvaluatorScript).
func runDriverScript(inv driverInvocation) (timedOut bool, err error) {
	if inv.driverPath == "" {
		return false, nil
	}
	ctx := context.Background()
	var cancel context.CancelFunc
	if inv.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, inv.timeout)
		defer cancel()
	}
	cmd := observedCommandContext(ctx, inv.driverPath)
	cmd.Dir = inv.worktreePath
	cmd.Cancel = func() error {
		// Mirror runEvaluatorScript's SIGTERM-then-grace pattern so a
		// well-behaved driver can flush partial state.
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = evaluatorTermGracePeriod

	env := append(os.Environ(),
		"FLOWSTATE_AUTORESEARCH_RUN_ID="+inv.runID,
		"FLOWSTATE_AUTORESEARCH_SURFACE="+inv.relSurface,
		"FLOWSTATE_AGENT_DIR="+filepath.Join(inv.worktreePath, "internal", "app", "agents"),
		fmt.Sprintf("FLOWSTATE_AUTORESEARCH_TRIAL=%d", inv.trialN),
	)
	if inv.promptFilePath != "" {
		env = append(env, "FLOWSTATE_AUTORESEARCH_PROMPT_FILE="+inv.promptFilePath)
	}
	if inv.maxTurns > 0 {
		env = append(env, fmt.Sprintf("FLOWSTATE_AUTORESEARCH_DRIVER_MAX_TURNS=%d", inv.maxTurns))
	}
	cmd.Env = env

	output, runErr := cmd.CombinedOutput()
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return true, fmt.Errorf("driver %q: timed out after %s (output: %s)", inv.driverPath, inv.timeout, strings.TrimSpace(string(output)))
		}
		return false, fmt.Errorf("driver %q: %w (output: %s)", inv.driverPath, runErr, strings.TrimSpace(string(output)))
	}
	return false, nil
}

// evaluatorResult captures the contract-aware outcome of one
// evaluator invocation. ContractViolation is true when the script's
// behaviour deviated from plan v3.1 § 4.6 (non-zero exit, non-integer
// stdout, negative integer, multi-line stdout, timeout). When set,
// Reason carries a short human-readable description for the trial
// record; the trial outcome's reason is mapped to
// `evaluator-contract-violation` regardless. TimeoutMS is the
// `--evaluator-timeout` budget that fired (in milliseconds) when
// TimedOut is true; otherwise zero.
type evaluatorResult struct {
	Score             float64
	ContractViolation bool
	Reason            string
	TimedOut          bool
	TimeoutMS         int64
}

// runEvaluatorScript invokes the evaluator script and applies the
// formal evaluator contract from plan v3.1 § 4.6.
//
// Contract enforced here (caller is responsible for mapping
// ContractViolation onto the trial reason):
//
//  1. Stdout — exactly one line (after trimming a trailing newline),
//     a non-negative integer in decimal. Multi-line stdout, empty
//     stdout, or any non-integer scalar is a contract violation.
//  2. Exit code — 0 on success. Non-zero is a contract violation.
//  3. Stderr — free-form; captured for diagnostics only.
//  4. Working directory — the worktree root.
//  5. Environment — FLOWSTATE_AUTORESEARCH_RUN_ID,
//     FLOWSTATE_AUTORESEARCH_SURFACE, FLOWSTATE_AGENT_DIR.
//  6. Time budget — `timeout` caps wall-clock; SIGTERM at deadline,
//     SIGKILL `evaluatorTermGracePeriod` later. A timeout records a
//     contract violation with TimedOut=true and TimeoutMS set so the
//     trial record can persist `evaluator_timeout_ms`.
//
// Expected:
//   - evaluatorPath is the script to invoke; empty falls back to
//     the MVP default (validate-harness.sh).
//   - worktreePath becomes the cwd for the script.
//   - relSurface is exposed to the script via the documented env.
//   - runID is propagated.
//   - timeout is the evaluator wall-clock cap. Zero or negative
//     disables the timeout (used in the Slice 1 setup-only paths).
//
// Returns:
//   - An evaluatorResult populated with either the parsed score or a
//     ContractViolation flag and Reason. The function only returns a
//     non-nil error for harness-side I/O failures (e.g. unable to
//     fork the process for reasons other than the script being
//     missing); contract failures are returned in-band so the caller
//     can record them on the trial.
//
// Side effects:
//   - Executes the evaluator script as a subprocess. The subprocess
//     receives SIGTERM/SIGKILL on timeout.
func runEvaluatorScript(evaluatorPath, worktreePath, relSurface, runID string, timeout time.Duration) (evaluatorResult, error) {
	if evaluatorPath == "" {
		evaluatorPath = "scripts/validate-harness.sh"
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	timedOut := false
	timeoutMS := int64(0)
	if timeout > 0 {
		// CommandContext cancels by SIGKILL by default; wrap with a
		// SIGTERM-then-grace-then-SIGKILL pattern so well-behaved
		// evaluators get a chance to clean up.
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
	}

	cmd := observedCommandContext(ctx, evaluatorPath)
	cmd.Dir = worktreePath
	cmd.Env = append(os.Environ(),
		"FLOWSTATE_AUTORESEARCH_RUN_ID="+runID,
		"FLOWSTATE_AUTORESEARCH_SURFACE="+relSurface,
		"FLOWSTATE_AGENT_DIR="+filepath.Join(worktreePath, "internal", "app", "agents"),
	)
	cmd.Cancel = func() error {
		// Send SIGTERM first; CommandContext's default behaviour is
		// SIGKILL which would skip the documented grace window.
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = evaluatorTermGracePeriod

	if timeout > 0 {
		timeoutMS = timeout.Milliseconds()
		// Trip the cancel after the configured timeout. The
		// WaitDelay above forces SIGKILL grace.
		timer := time.AfterFunc(timeout, func() {
			cancel()
		})
		defer timer.Stop()
	}

	startedAt := time.Now()
	stdout, runErr := cmd.Output()

	// Detect timeout: ctx is cancelled iff the timer fired (we
	// only cancel from the timer in the timeout path).
	if timeout > 0 && ctx.Err() != nil && time.Since(startedAt) >= timeout {
		timedOut = true
	}

	if timedOut {
		return evaluatorResult{
			ContractViolation: true,
			Reason:            fmt.Sprintf("evaluator %q exceeded --evaluator-timeout %s", evaluatorPath, timeout),
			TimedOut:          true,
			TimeoutMS:         timeoutMS,
		}, nil
	}
	if runErr != nil {
		return evaluatorResult{
			ContractViolation: true,
			Reason:            fmt.Sprintf("evaluator %q exited non-zero: %v", evaluatorPath, runErr),
		}, nil
	}

	if len(stdout) > trialStdoutCaptureLimit {
		stdout = stdout[:trialStdoutCaptureLimit]
	}

	score, parseErr := parseEvaluatorStdout(string(stdout))
	if parseErr != nil {
		return evaluatorResult{
			ContractViolation: true,
			Reason:            fmt.Sprintf("evaluator %q: %s", evaluatorPath, parseErr.Error()),
		}, nil
	}
	return evaluatorResult{Score: float64(score)}, nil
}

// parseEvaluatorStdout enforces the stdout half of plan v3.1 § 4.6.
//
// Accepted shapes:
//   - "<digits>\n"
//   - "<digits>" (no trailing newline)
//
// Rejected (returns non-nil error):
//   - empty stdout
//   - any character outside [0-9] inside the integer (negative sign
//     included — non-negative integer is the rule)
//   - more than one non-empty line after splitting on '\n'
//
// Whitespace surrounding the integer on its single line is trimmed.
func parseEvaluatorStdout(stdout string) (int64, error) {
	// Drop a single trailing newline so a well-formed
	// "12\n" parses identically to "12". Anything beyond that is
	// inspected line-by-line below.
	body := strings.TrimRight(stdout, "\n")
	if strings.TrimSpace(body) == "" {
		return 0, errors.New("stdout was empty (contract: exactly one non-negative integer)")
	}

	lines := strings.Split(body, "\n")
	nonEmpty := 0
	var integerLine string
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		nonEmpty++
		integerLine = strings.TrimSpace(ln)
	}
	if nonEmpty != 1 {
		return 0, fmt.Errorf(
			"stdout had %d non-empty lines (contract: exactly one non-negative integer)",
			nonEmpty,
		)
	}

	score, err := strconv.ParseInt(integerLine, 10, 64)
	if err != nil {
		return 0, fmt.Errorf(
			"stdout %q not a base-10 integer (contract: exactly one non-negative integer)",
			integerLine,
		)
	}
	if score < 0 {
		return 0, fmt.Errorf(
			"stdout %d is negative (contract: exactly one non-negative integer; --metric-direction max inverts comparison logic, evaluator does not emit negatives)",
			score,
		)
	}
	return score, nil
}

// surfaceSHA returns the hex-encoded SHA-256 of the surface file's
// content. Used to detect fixed-point candidates (§ 4.7).
//
// Expected:
//   - path is an absolute path to an existing file.
//
// Returns:
//   - The lowercase hex SHA-256.
//   - An error if the file cannot be read.
//
// Side effects:
//   - Reads the file once into memory.
func surfaceSHA(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// isFixedPoint returns true when sha matches any candidate already in
// the ring. Linear scan is fine — the ring caps at 20 entries.
func isFixedPoint(ring []seenCandidate, sha string) bool {
	for _, c := range ring {
		if c.CandidateSHA == sha {
			return true
		}
	}
	return false
}

// appendSeenRing appends an entry to the seen-candidates ring,
// truncating from the front to maintain the ring capacity.
func appendSeenRing(ring []seenCandidate, entry seenCandidate) []seenCandidate {
	ring = append(ring, entry)
	if len(ring) > seenCandidatesRingCapacity {
		ring = ring[len(ring)-seenCandidatesRingCapacity:]
	}
	return ring
}

// appendRecentOutcomes appends a trial outcome to the rolling history
// the synthesiser renders into the # HISTORY section of the next
// trial's driver prompt. Live-driver Slice 1.
//
// window caps the slice from the front. A non-positive window falls
// back to driverPromptHistoryDefault so the loop never accumulates an
// unbounded slice when the operator passes --prompt-history-window 0.
func appendRecentOutcomes(history []trialOutcome, entry trialOutcome, window int) []trialOutcome {
	if window <= 0 {
		window = driverPromptHistoryDefault
	}
	history = append(history, entry)
	if len(history) > window {
		history = history[len(history)-window:]
	}
	return history
}

// isImprovement compares score against the best-so-far given
// --metric-direction. The first scored trial is always an improvement.
func isImprovement(state *trialLoopState, score float64, direction string) bool {
	if !state.bestScoreSet {
		// Bootstrap: if a baseline is unset, the first trial sets it
		// only when its commit is kept. With min direction the first
		// trial is "improved" (it is the new best); with max
		// direction the same. Tests pin this — first scored trial is
		// always an improvement.
		return true
	}
	if direction == metricDirectionMax {
		return score > state.bestScore
	}
	return score < state.bestScore
}

// relativeSurfacePath returns the surface path relative to the
// worktree root. Both surface and worktreePath are expected to be
// rooted in the same parent repo — surface lives in the operator's
// tree, but the worktree's checkout mirrors the same path layout.
func relativeSurfacePath(surface, worktreePath string) (string, error) {
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

// gitCommitTrial commits the candidate edit inside the worktree.
// Per § 5.5 N13, --no-verify is mandatory; the worktree inherits the
// parent's hooks including the make-check gate broken on origin.
func gitCommitTrial(worktreePath string, n int) (string, error) {
	addCmd := observedCommand("git", "-C", worktreePath, "add", "-A")
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	commitCmd := observedCommand("git", "-C", worktreePath, "commit",
		"--no-verify",
		"--allow-empty-message",
		"-m", fmt.Sprintf("autoresearch trial-%d", n),
	)
	commitCmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=autoresearch",
		"GIT_AUTHOR_EMAIL=autoresearch@flowstate.local",
		"GIT_COMMITTER_NAME=autoresearch",
		"GIT_COMMITTER_EMAIL=autoresearch@flowstate.local",
	)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git commit: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	revCmd := observedCommand("git", "-C", worktreePath, "rev-parse", "HEAD")
	out, err := revCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitResetHard runs `git reset --hard HEAD~1` inside the worktree —
// the canonical revert for a regression-or-no-improve candidate.
func gitResetHard(worktreePath string) error {
	cmd := observedCommand("git", "-C", worktreePath, "reset", "--hard", "HEAD~1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset --hard HEAD~1: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// gitCheckoutSurface restores the surface file in the worktree to its
// HEAD content — used by the fixed-point and manifest-gate paths
// where the driver wrote a candidate but no commit was issued, so
// `reset --hard HEAD~1` would over-revert.
func gitCheckoutSurface(worktreePath, relSurface string) error {
	cmd := observedCommand("git", "-C", worktreePath, "checkout", "HEAD", "--", relSurface)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout HEAD %s: %w (output: %s)", relSurface, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// writeTrialRecord persists a trial outcome to the coord-store.
func writeTrialRecord(store coordination.Store, runID string, outcome trialOutcome) error {
	raw, err := json.Marshal(outcome)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("autoresearch/%s/trial-%d", runID, outcome.N)
	return store.Set(key, raw)
}

// writeBestRecord persists the best-so-far pointer to the coord-store.
//
// Content mode (April 2026 In-Memory Default): CommitSHA is empty
// and CandidateContentSHA carries the load-bearing content identifier.
// In --commit-trials mode the legacy CommitSHA stays populated.
func writeBestRecord(store coordination.Store, runID string, state *trialLoopState) error {
	rec := bestRecord{
		CommitSHA:           state.bestCommitSHA,
		CandidateContentSHA: state.bestContentSHA,
		Score:               state.bestScore,
		TrialN:              state.bestTrialN,
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("autoresearch/%s/best", runID)
	return store.Set(key, raw)
}

// writeSeenCandidates persists the SHA ring as a single JSON array.
func writeSeenCandidates(store coordination.Store, runID string, ring []seenCandidate) error {
	raw, err := json.Marshal(ring)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("autoresearch/%s/seen-candidates", runID)
	return store.Set(key, raw)
}

// writeResultRecord persists the run-end summary. Total trials is
// derived from the last outcome's N; final score/commit are taken
// from best-so-far when set, otherwise from the last outcome.
func writeResultRecord(
	store coordination.Store,
	runID, terminationReason string,
	state *trialLoopState,
	last trialOutcome,
) error {
	rec := resultRecord{
		Converged:         terminationReason == terminationConverged,
		TotalTrials:       last.N,
		EndedAt:           time.Now().UTC().Format(time.RFC3339),
		TerminationReason: terminationReason,
	}
	if state.bestScoreSet {
		rec.FinalScore = state.bestScore
		rec.FinalCommit = state.bestCommitSHA
	} else {
		rec.FinalScore = math.NaN()
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("autoresearch/%s/result", runID)
	return store.Set(key, raw)
}
