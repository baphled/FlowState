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
//  3. Manifest gate — when the surface is a manifest, validate it
//     via agent.LoadAndValidateManifest. Failure records
//     `manifest-validate-failed` and reverts the uncommitted edit.
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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
)

// trialOutcome captures the per-trial decision. The reason string is
// the canonical taxonomy from plan § 4.2.
type trialOutcome struct {
	N            int     `json:"n"`
	CommitSHA    string  `json:"commit_sha"`
	CandidateSHA string  `json:"candidate_sha"`
	Score        float64 `json:"score"`
	Kept         bool    `json:"kept"`
	Reason       string  `json:"reason"`
	DurationS    float64 `json:"duration_s"`
	StartedAt    string  `json:"started_at"`
	EndedAt      string  `json:"ended_at"`
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
	terminationConverged                  = "converged"
	terminationMaxTrials                  = "max-trials"
	terminationTimeBudget                 = "time-budget"
	terminationFixedPointSaturated        = "fixed-point-saturated"
	terminationManifestGateFailureRate    = "manifest-gate-failure-rate"
	terminationEvaluatorContractFailure   = "evaluator-contract-failure-rate"
	terminationSignal                     = "signal"

	// Threshold constants from plan § 4.7.
	fixedPointSaturationLimit  = 10
	manifestGateFailureLimit   = 3
	seenCandidatesRingCapacity = 20

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
	consecutiveFixedPoint    int
	consecutiveManifestFails int
	consecutiveNoImprove     int
	bestScore                float64
	bestScoreSet             bool
	bestCommitSHA            string
	bestTrialN               int
	seenCandidates           []seenCandidate
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
type bestRecord struct {
	CommitSHA string  `json:"commit_sha"`
	Score     float64 `json:"score"`
	TrialN    int     `json:"trial_n"`
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
func runTrialLoop(
	resolved autoresearchRunOptions,
	worktreePath string,
	store coordination.Store,
	out io.Writer,
) (string, error) {
	state := &trialLoopState{}
	deadline := time.Now().Add(resolved.timeBudget)

	relSurface, err := relativeSurfacePath(resolved.surface, worktreePath)
	if err != nil {
		return "", err
	}
	worktreeSurface := filepath.Join(worktreePath, relSurface)

	// Baseline scoring: hash + score the unmodified surface so trial
	// 1 has a `best` to compare against. Without this, the very
	// first scored trial would always be `improved` regardless of
	// its scalar — the seam tests pin the regression-on-trial-1
	// case directly. Per § 4.2 the manifest record's
	// `baseline_score`/`baseline_commit` are the persistent home of
	// this value; Slice 1d will extend the manifest record write to
	// include it.
	baselineSHA, err := surfaceSHA(worktreeSurface)
	if err != nil {
		return "", fmt.Errorf("hashing baseline surface: %w", err)
	}
	baselineScore, err := runEvaluatorScript(resolved.evaluatorScript, worktreePath, relSurface, resolved.runID)
	if err != nil {
		return "", fmt.Errorf("baseline evaluator: %w", err)
	}
	state.bestScore = baselineScore
	state.bestScoreSet = true
	state.seenCandidates = appendSeenRing(state.seenCandidates, seenCandidate{
		CandidateSHA: baselineSHA, TrialN: 0, Score: baselineScore,
	})
	if err := writeSeenCandidates(store, resolved.runID, state.seenCandidates); err != nil {
		return "", fmt.Errorf("seeding seen-candidates: %w", err)
	}

	terminationReason := ""

	var lastOutcome trialOutcome
	for n := 1; n <= resolved.maxTrials; n++ {
		if time.Now().After(deadline) {
			terminationReason = terminationTimeBudget
			break
		}

		outcome, err := runOneTrial(n, resolved, worktreePath, worktreeSurface, relSurface, state)
		if err != nil {
			return "", fmt.Errorf("trial %d: %w", n, err)
		}
		lastOutcome = outcome

		if err := writeTrialRecord(store, resolved.runID, outcome); err != nil {
			return "", fmt.Errorf("writing trial-%d record: %w", n, err)
		}
		if err := writeSeenCandidates(store, resolved.runID, state.seenCandidates); err != nil {
			return "", fmt.Errorf("writing seen-candidates: %w", err)
		}
		if outcome.Kept {
			if err := writeBestRecord(store, resolved.runID, state); err != nil {
				return "", fmt.Errorf("writing best record: %w", err)
			}
		}

		_, _ = fmt.Fprintf(out,
			"trial %d: kept=%v reason=%s score=%g\n",
			outcome.N, outcome.Kept, outcome.Reason, outcome.Score)

		// Termination checks (post-trial).
		if state.consecutiveFixedPoint >= fixedPointSaturationLimit {
			terminationReason = terminationFixedPointSaturated
			break
		}
		if state.consecutiveManifestFails >= manifestGateFailureLimit {
			terminationReason = terminationManifestGateFailureRate
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
		return terminationReason, fmt.Errorf("writing result record: %w", err)
	}

	return terminationReason, nil
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
		N:         n,
		StartedAt: startedAt.UTC().Format(time.RFC3339),
	}

	if err := runDriverScript(resolved.driverScript, worktreePath, relSurface, resolved.runID); err != nil {
		return outcome, fmt.Errorf("driver script: %w", err)
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

	if isManifestSurface(resolved.surface) {
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

	score, err := runEvaluatorScript(resolved.evaluatorScript, worktreePath, relSurface, resolved.runID)
	if err != nil {
		// Evaluator-side failure: revert and record. Slice 5
		// formalises the contract; Slice 1 just records the
		// underlying signal.
		if rErr := gitResetHard(worktreePath); rErr != nil {
			return outcome, fmt.Errorf("evaluator failure recovery: %w", rErr)
		}
		outcome.Kept = false
		outcome.Reason = reasonEvaluatorContractFail
		outcome.Score = 0
		state.consecutiveFixedPoint = 0
		state.consecutiveManifestFails = 0
		state.seenCandidates = appendSeenRing(state.seenCandidates, seenCandidate{
			CandidateSHA: candidateSHA, TrialN: n, Score: 0,
		})
		finishOutcome(&outcome, startedAt)
		return outcome, nil
	}
	outcome.Score = score

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

// runDriverScript invokes the driver script with the worktree as cwd
// and the per-trial environment.
//
// Expected:
//   - driverPath may be empty (no-op driver — useful for fixed-point
//     trajectories in tests where the driver makes no edit).
//   - worktreePath is the worktree root.
//   - relSurface is the surface path relative to the worktree.
//   - runID is propagated to the driver via FLOWSTATE_AUTORESEARCH_RUN_ID.
//
// Returns:
//   - nil on a 0-exit driver invocation, or when driverPath is empty.
//   - non-nil on a non-zero driver exit.
//
// Side effects:
//   - Executes the driver script as a subprocess; the script may
//     mutate any file under the worktree (including the surface).
func runDriverScript(driverPath, worktreePath, relSurface, runID string) error {
	if driverPath == "" {
		return nil
	}
	cmd := exec.Command(driverPath)
	cmd.Dir = worktreePath
	cmd.Env = append(os.Environ(),
		"FLOWSTATE_AUTORESEARCH_RUN_ID="+runID,
		"FLOWSTATE_AUTORESEARCH_SURFACE="+relSurface,
		"FLOWSTATE_AGENT_DIR="+filepath.Join(worktreePath, "internal", "app", "agents"),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("driver %q: %w (output: %s)", driverPath, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// runEvaluatorScript invokes the evaluator script and parses its
// stdout into an integer score. Per the evaluator contract (§ 4.6),
// stdout is exactly one integer line; stderr is free-form. The score
// is returned as float64 so the ratchet can compare via standard
// numeric ops; the trial record persists the same value.
//
// Expected:
//   - evaluatorPath is the script to invoke; empty falls back to
//     the MVP default (validate-harness.sh --score). For Slice 1
//     tests we always pass an explicit script, so the empty-path
//     branch is not exercised by the seam spec.
//   - worktreePath becomes the cwd for the script.
//   - relSurface is exposed to the script via the documented env.
//   - runID is propagated.
//
// Returns:
//   - The parsed integer score on success.
//   - An error if the script exits non-zero, stdout is not parseable,
//     or stdout is empty.
//
// Side effects:
//   - Executes the evaluator script as a subprocess.
func runEvaluatorScript(evaluatorPath, worktreePath, relSurface, runID string) (float64, error) {
	if evaluatorPath == "" {
		evaluatorPath = "scripts/validate-harness.sh"
	}
	cmd := exec.Command(evaluatorPath)
	cmd.Dir = worktreePath
	cmd.Env = append(os.Environ(),
		"FLOWSTATE_AUTORESEARCH_RUN_ID="+runID,
		"FLOWSTATE_AUTORESEARCH_SURFACE="+relSurface,
		"FLOWSTATE_AGENT_DIR="+filepath.Join(worktreePath, "internal", "app", "agents"),
	)
	stdout, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("evaluator %q: %w", evaluatorPath, err)
	}

	if len(stdout) > trialStdoutCaptureLimit {
		stdout = stdout[:trialStdoutCaptureLimit]
	}
	line := strings.TrimSpace(string(stdout))
	if line == "" {
		return 0, fmt.Errorf("evaluator %q emitted no integer scalar", evaluatorPath)
	}
	// Take only the first non-empty line in case the evaluator
	// emits structured output. Slice 5 will harden the parse.
	firstLine := strings.SplitN(line, "\n", 2)[0]
	score, err := strconv.ParseInt(strings.TrimSpace(firstLine), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("evaluator %q: stdout %q not an integer: %w", evaluatorPath, firstLine, err)
	}
	return float64(score), nil
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

// isManifestSurface returns true when the surface path is under
// `internal/app/agents/`. Slice 4 broadens the detection rule per
// § 4.4; Slice 1 sticks with the MVP path-prefix probe so the gate
// fires for the planner.md hard-coded surface.
func isManifestSurface(surface string) bool {
	cleaned := filepath.Clean(surface)
	parts := strings.Split(cleaned, string(filepath.Separator))
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == "internal" && parts[i+1] == "app" && parts[i+2] == "agents" {
			return true
		}
	}
	return false
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
	addCmd := exec.Command("git", "-C", worktreePath, "add", "-A")
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	commitCmd := exec.Command("git", "-C", worktreePath, "commit",
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
	revCmd := exec.Command("git", "-C", worktreePath, "rev-parse", "HEAD")
	out, err := revCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitResetHard runs `git reset --hard HEAD~1` inside the worktree —
// the canonical revert for a regression-or-no-improve candidate.
func gitResetHard(worktreePath string) error {
	cmd := exec.Command("git", "-C", worktreePath, "reset", "--hard", "HEAD~1")
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
	cmd := exec.Command("git", "-C", worktreePath, "checkout", "HEAD", "--", relSurface)
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
func writeBestRecord(store coordination.Store, runID string, state *trialLoopState) error {
	rec := bestRecord{
		CommitSHA: state.bestCommitSHA,
		Score:     state.bestScore,
		TrialN:    state.bestTrialN,
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
