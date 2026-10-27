package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func newStoreForTerminal(t *testing.T) *Store {
	t.Helper()
	st, err := OpenMemory()
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func seedSession(t *testing.T, st *Store) string {
	t.Helper()
	sess, err := st.CreateSession(context.Background(), "0199aaaa-0000-7000-8000-000000000001",
		"zai", "glm-4.6", "flowstate-cli", "cfgsha")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return sess.ID
}

// Given an active session
// When it is cancelled
// Then the status becomes cancelled and exactly one session.cancelled
// event is appended in the same logical transition.
func TestCancelActiveSession_RecordsSingleCancelledEvent(t *testing.T) {
	st := newStoreForTerminal(t)
	id := seedSession(t, st)

	sess, err := st.CancelSession(context.Background(), id, "user requested")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if sess.Status != "cancelled" {
		t.Fatalf("status %q, want cancelled", sess.Status)
	}
	got, err := st.GetSession(context.Background(), id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != "cancelled" {
		t.Fatalf("persisted status %q, want cancelled", got.Status)
	}
	events, err := st.Replay(context.Background(), id)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count %d, want 1", len(events))
	}
	if events[0].Type != "session.cancelled" {
		t.Fatalf("event type %q, want session.cancelled", events[0].Type)
	}
	if events[0].CreatedAt.IsZero() {
		t.Fatal("event timestamp not recorded")
	}
}

// Given an unknown session id
// When it is cancelled
// Then the call fails with ErrUnknownSession and nothing is written.
func TestCancelUnknownSession_FailsTyped(t *testing.T) {
	st := newStoreForTerminal(t)
	if _, err := st.CancelSession(context.Background(), "nope", ""); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("err %v, want ErrUnknownSession", err)
	}
}

// Given a cancelled session
// When it is cancelled again or completed
// Then every transition fails with ErrSessionTerminal and the event
// log gains no further terminal event (terminal-once).
func TestTerminalOnce_RejectsAllFurtherTransitions(t *testing.T) {
	st := newStoreForTerminal(t)
	id := seedSession(t, st)
	if _, err := st.CancelSession(context.Background(), id, ""); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	if _, err := st.CancelSession(context.Background(), id, ""); !errors.Is(err, ErrSessionTerminal) {
		t.Fatalf("second cancel err %v, want ErrSessionTerminal", err)
	}
	if _, err := st.CompleteSession(context.Background(), id, nil); !errors.Is(err, ErrSessionTerminal) {
		t.Fatalf("complete-after-cancel err %v, want ErrSessionTerminal", err)
	}
	events, err := st.Replay(context.Background(), id)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count %d, want exactly one terminal event", len(events))
	}
}

// Given a completed session
// When it is cancelled
// Then the transition fails with ErrSessionTerminal (close-then-cancel).
func TestCompletedSession_RejectsCancel(t *testing.T) {
	st := newStoreForTerminal(t)
	id := seedSession(t, st)
	if _, err := st.CompleteSession(context.Background(), id, "done"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := st.CancelSession(context.Background(), id, ""); !errors.Is(err, ErrSessionTerminal) {
		t.Fatalf("err %v, want ErrSessionTerminal", err)
	}
}

// Given a paused session
// When it is cancelled
// Then the cancel succeeds (cancel is allowed from active or paused,
// DATA-MODEL §7).
func TestPausedSession_CancelSucceeds(t *testing.T) {
	st := newStoreForTerminal(t)
	id := seedSession(t, st)
	ctx := context.Background()
	w, err := st.NewEventWriter(ctx, id)
	if err != nil {
		t.Fatalf("writer: %v", err)
	}
	if err := w.PauseBudget(ctx, map[string]any{"reason": "cap"}); err != nil {
		t.Fatalf("pause: %v", err)
	}
	w.Close()
	sess, err := st.CancelSession(ctx, id, "user requested")
	if err != nil {
		t.Fatalf("cancel paused: %v", err)
	}
	if sess.Status != "cancelled" {
		t.Fatalf("status %q, want cancelled", sess.Status)
	}
	events, _ := st.Replay(ctx, id)
	if events[len(events)-1].Type != "session.cancelled" {
		t.Fatalf("last event %q, want session.cancelled", events[len(events)-1].Type)
	}
}

// Given a session being cancelled concurrently by many callers
// When all try to cancel at once
// Then exactly one caller succeeds and the event log holds exactly
// one session.cancelled event (no duplicate terminal events).
func TestConcurrentCancels_ExactlyOneTerminalEvent(t *testing.T) {
	st := newStoreForTerminal(t)
	id := seedSession(t, st)
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = st.CancelSession(context.Background(), id, "race")
		}(i)
	}
	close(start)
	wg.Wait()
	successes, terminals := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrSessionTerminal):
			terminals++
		default:
			t.Fatalf("unexpected err %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes %d, want 1", successes)
	}
	if terminals != n-1 {
		t.Fatalf("terminal rejections %d, want %d", terminals, n-1)
	}
	events, err := st.Replay(context.Background(), id)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	cancelled := 0
	for _, e := range events {
		if e.Type == "session.cancelled" {
			cancelled++
		}
	}
	if cancelled != 1 {
		t.Fatalf("session.cancelled count %d, want 1", cancelled)
	}
}

// Given an already-canceled context
// When a cancel is attempted
// Then the transition fails without writing any event or status
// change (ctx cancel mid-append leaves no partial writes).
func TestCanceledContext_WritesNothing(t *testing.T) {
	st := newStoreForTerminal(t)
	id := seedSession(t, st)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.CancelSession(ctx, id, ""); err == nil {
		t.Fatal("cancel with dead ctx: want error, got nil")
	}
	got, err := st.GetSession(context.Background(), id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != "active" {
		t.Fatalf("status %q, want active (no partial write)", got.Status)
	}
	events, err := st.Replay(context.Background(), id)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("event count %d, want 0 (no partial writes)", len(events))
	}
}

// Given an active session
// When it is completed
// Then exactly one terminal turn.end event is recorded and the
// session is completed exactly once.
func TestCompleteSession_TerminalExactlyOnce(t *testing.T) {
	st := newStoreForTerminal(t)
	id := seedSession(t, st)
	ctx := context.Background()
	if _, err := st.CompleteSession(ctx, id, "ok"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := st.CompleteSession(ctx, id, "again"); !errors.Is(err, ErrSessionTerminal) {
		t.Fatalf("second complete err %v, want ErrSessionTerminal", err)
	}
	events, err := st.Replay(ctx, id)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(events) != 1 || events[0].Type != "turn.end" {
		t.Fatalf("events %+v, want single turn.end", events)
	}
}
