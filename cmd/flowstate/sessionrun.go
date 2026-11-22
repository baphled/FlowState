package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// selfTestRun marks a live self-test invocation so the post-turn
// receipt hook fires on the run path.
var selfTestRun bool

// providerResolver is the CLI's provider factory seam; tests swap it
// for an httptest-backed adapter, production uses the registry.
var providerResolver app.ProviderResolver = app.ResolveProvider

// exitProviderError is the stable provider-failure exit code.
const exitProviderError = 3

// selftestProvider and selftestModel are the live self-test route
// labels; attribution always comes from the runtime session row.
const (
	selftestProvider = "z.ai"
	selftestModel    = "glm-5.3"
)

// selftestDefaultBudget is the hard cap applied when --budget is not
// supplied on a live self-test run: a live turn may never run
// unlimited (M1-SELFTEST).
const selftestDefaultBudget = 20000

// selftestArtefact is the transcript artefact name per attempt.
const selftestArtefact = "transcript.jsonl"

// liveGate reports whether both live gates are open: a provider API
// key is present AND the operator opted in via FLOWSTATE_ALLOW_LIVE=1
// (CI has no key so the live path is unreachable there).
func liveGate() bool {
	return os.Getenv("ZAI_API_KEY") != "" && os.Getenv("FLOWSTATE_ALLOW_LIVE") == "1"
}

// runSelfTestTask handles `session run --task`: creates a live
// self-test session via the standard session-create path (emitting
// session.started like any other session), applies the default
// budget cap when none was supplied, prints the new session row,
// then runs the ordinary run path.
func runSelfTestTask(ctx context.Context, st *app.Store, fl *flags, stdout, stderr *os.File) int {
	if !liveGate() {
		writeError(stderr, fmt.Errorf("%w: live self-test run requires both ZAI_API_KEY and FLOWSTATE_ALLOW_LIVE=1", app.ErrConfigInvalid))
		return exitConfigInvalid
	}
	if fl.budget == 0 {
		fl.budget = selftestDefaultBudget
	}
	task, err := readTask(fl.task)
	if err != nil {
		writeError(stderr, err)
		return exitUsage
	}
	model := selftestModel
	if fl.model != "" {
		model = fl.model
	}
	sess, err := app.CreateSelfTestSession(ctx, st, selftestProvider, model, task)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if err := writeJSON(stdout, map[string]any{"id": sess.ID, "status": sess.Status}); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	// The task flag drove session creation only; the actual turn runs
	// through the ordinary id-based run path with the receipt hook on.
	fl.task = ""
	return cmdRun(ctx, st, sess.ID, fl, stdout, stderr)
}

// emitSelfTestReceipt is the post-turn hook side-effect of a live
// self-test run: it writes the immutable attempt record using
// runtime-observed attribution; skipped when the turn errored so
// failures never masquerade as recorded attempts.
func emitSelfTestReceipt(ctx context.Context, fl *flags, sess *app.Session, res *app.TurnResult, terr error) error {
	if terr != nil {
		return nil
	}
	outcome := "pass"
	if res != nil && res.BudgetExceeded {
		outcome = "fail"
	}
	sha := os.Getenv("FLOWSTATE_COMMIT_SHA")
	if len(sha) != 40 {
		return fmt.Errorf("%w: FLOWSTATE_COMMIT_SHA must be a 40-char sha", app.ErrConfigInvalid)
	}
	body, err := selfTestTranscript(ctx, fl, sess.ID)
	if err != nil {
		return err
	}
	author := os.Getenv("FLOWSTATE_AUTHOR_IDENTITY")
	if author == "" {
		author = "git:config"
	}
	_, rerr := app.EmitAttemptReceipt(ctx, fl.db, app.Attempt{
		ID:           "selftest-" + sess.ID,
		CapabilityID: "M1-SELFTEST",
		CommitSHA:    sha,
		GateOutcome:  outcome,
		Attribution: app.Attribution{
			Provider:       sess.Provider,
			ModelID:        sess.ModelID,
			Harness:        sess.Harness,
			AuthorIdentity: author,
			Source:         "flowstate session run --task",
		},
		Artefacts: []app.Artefact{{Name: selftestArtefact, Body: body}},
	})
	if rerr != nil {
		return fmt.Errorf("app: emit self-test receipt: %w", rerr)
	}
	return nil
}

