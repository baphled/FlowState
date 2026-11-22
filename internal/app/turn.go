// Package app implements the application turn loop (M1-SESSION): one
// provider turn streamed through the provider port and persisted as
// ordered attributable events via the session store's single writer.
// It owns session lifecycle and event ordering only; provider
// transport details stay in the adapters.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/registry"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// ErrTerminal aliases the session terminal sentinel for cmd use.
var ErrTerminal = session.ErrTerminal

// ResolveProvider is the registry-backed default resolver.
var ResolveProvider ProviderResolver = registry.Resolve

// OpenStore opens (creating parent dirs) the SQLite database.
func OpenStore(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, err
		}
	}
	return store.Open(path)
}

// payloadTask extracts the task text from a stored turn.start
// payload without failing on unknown shapes.
func payloadTask(payloadJSON string) string {
	var p struct {
		Task string `json:"task"`
	}
	_ = json.Unmarshal([]byte(payloadJSON), &p)
	return p.Task
}

// Turn runs one provider turn for sess: it appends the user message
// event, streams the provider response appending one delta event per
// chunk, then the final assistant event and turn.end, and marks the
// session completed. A provider error appends a terminal typed
// provider.error event, marks the session failed and returns the
// error; there are no retries (M1-PROVIDER failure path).
func Turn(ctx context.Context, st *store.Store, sess *store.Session, p provider.Streamer) (*TurnResult, error) {
	return TurnWithOptions(ctx, st, sess, p, TurnOptions{})
}

