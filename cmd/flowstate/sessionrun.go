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

// errProvider marks provider failures (code provider_error, exit 3
// per CLI-AND-API-CONTRACT §1).
var errProvider = fmt.Errorf("provider_error")

// exitProviderError is the stable provider-failure exit code.
const exitProviderError = 3

// selftestProvider and selftestModel are the live self-test route
// labels; attribution always comes from the runtime session row.
const (
	selftestProvider = "z.ai"
	selftestModel    = "glm-4.6"
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
	sess, err := app.CreateSelfTestSession(ctx, st, selftestProvider, selftestModel, task)
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
// a gated live self-test): runs one bounded provider turn, projects
// the persisted events as JSONL on stdout and ends with a terminal
// event envelope. Exit 0 completed; 1 unknown session; 2 usage;
// 3 provider error; 4 gates closed (task mode); 6 already terminal.
func cmdRun(ctx context.Context, st *app.Store, id string, fl *flags, stdout, stderr *os.File) int {
	if fl.task != "" {
		if id != "" {
			writeError(stderr, fmt.Errorf("%w: session run takes an id or --task, not both", app.ErrUsage))
			return exitUsage
		}
		selfTestRun = true
		defer func() { selfTestRun = false }()
		return runSelfTestTask(ctx, st, fl, stdout, stderr)
	}
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", app.ErrUsage))
		return exitUsage
	}
	if fl.budget < 0 || fl.maxTokens < 0 {
		writeError(stderr, fmt.Errorf("%w: --budget and --max-tokens must not be negative", app.ErrUsage))
		return exitUsage
	}
	sess, err := st.GetSession(ctx, id)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if sess.Status != "active" && sess.Status != "paused" {
		writeError(stderr, fmt.Errorf("%w: session %s is %s", app.ErrTerminal, id, sess.Status))
		return exitTerminal
	}
	p, err := providerResolver(sess.Provider)
	if err != nil {
		if errors.Is(err, app.ErrSessionCancelled) {
			writeError(stderr, fmt.Errorf("%w: interrupted; session cancelled", app.ErrSessionCancelled))
			return exitSigint
		}
		writeError(stderr, fmt.Errorf("%w: %v", errProvider, err))
		return exitProviderError
	}
	s, ok := p.(app.Streamer)
	if !ok {
		writeError(stderr, fmt.Errorf("%w: provider %s does not support streaming turns", errProvider, sess.Provider))
		return exitProviderError
	}
	res, err := app.TurnWithOptions(ctx, st, sess, s, app.TurnOptions{
		Budget:    fl.budget,
		MaxTokens: fl.maxTokens,
	})
	// Receipt hook: live self-test runs emit their attempt record as
	// a post-turn side-effect of the same run path.
	if selfTestRun {
		if recErr := emitSelfTestReceipt(ctx, fl, sess, res, err); recErr != nil {
			writeError(stderr, recErr)
			return exitCodeFor(recErr)
		}
	}
	// Replay on a fresh context: after SIGINT the cancel path still
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
	return finishRun(err, res, fl, stderr)
}

// finishRun maps the turn outcome onto its exit code: SIGINT
// cancellation exits 130, provider failures 3, budget stops 5.
func finishRun(err error, res *app.TurnResult, fl *flags, stderr *os.File) int {
	if err != nil {
		if errors.Is(err, app.ErrSessionCancelled) {
			writeError(stderr, fmt.Errorf("%w: interrupted; session cancelled", app.ErrSessionCancelled))
			return exitSigint
		}
		writeError(stderr, fmt.Errorf("%w: %v", errProvider, err))
		return exitProviderError
	}
	if res != nil && res.BudgetExceeded {
		writeError(stderr, fmt.Errorf("%w: budget of %d exceeded after %d tokens; session paused", app.ErrBudgetExceeded, fl.budget, res.ConsumedBudget))
		return exitBudget
	}
	return exitOK
}