// selfTestTranscript exports the session transcript as artefact body.
func selfTestTranscript(ctx context.Context, fl *flags, id string) ([]byte, error) {
	st, err := openStore(fl.db)
	if err != nil {
		return nil, fmt.Errorf("app: open store for transcript: %w", err)
	}
	defer func() { _ = st.Close() }()
	body, err := st.ExportJSONL(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("app: export transcript: %w", err)
	}
	return body, nil
}

// cmdRun implements `flowstate session run <id>` (or, with --task,
// a gated live self-test): runs one bounded provider turn, explicitly
// resuming a paused session first, projects the persisted events as
// JSONL on stdout and ends with a terminal event envelope. Exit 0
// completed (a truncated answer, finish reason length, is still 0 and
// adds one stderr warning line); 1 unknown session or any other
// unclassified failure, which reports code `internal`; 2 usage; 3
// provider error; 4 config_invalid (live gates closed in task mode,
// or the database, crash recovery or the steering drain failed); 5
// budget stop; 6 already terminal; 7 replay_mismatch (corrupt replay
// found during the turn); 130 cancelled by SIGINT, SIGTERM or SIGHUP
// (reason sigint, sigterm or sighup). Exit 8 (receipt_invalid) is not
// reachable from here: it belongs to `swarm run` and `receipt
// validate`.
func cmdRun(ctx context.Context, st *app.Store, id string, fl *flags, stdout, stderr *os.File) int {
	applyRunDefaults(fl)
	if fl.task != "" {
		if id != "" {
			writeError(stderr, fmt.Errorf("%w: session run takes an id or --task, not both", app.ErrUsage))
			return exitUsage
		}
		selfTestRun = true
		defer func() { selfTestRun = false }()
		return runSelfTestTask(ctx, st, fl, stdout, stderr)
	}
	if code, done := runSessionByID(id, fl, stderr); done {
		return code
	}
	sess, loadErr := loadRunnableSession(ctx, st, id)
	if loadErr != nil {
		writeError(stderr, loadErr)
		return exitCodeFor(loadErr)
	}
	// Crash-recovery: detect an active session whose last turn was left
	// incomplete (process died mid-turn) and append session.interrupted
	// before proceeding, so the event log records the gap.
	if crashed, derr := app.DetectCrashedSession(ctx, st, id); derr == nil && crashed {
		if rerr := app.RecoverCrashedSession(ctx, st, id); rerr != nil {
			writeError(stderr, fmt.Errorf("%w: crash recovery: %v", app.ErrConfigInvalid, rerr))
			return exitConfigInvalid
		}
	}
	if code, done := resumePausedRun(ctx, st, sess, stderr); done {
		return code
	}
	// Steering drain: pending messages addressed to this session
	// become session.steered events (send order) before turn work, so
	// a directive lands at the next turn boundary.
	if _, derr := app.DrainSteeringMessages(ctx, st, sess.ID); derr != nil {
		writeError(stderr, fmt.Errorf("%w: steering drain: %v", app.ErrConfigInvalid, derr))
		return exitConfigInvalid
	}
	// Resolve the effective budget: an explicit --budget anchors the
	// session's shared pool; a delegated child without one draws from
	// its reservation remainder (M2 shared budgets).
	budget, berr := app.ResolveRunBudget(ctx, st, sess, fl.budget)
	if berr != nil {
		writeError(stderr, berr)
		return exitCodeFor(berr)
	}
	fl.budget = budget
	return runSessionTurn(ctx, st, sess, fl, stdout, stderr)
}

// applyRunDefaults resolves config-supplied run defaults for flags
// the invocation omitted: --budget falls back to config
// default_budget ahead of any built-in default, so explicit flags
// strictly override config values.
func applyRunDefaults(fl *flags) {
	if !fl.budgetSet && fl.config.DefaultBudget > 0 {
		fl.budget = fl.config.DefaultBudget
	}
}

