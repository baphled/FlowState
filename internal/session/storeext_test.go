package session

import (
	"context"
	"errors"
	"testing"

	"github.com/baphled/flowstate/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestGivenNoSessionsWhenListThenEmptySlice(t *testing.T) {
	st := newStore(t)
	got, err := List(context.Background(), st, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d sessions, want 0", len(got))
	}
}

func TestGivenCreatedSessionWhenListThenRowReturned(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	created, err := Create(ctx, st, "prov", "model-x", DefaultHarness, "deadbeef", "do the thing")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := List(ctx, st, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != created.ID {
		t.Fatalf("got %+v, want one session %s", got, created.ID)
	}
}

func TestGivenMixedStatusesWhenListWithStatusFilterThenOnlyMatching(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	a, _ := Create(ctx, st, "p", "m", DefaultHarness, "sha", "t1")
	b, _ := Create(ctx, st, "p", "m", DefaultHarness, "sha", "t2")
	if _, err := Close(ctx, st, b.ID); err != nil {
		t.Fatalf("Close b: %v", err)
	}
	got, err := List(ctx, st, "cancelled")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != a.ID && got[0].ID != b.ID {
		t.Fatalf("filter mismatch: %+v", got)
	}
	if got[0].ID != b.ID {
		t.Fatalf("got %s, want closed session %s", got[0].ID, b.ID)
	}
	_ = a
}

func TestGivenInvalidStatusFilterWhenListThenUsageError(t *testing.T) {
	st := newStore(t)
	if _, err := List(context.Background(), st, "banana"); !errors.Is(err, ErrUsage) {
		t.Fatalf("got %v, want ErrUsage", err)
	}
}

func TestGivenCreateWhenRecordPersistedThenTurnStartEventAndActiveStatus(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	created, err := Create(ctx, st, "prov", "model", DefaultHarness, "sha1", "hello task")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != "active" {
		t.Fatalf("status %q, want active", created.Status)
	}
	events, err := st.Replay(ctx, created.ID)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) != 1 || events[0].Type != "turn.start" || events[0].Actor != "user" || events[0].Seq != 0 {
		t.Fatalf("events %+v, want single user turn.start at seq 0", events)
	}
}

func TestGivenEmptyTaskWhenCreateThenUsageError(t *testing.T) {
	st := newStore(t)
	if _, err := Create(context.Background(), st, "p", "m", DefaultHarness, "sha", ""); !errors.Is(err, ErrUsage) {
		t.Fatalf("got %v, want ErrUsage", err)
	}
}

func TestGivenEmptyProviderOrModelWhenCreateThenUsageError(t *testing.T) {
	st := newStore(t)
	if _, err := Create(context.Background(), st, "", "m", DefaultHarness, "sha", "t"); !errors.Is(err, ErrUsage) {
		t.Fatalf("provider: got %v, want ErrUsage", err)
	}
	if _, err := Create(context.Background(), st, "p", "", DefaultHarness, "sha", "t"); !errors.Is(err, ErrUsage) {
		t.Fatalf("model: got %v, want ErrUsage", err)
	}
}

func TestGivenActiveSessionWhenCloseThenCancelledWithEvent(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	created, _ := Create(ctx, st, "p", "m", DefaultHarness, "sha", "t")
	closed, err := Close(ctx, st, created.ID)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closed.Status != "cancelled" {
		t.Fatalf("status %q, want cancelled", closed.Status)
	}
	events, err := st.Replay(ctx, created.ID)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	last := events[len(events)-1]
	if last.Type != "session.cancelled" {
		t.Fatalf("last event %q, want session.cancelled", last.Type)
	}
}

func TestGivenClosedSessionWhenCloseAgainThenTerminalErrorAndNoNewEvent(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	created, _ := Create(ctx, st, "p", "m", DefaultHarness, "sha", "t")
	if _, err := Close(ctx, st, created.ID); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if _, err := Close(ctx, st, created.ID); !errors.Is(err, ErrTerminal) {
		t.Fatalf("second Close: got %v, want ErrTerminal", err)
	}
	events, _ := st.Replay(ctx, created.ID)
	// turn.start + session.cancelled only
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
}

func TestGivenUnknownSessionWhenCloseThenUnknownSessionError(t *testing.T) {
	st := newStore(t)
	_, err := Close(context.Background(), st, "nope")
	if !errors.Is(err, store.ErrUnknownSession) {
		t.Fatalf("got %v, want ErrUnknownSession", err)
	}
}
