package internalapp

import (
	"context"
	"testing"

	"github.com/baphled/flowstate/internal/store"
)

// Given the concrete store adapter, when it is wired as both ports,
// then session CRUD and event append/replay round-trip through the
// interfaces (BDD: ports behave identically to the concrete store).
func TestGivenStoreWhenWiredAsPortsThenBehaviourMatchesConcreteStore(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	defer st.Close()

	var repo SessionRepo = st
	var log EventLog = st
	ctx := context.Background()

	created, err := repo.CreateSession(ctx, "sid-1", "p", "m", "h", "sha")
	if err != nil {
		t.Fatalf("create via port: %v", err)
	}
	if created.Status != "active" {
		t.Fatalf("status = %q, want active", created.Status)
	}

	w, err := log.NewEventWriter(ctx, "sid-1")
	if err != nil {
		t.Fatalf("new writer via port: %v", err)
	}
	var wPort EventWriter = w
	if _, err := wPort.Append(ctx, "user", "turn.start", map[string]any{"task": "t"}); err != nil {
		t.Fatalf("append via port: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer via port: %v", err)
	}

	events, err := log.Replay(ctx, "sid-1")
	if err != nil {
		t.Fatalf("replay via port: %v", err)
	}
	if len(events) != 1 || events[0].Type != "turn.start" {
		t.Fatalf("replayed = %+v", events)
	}

	if err := repo.UpdateSessionStatus(ctx, "sid-1", "completed"); err != nil {
		t.Fatalf("update status via port: %v", err)
	}
	got, err := repo.GetSession(ctx, "sid-1")
	if err != nil || got.Status != "completed" {
		t.Fatalf("get via port = %+v, %v", got, err)
	}
	listed, err := repo.ListSessions(ctx, "completed")
	if err != nil || len(listed) != 1 {
		t.Fatalf("list via port = %+v, %v", listed, err)
	}
}