// resumePausedRun routes a paused session through the explicit
// app-layer resume before any turn work (CLI-AND-API-CONTRACT §3):
// the store appends session.resumed (actor user, from_status paused)
// and restores status active, then the ordinary run continues under
// a fresh budget derived from this invocation's flags alone — the
// pre-pause budget is never carried over. Non-paused runnable
// sessions pass straight through with no session.resumed emission.
// A resume append failure blocks the turn and surfaces the persisted
// failure; the session stays paused and unacknowledged.
func resumePausedRun(ctx context.Context, st *app.Store, sess *app.Session, stderr *os.File) (int, bool) {
	if sess.Status != "paused" {
		return exitOK, false
	}
	if rerr := app.ResumePausedSession(ctx, st, sess); rerr != nil {
		writeError(stderr, rerr)
		return exitCodeFor(rerr), true
	}
	return exitOK, false
}

// runSessionByID validates the plain id invocation's usage and
// reports whether the run finished (or failed) without a turn.
func runSessionByID(id string, fl *flags, stderr *os.File) (int, bool) {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", app.ErrUsage))
		return exitUsage, true
	}
	if fl.budget < 0 || fl.maxTokens < 0 {
		writeError(stderr, fmt.Errorf("%w: --budget and --max-tokens must not be negative", app.ErrUsage))
		return exitUsage, true
	}
	return exitOK, false
}

// loadRunnableSession loads the session row and rejects terminal
// states ahead of the provider turn.
func loadRunnableSession(ctx context.Context, st *app.Store, id string) (*app.Session, error) {
	sess, err := st.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if sess.Status != "active" && sess.Status != "paused" {
		return nil, fmt.Errorf("%w: session %s is %s", app.ErrTerminal, id, sess.Status)
	}
	return sess, nil
}

// runSessionTurn resolves the provider, runs one bounded turn, fires
// the receipt hook for live self-tests, projects the persisted events
// and maps the outcome onto its exit code.
func runSessionTurn(ctx context.Context, st *app.Store, sess *app.Session, fl *flags, stdout, stderr *os.File) int {
	id := sess.ID
	p, perr := providerResolver(sess.Provider)
	if perr != nil {
		if errors.Is(perr, app.ErrSessionCancelled) {
			return reportInterrupt(perr, stderr)
		}
		writeError(stderr, providerFailure(perr))
		return exitProviderError
	}
	s, ok := p.(app.Streamer)
	if !ok {
		writeError(stderr, providerFailure(fmt.Errorf("provider %s does not support streaming turns", sess.Provider)))
		return exitProviderError
	}
	res, terr := runTurnOrLoop(ctx, st, sess, s, fl)
	// Receipt hook: live self-test runs emit their attempt record as
	// a post-turn side-effect of the same run path.
	if selfTestRun {
		if recErr := emitSelfTestReceipt(ctx, fl, sess, res, terr); recErr != nil {
			writeError(stderr, recErr)
			return exitCodeFor(recErr)
		}
	}
	// Replay on a fresh context: after a signal the cancel path still
	// needs to project the persisted events before exiting 130.
	evts, rerr := st.Replay(context.WithoutCancel(ctx), id)
	if rerr != nil {
		writeError(stderr, rerr)
		return exitCodeFor(rerr)
	}
	for _, e := range evts {
		if err := writeJSON(stdout, map[string]any{
			"session_id":     e.SessionID,
			"seq":            e.Seq,
			"created_at_utc": e.CreatedAt.UTC().Format(timeFormat),
			"actor":          e.Actor,
			"type":           e.Type,
			"payload":        json.RawMessage(e.PayloadJSON),
			"payload_sha256": e.PayloadSHA,
		}); err != nil {
			writeError(stderr, err)
			return exitCodeFor(err)
		}
	}
	return finishRun(terr, res, fl, stderr)
}

// providerFailure labels an error from provider resolution as the
// provider's failure. A single wrap suffices: resolver errors never
// already carry app.ErrProvider (the engine wraps only running turns).
func providerFailure(err error) error {
	return fmt.Errorf("%w: %v", app.ErrProvider, err)
}

