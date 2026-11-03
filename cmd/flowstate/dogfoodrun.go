package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// dogfoodProvider and dogfoodModel are the requested route labels;
// attribution always comes from the runtime session row.
const (
	dogfoodProvider = "z.ai"
	dogfoodModel    = "glm-4.6"
)

// dogfoodDefaultArtefact is the transcript artefact name per attempt.
const dogfoodDefaultArtefact = "transcript.jsonl"

// finishDogfoodRun runs one budget-capped turn via the session-run
// path, emits the attempt receipt, then projects the persisted events
// as JSONL on stdout.
func finishDogfoodRun(ctx context.Context, st *app.Store, id string, fl *flags, stdout, stderr *os.File) int {
	sess, err := st.GetSession(ctx, id)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	p, perr := providerResolver(sess.Provider)
	if perr != nil {
		if errors.Is(perr, app.ErrSessionCancelled) {
			writeError(stderr, fmt.Errorf("%w: interrupted; session cancelled", app.ErrSessionCancelled))
			return exitSigint
		}
		writeError(stderr, fmt.Errorf("%w: %v", errProvider, perr))
		return exitProviderError
	}
	s, ok := p.(app.Streamer)
	if !ok {
		writeError(stderr, fmt.Errorf("%w: provider %s does not support streaming turns", errProvider, sess.Provider))
		return exitProviderError
	}
	res, terr := app.TurnWithOptions(ctx, st, sess, s, app.TurnOptions{
		Budget:    fl.budget,
		MaxTokens: fl.maxTokens,
	})
	if recErr := emitDogfoodReceipt(ctx, fl, sess, res, terr); recErr != nil {
		writeError(stderr, recErr)
		return exitCodeFor(recErr)
	}
	return replayDogfood(ctx, st, id, fl, terr, res, stdout, stderr)
}

// replayDogfood projects the persisted events as JSONL on stdout then
// maps the turn outcome onto its exit code.
func replayDogfood(ctx context.Context, st *app.Store, id string, fl *flags, terr error, res *app.TurnResult, stdout, stderr *os.File) int {
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

// emitDogfoodReceipt writes the immutable attempt record using
// runtime-observed attribution; skipped when the turn errored so
// failures never masquerade as recorded attempts.
func emitDogfoodReceipt(ctx context.Context, fl *flags, sess *app.Session, res *app.TurnResult, terr error) error {
	if terr != nil {
		return nil
	}
	outcome := "pass"
	if res != nil && res.BudgetExceeded {
		outcome = "fail"
	}
	sha, err := dogfoodCommitSHA()
	if err != nil {
		return err
	}
	body, err := dogfoodTranscript(ctx, fl, sess.ID)
	if err != nil {
		return err
	}
	_, rerr := app.EmitAttemptReceipt(ctx, fl.db, app.Attempt{
		ID:           "dogfood-" + sess.ID,
		CapabilityID: "M1-DOGFOOD",
		CommitSHA:    sha,
		GateOutcome:  outcome,
		Attribution: app.Attribution{
			Provider:       sess.Provider,
			ModelID:        sess.ModelID,
			Harness:        sess.Harness,
			AuthorIdentity: dogfoodAuthor(),
			Source:         "flowstate dogfood run",
		},
		Artefacts: []app.Artefact{{Name: dogfoodDefaultArtefact, Body: body}},
	})
	if rerr != nil {
		return fmt.Errorf("app: emit dogfood receipt: %w", rerr)
	}
	return nil
}

// dogfoodTranscript exports the session transcript as artefact body.
func dogfoodTranscript(ctx context.Context, fl *flags, id string) ([]byte, error) {
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

// dogfoodCommitSHA resolves the 40-char commit sha from env.
func dogfoodCommitSHA() (string, error) {
	if sha := os.Getenv("FLOWSTATE_COMMIT_SHA"); len(sha) == 40 {
		return sha, nil
	}
	return "", fmt.Errorf("%w: FLOWSTATE_COMMIT_SHA must be a 40-char sha", app.ErrConfigInvalid)
}

// dogfoodAuthor returns the attribution author identity from env.
func dogfoodAuthor() string {
	if a := os.Getenv("FLOWSTATE_AUTHOR_IDENTITY"); a != "" {
		return a
	}
	return "git:config"
}

// dogfoodDefaultBudget is the hard cap applied when --budget is not
// supplied: a live turn may never run unlimited (M1-DOGFOOD).
const dogfoodDefaultBudget = 20000

// liveGate reports whether both live gates are open: a provider API
// key is present AND the operator opted in via FLOWSTATE_ALLOW_LIVE=1
// (CI has no key so the live path is unreachable there).
func liveGate() bool {
	return os.Getenv("ZAI_API_KEY") != "" && os.Getenv("FLOWSTATE_ALLOW_LIVE") == "1"
}

// cmdDogfoodRun implements `flowstate dogfood run`: one budget-capped
// provider turn via the session-run path, then a metadata attempt
// receipt. Exit 0 done (5 budget stop); 2 usage; 4 gates closed or
// database failure; 3 provider error.
func cmdDogfoodRun(ctx context.Context, st *app.Store, fl *flags, stdout, stderr *os.File) int {
	if fl.task == "" {
		writeError(stderr, fmt.Errorf("%w: --task is required", app.ErrUsage))
		return exitUsage
	}
	if fl.budget < 0 {
		writeError(stderr, fmt.Errorf("%w: --budget must not be negative", app.ErrUsage))
		return exitUsage
	}
	if !liveGate() {
		writeError(stderr, fmt.Errorf("%w: dogfood run requires both ZAI_API_KEY and FLOWSTATE_ALLOW_LIVE=1", app.ErrConfigInvalid))
		return exitConfigInvalid
	}
	return runDogfoodTurn(ctx, st, fl, stdout, stderr)
}

// dogfoodCreate creates the dogfood session via the standard
// session-create path.
func dogfoodCreate(ctx context.Context, st *app.Store, task string) (*app.Session, error) {
	return app.CreateSelfTestSession(ctx, st, dogfoodProvider, dogfoodModel, task)
}

// runDogfoodTurn creates the session, applies the default budget cap
// when none was supplied, runs the turn and emits the receipt.
func runDogfoodTurn(ctx context.Context, st *app.Store, fl *flags, stdout, stderr *os.File) int {
	task, err := readTask(fl.task)
	if err != nil {
		writeError(stderr, err)
		return exitUsage
	}
	runFl := *fl
	if runFl.budget == 0 {
		runFl.budget = dogfoodDefaultBudget
	}
	sess, err := dogfoodCreate(ctx, st, task)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if err := writeJSON(stdout, map[string]any{"id": sess.ID, "status": sess.Status}); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return finishDogfoodRun(ctx, st, sess.ID, &runFl, stdout, stderr)
}
