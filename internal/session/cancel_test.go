package session

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/baphled/flowstate/internal/store"
)

func newTerminalStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func seedForCancel(t *testing.T, st *store.Store) string {
	t.Helper()
	sess, err := Create(context.Background(), st, "zai", "glm-4.6", "", "", "task")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return sess.ID
}

// Given an active session
// When Cancel is called with a live context
// Then the session becomes cancelled with one session.cancelled
// event carrying the reason.
func TestCancel_LiveContext_PersistsCancelled(t *testing.T) {
	st := newTerminalStore(t)
	id := seedForCancel(t, st)

	if err := Cancel(context.Background(), st, id, "user requested"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	sess, err := st.GetSession(context.Background(), id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if sess.Status != "cancelled" {
		t.Fatalf("status %q, want cancelled", sess.Status)
	}
	events, err := st.Replay(context.Background(), id)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if events[len(events)-1].Type != TypeSessionCancelled {
		t.Fatalf("last event %q, want session.cancelled", events[len(events)-1].Type)
	}
}

// Given a turn in flight (context cancelled mid-turn)
// When Cancel observes the dead context
// Then the cancellation is durably persisted and Cancel returns
// ErrContextCancelled (mapped from the interrupted turn).
func TestCancel_MidTurnContext_PersistsAndReturnsTypedError(t *testing.T) {
	st := newTerminalStore(t)
	id := seedForCancel(t, st)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the interrupt happens first

	err := Cancel(ctx, st, id, "")
	if !errors.Is(err, ErrContextCancelled) {
		t.Fatalf("err %v, want ErrContextCancelled", err)
	}
	sess, err := st.GetSession(context.Background(), id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if sess.Status != "cancelled" {
		t.Fatalf("status %q, want cancelled", sess.Status)
	}
}

// Given a context that dies mid-append (the write itself is aborted)
// When Cancel is called
// Then no terminal event and no status change are committed (no
// partial writes), and the error does not masquerade as success.
func TestCancel_MidAppendAborted_NoPartialWrite(t *testing.T) {
	st := newTerminalStore(t)
	id := seedForCancel(t, st)
	// Cancel after the persist has begun: with an already-dead context
	// the SQL transaction fails before commit.
	dead, dcancel := context.WithCancel(context.Background())
	dcancel()
	if err := persistCancel(dead, st, id, "reason"); err == nil {
		t.Fatal("want error when the write context is dead")
	}
	sess, err := st.GetSession(context.Background(), id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if sess.Status != "active" {
		t.Fatalf("status %q, want active (no partial write)", sess.Status)
	}
	events, err := st.Replay(context.Background(), id)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	term := 0
	for _, e := range events {
		if e.Type == TypeSessionCancelled {
			term++
		}
	}
	if term != 0 {
		t.Fatalf("terminal events %d, want 0", term)
	}
}

// Given a cancelled session
// When Cancel or Close is called again
// Then both fail with ErrTerminal and no duplicate terminal event is
// written (cancellation is safe: idempotent rejection, not a second
// event).
func TestCancel_IdempotentSafe_AlreadyTerminalFailsTyped(t *testing.T) {
	st := newTerminalStore(t)
	id := seedForCancel(t, st)
	ctx := context.Background()
	if err := Cancel(ctx, st, id, ""); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	if err := Cancel(ctx, st, id, ""); !errors.Is(err, ErrTerminal) {
		t.Fatalf("second cancel err %v, want ErrTerminal", err)
	}
	if _, err := Close(ctx, st, id); !errors.Is(err, ErrTerminal) {
		t.Fatalf("close-after-cancel err %v, want ErrTerminal", err)
	}
	events, err := st.Replay(ctx, id)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	term := 0
	for _, e := range events {
		if e.Type == TypeSessionCancelled {
			term++
		}
	}
	if term != 1 {
		t.Fatalf("session.cancelled count %d, want 1", term)
	}
}

// Given an unknown session
// When Cancel is called
// Then it fails with store.ErrUnknownSession.
func TestCancel_UnknownSession_FailsTyped(t *testing.T) {
	st := newTerminalStore(t)
	if err := Cancel(context.Background(), st, "ghost", ""); !errors.Is(err, store.ErrUnknownSession) {
		t.Fatalf("err %v, want ErrUnknownSession", err)
	}
}

// Given an active session
// When many goroutines cancel concurrently (SIGINT plus command)
// Then exactly one terminal session.cancelled event is committed.
func TestCancel_Concurrent_ExactlyOneTerminalEvent(t *testing.T) {
	st := newTerminalStore(t)
	id := seedForCancel(t, st)
	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = Cancel(context.Background(), st, id, "concurrent")
		}(i)
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrTerminal):
		default:
			t.Fatalf("unexpected err %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("successful cancels %d, want 1", ok)
	}
	events, err := st.Replay(context.Background(), id)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	term := 0
	for _, e := range events {
		if e.Type == TypeSessionCancelled {
			term++
		}
	}
	if term != 1 {
		t.Fatalf("session.cancelled count %d, want 1", term)
	}
}

// Given a completed session (closed with a terminal turn.end via
// Complete)
// When Cancel is attempted (close-then-cancel)
// Then it fails with ErrTerminal.
func TestCancel_AfterComplete_FailsTyped(t *testing.T) {
	st := newTerminalStore(t)
	id := seedForCancel(t, st)
	ctx := context.Background()
	if _, err := st.CompleteSession(ctx, id, "done"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := Cancel(ctx, st, id, ""); !errors.Is(err, ErrTerminal) {
		t.Fatalf("err %v, want ErrTerminal", err)
	}
}