// finishRun maps the turn outcome onto its exit code: SIGINT, SIGTERM
// or SIGHUP cancellation exits 130, budget stops 5, tool-surface
// usage failures 2, and only errors that satisfy errors.Is(err,
// app.ErrProvider) exit 3. Any other error (a storage failure while
// recording the turn, a lease fence) keeps its own classification
// through exitCodeFor, so an unclassified one is `internal`. The
// loop path surfaces budget stops as an ErrBudgetExceeded error (and
// drops the result's budget flag), so the error arm carries it.
func finishRun(err error, res *app.TurnResult, fl *flags, stderr *os.File) int {
	if err != nil {
		if errors.Is(err, app.ErrSessionCancelled) {
			return reportInterrupt(err, stderr)
		}
		if errors.Is(err, app.ErrBudgetExceeded) {
			writeError(stderr, err)
			return exitBudget
		}
		if errors.Is(err, app.ErrUsage) {
			writeError(stderr, err)
			return exitUsage
		}
		writeError(stderr, err)
		if errors.Is(err, app.ErrProvider) {
			return exitProviderError
		}
		return exitCodeFor(err)
	}
	if res != nil && res.BudgetExceeded {
		writeError(stderr, fmt.Errorf("%w: budget of %d exceeded after %d tokens; session paused", app.ErrBudgetExceeded, fl.budget, res.ConsumedBudget))
		return exitBudget
	}
	noteTruncation(res, stderr)
	return exitOK
}

// reportInterrupt reports a cancelled run and returns exit 130. A
// reservation settle failure joined to the cancellation is printed first
// as its own line, as `session cancel` does, so it is not hidden behind
// the fixed interrupt message; the exit status never changes.
func reportInterrupt(err error, stderr *os.File) int {
	var se *app.SettleError
	if errors.As(err, &se) {
		writeError(stderr, fmt.Errorf("%w: %s", app.ErrReservationSettle, se.Detail()))
	}
	writeError(stderr, fmt.Errorf("%w: interrupted; session cancelled", app.ErrSessionCancelled))
	return exitSigint
}

// noteTruncation tells the person running the command that the final
// answer was cut off at the token limit. It writes one plain stderr
// line and never changes the exit code or the stdout event stream.
func noteTruncation(res *app.TurnResult, stderr *os.File) {
	if res == nil || !app.FinishTruncated(res.FinishReason) {
		return
	}
	_, _ = fmt.Fprintf(stderr, "warning: the answer was cut off at the token limit; run `flowstate session doctor %s` "+
		"to inspect it, or rerun with a higher --max-tokens\n", res.SessionID)
}

// runTurnOrLoop selects the execution surface for one run: a
// tool-surface invocation (explicit --allow/--allow-file/--workdir,
// or a config-derived allowlist when no such flag was given) drives
// the bounded provider execution loop; a plain invocation keeps the
// single-turn path.
func runTurnOrLoop(ctx context.Context, st *app.Store, sess *app.Session, s app.Streamer, fl *flags) (*app.TurnResult, error) {
	allow := effectiveAllowlist(fl)
	if !hasToolSurface(fl) {
		return app.TurnWithOptions(ctx, st, sess, s, app.TurnOptions{
			Budget:    fl.budget,
			MaxTokens: fl.maxTokens,
			ModelID:   fl.model,
		})
	}
	loopRes, err := app.RunSessionExecLoop(ctx, st, fl.db, sess, s, app.SessionExecLoopRequest{
		Allow:     allow,
		AllowFile: fl.allowFile,
		Workdir:   fl.workdir,
		Budget:    fl.budget,
		MaxTokens: fl.maxTokens,
	})
	return toTurnResult(sess, loopRes), err
}

// toolSurfaceFlagged reports whether the invocation supplied any
// explicit tool-surface flag (--allow, --allow-file, --workdir).
func toolSurfaceFlagged(fl *flags) bool {
	return len(fl.allow) > 0 || fl.allowFile != "" || fl.workdir != ""
}

// hasToolSurface reports whether the invocation activates the tool
// execution loop: an explicit tool-surface flag or a config-derived
// allowlist.
func hasToolSurface(fl *flags) bool {
	return toolSurfaceFlagged(fl) || len(fl.config.ToolAllowlist) > 0
}

// toTurnResult projects a loop result onto the shared turn result
// shape used by the CLI projection and receipt hook; the session id
// keeps the receipt hook bound to the run's session.
func toTurnResult(sess *app.Session, res *app.ExecLoopResult) *app.TurnResult {
	if res == nil {
		return nil
	}
	return &app.TurnResult{SessionID: sess.ID, FinalContent: res.FinalContent, FinishReason: res.FinishReason}
}