// TurnWithOptions runs one provider turn like Turn, honouring opts.
// When opts.Budget > 0 and the accumulated streamed delta content
// length reaches the budget, the turn stops consuming the stream
// without appending assistant.message or turn.end, pauses the session
// via the store writer (session.paused with reason "budget_exceeded"
// and status paused) and returns a TurnResult with BudgetExceeded set
// and a nil error.
func TurnWithOptions(ctx context.Context, st *store.Store, sess *store.Session, p provider.Streamer, opts TurnOptions) (*TurnResult, error) {
	if sess == nil {
		return nil, fmt.Errorf("app: session must not be nil")
	}
	if p == nil {
		return nil, fmt.Errorf("app: provider must not be nil")
	}
	events, err := st.Replay(ctx, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("load session log: %w", err)
	}
	task := taskFromEvents(events)
	w, err := st.NewEventWriter(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	if _, err := w.Append(ctx, session.ActorUser, TypeUserMessage, map[string]any{"content": task}); err != nil {
		return nil, err
	}
	s := &turnState{}
	model := sess.ModelID
	if opts.ModelID != "" {
		model = opts.ModelID
	}
	for ev := range p.Stream(ctx, &provider.ChatRequest{
		Messages:  []provider.Message{{Role: "user", Content: task}},
		Model:     model,
		MaxTokens: opts.MaxTokens,
	}) {
		if stop := consumeStreamEvent(ctx, w, st, sess, opts, ev, s); stop {
			break
		}
	}
	if s.budgetExceeded {
		return pauseOnBudget(ctx, w, sess, s)
	}
	if ctx.Err() != nil {
		return nil, cancelOnInterrupt(ctx, w, st, sess)
	}
	if ferr := turnFailure(s); ferr != nil {
		return nil, ferr
	}
	if _, err := w.Append(ctx, session.ActorEngine, TypeAssistantMessage, map[string]any{"content": s.content}); err != nil {
		return nil, err
	}
	if _, err := w.Append(ctx, session.ActorEngine, TypeTurnEnd, turnEndPayload(s.finishReason)); err != nil {
		return nil, err
	}
	if err := st.UpdateSessionStatus(ctx, sess.ID, "completed"); err != nil {
		return nil, fmt.Errorf("mark session completed: %w", err)
	}
	return &TurnResult{SessionID: sess.ID, FinalContent: s.content, FinishReason: s.finishReason}, nil
}

// ReasonCancelled is recorded when the cancellation carries no known cause.
const ReasonCancelled = "cancelled"

// Error implements error.
func (c *CancelCause) Error() string { return "cancelled: " + c.Reason }

// CancelReason returns the reason the cause records.
func (c *CancelCause) CancelReason() string { return c.Reason }

// SignalCause is the cancellation cause for sig: sigint, sigterm or
// sighup; any other signal is the neutral ReasonCancelled.
func SignalCause(sig os.Signal) error {
	switch sig {
	case os.Interrupt:
		return &CancelCause{Reason: "sigint"}
	case syscall.SIGTERM:
		return &CancelCause{Reason: "sigterm"}
	case syscall.SIGHUP:
		return &CancelCause{Reason: "sighup"}
	}
	return &CancelCause{Reason: ReasonCancelled}
}

// CancelReason reports why ctx was cancelled, from its cancellation
// cause; a cancellation with no named cause is ReasonCancelled. Read it
// before detaching the context, as WithoutCancel drops the cause.
func CancelReason(ctx context.Context) string {
	var r reasonedCause
	if errors.As(context.Cause(ctx), &r) {
		return r.CancelReason()
	}
	return ReasonCancelled
}

// cancelOnInterrupt cancels the session cleanly on an interrupt, SIGTERM,
// SIGHUP or a server-side cancellation, recording which one (CancelReason)
// so the CLI can exit 130 without partial silent writes; a detached context
// completes the cancellation writes even though the parent is
// cancelled. The event is appended before the status, as on every
// other terminal path, so a reader that observes the terminal status
// always finds its final event already committed.
func cancelOnInterrupt(ctx context.Context, w *store.EventWriter, st *store.Store, sess *store.Session) error {
	reason := CancelReason(ctx)
	cctx := context.WithoutCancel(ctx)
	if _, aerr := w.Append(cctx, session.ActorEngine, session.TypeSessionCancelled,
		map[string]any{"status": "cancelled", "reason": reason}); aerr != nil {
		return aerr
	}
	if serr := st.UpdateSessionStatus(cctx, sess.ID, "cancelled"); serr != nil {
		return fmt.Errorf("mark session cancelled: %w", serr)
	}
	return ErrSessionCancelled
}

// taskFromEvents extracts the original task from the replayed
// turn.start event, if present.
func taskFromEvents(events []store.RecordedEvent) string {
	for _, e := range events {
		if e.Type == session.TypeTurnStart {
			return payloadTask(e.PayloadJSON)
		}
	}
	return ""
}

// consumeStreamEvent appends one stream event to the log and updates
// s; it reports whether the stream loop should stop.
func consumeStreamEvent(ctx context.Context, w *store.EventWriter, st *store.Store, sess *store.Session, opts TurnOptions, ev provider.StreamEvent, s *turnState) bool {
	if ev.Err != nil {
		recordProviderFailure(ctx, w, st, sess, ev.Err, s)
		return true
	}
	if ev.Kind == provider.StreamEventFinish {
		s.finishReason = ev.FinishReason
		return false
	}
	if _, derr := w.Append(ctx, session.ActorEngine, TypeAssistantDelta, map[string]any{"delta": ev.Content}); derr != nil {
		s.err = derr
		return true
	}
	s.content += ev.Content
	s.consumed += int64(len(ev.Content))
	if opts.Budget > 0 && s.consumed >= opts.Budget {
		s.budgetExceeded = true
		return true
	}
	return false
}

// pauseOnBudget pauses the session on the store writer after a budget
// stop and returns the budget-exceeded turn result.
func pauseOnBudget(ctx context.Context, w *store.EventWriter, sess *store.Session, s *turnState) (*TurnResult, error) {
	if perr := w.Pause(ctx, store.PauseInfo{
		Reason:         "budget_exceeded",
		ConsumedBudget: s.consumed,
		NextSafeOp:     "session run " + sess.ID,
	}); perr != nil {
		return nil, perr
	}
	return &TurnResult{SessionID: sess.ID, FinalContent: s.content, BudgetExceeded: true, ConsumedBudget: s.consumed}, nil
}

// recordProviderFailure appends the terminal provider.error event and
// then marks the session failed, leaving the status untouched when the
// append fails. The provider's cause is kept in s.cause; any
// persistence failure is kept in s.err.
func recordProviderFailure(ctx context.Context, w *store.EventWriter, st *store.Store, sess *store.Session, cause error, s *turnState) {
	s.streamErr = true
	s.content = ""
	s.cause = cause
	if _, aerr := w.Append(ctx, session.ActorEngine, TypeProviderError, map[string]any{
		"error":    cause.Error(),
		"terminal": true,
	}); aerr != nil {
		s.err = fmt.Errorf("record provider error: %w", aerr)
		return
	}
	if uerr := st.UpdateSessionStatus(ctx, sess.ID, "failed"); uerr != nil {
		s.err = fmt.Errorf("mark session failed: %w", uerr)
	}
}

// turnFailure maps a finished stream's state onto the turn's error, or
// nil when the stream ended cleanly. A provider failure always wraps
// ErrProvider and the provider's cause, plus the persistence failure
// when recording it failed; a bare persistence failure is returned as
// is.
func turnFailure(s *turnState) error {
	switch {
	case s.streamErr && s.err != nil:
		return fmt.Errorf("%w: %w: %w", ErrProvider, s.cause, s.err)
	case s.streamErr:
		return fmt.Errorf("%w: %w", ErrProvider, s.cause)
	default:
		return s.err
	}
}

// turnEndPayload builds the terminal turn.end payload; finish_reason
// appears only when the provider reported one.
func turnEndPayload(finishReason string) map[string]any {
	payload := map[string]any{"status": "completed"}
	if finishReason != "" {
		payload["finish_reason"] = finishReason
	}
	return payload
}

// ErrProvider marks terminal provider failures surfaced by the turn
// loop (code provider_error, exit 3 per CLI-AND-API-CONTRACT §1).
var ErrProvider = fmt.Errorf("provider_error")
