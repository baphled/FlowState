package session

import (
	"strings"
	"testing"
)

func newTestSession(t *testing.T) *Session {
	t.Helper()
	s, err := New("sess-1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestNewRejectsEmptyID(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestTurnLifecycleOrderedEvents(t *testing.T) {
	s := newTestSession(t)
	turn, err := s.StartTurn("turn-1")
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if err := s.RecordToolResult(turn, "tool=shell output=ok"); err != nil {
		t.Fatalf("RecordToolResult: %v", err)
	}
	if err := s.Complete(turn, "result=done"); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	events := s.Events()
	wantKinds := []EventKind{EventTurnAdmitted, EventToolResult, EventTurnComplete}
	if len(events) != len(wantKinds) {
		t.Fatalf("got %d events, want %d", len(events), len(wantKinds))
	}
	var last uint64
	for i, e := range events {
		if e.Kind != wantKinds[i] {
			t.Errorf("event %d kind = %q, want %q", i, e.Kind, wantKinds[i])
		}
		if e.Seq <= last {
			t.Errorf("event %d seq %d not greater than %d", i, e.Seq, last)
		}
		last = e.Seq
	}
}

func TestCancelIsTerminal(t *testing.T) {
	s := newTestSession(t)
	turn, _ := s.StartTurn("turn-1")
	if err := s.Cancel(turn); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := s.RecordToolResult(turn, "late"); err == nil {
		t.Fatal("expected error recording into cancelled turn")
	}
	if err := s.Complete(turn, "late"); err == nil {
		t.Fatal("expected error completing cancelled turn")
	}
	if k := s.Events()[len(s.Events())-1].Kind; k != EventTurnCancelled {
		t.Errorf("last event kind = %q, want %q", k, EventTurnCancelled)
	}
}

func TestDuplicateTurnIDRejected(t *testing.T) {
	s := newTestSession(t)
	if _, err := s.StartTurn("dup"); err != nil {
		t.Fatalf("first StartTurn: %v", err)
	}
	_, err := s.StartTurn("dup")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

// --- regression tests for the PR #19 review findings ---

func TestForeignTurnRejected(t *testing.T) {
	s1 := newTestSession(t)
	s2 := newTestSession(t)
	// s2 reuses the same session id as s1 but is a distinct instance.
	turn1, err := s1.StartTurn("turn-1")
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	foreign, err := s2.StartTurn("turn-foreign")
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if err := s2.RecordToolResult(turn1, "hijack"); err == nil {
		t.Fatal("expected error recording into a turn owned by another session")
	}
	if err := s2.Complete(turn1, "hijack"); err == nil {
		t.Fatal("expected error completing a turn owned by another session")
	}
	if err := s1.RecordToolResult(foreign, "hijack"); err == nil {
		t.Fatal("expected error recording into a turn owned by another session")
	}
	// Neither session's event log may contain cross-session writes.
	for _, e := range s1.Events() {
		if strings.Contains(e.Payload, "hijack") {
			t.Errorf("session 1 log contains foreign write: %+v", e)
		}
	}
	for _, e := range s2.Events() {
		if strings.Contains(e.Payload, "hijack") {
			t.Errorf("session 2 log contains foreign write: %+v", e)
		}
	}
}

func TestTurnEventsSnapshotImmutable(t *testing.T) {
	s := newTestSession(t)
	turn, _ := s.StartTurn("turn-1")
	snap := turn.Events()
	if len(snap) != 1 {
		t.Fatalf("snapshot length = %d, want 1", len(snap))
	}
	snap[0].Payload = "tampered"
	if got := turn.Events()[0].Payload; got == "tampered" {
		t.Fatal("mutating snapshot leaked into turn state")
	}
	// Session-level snapshot must also be a copy.
	ssnap := s.Events()
	ssnap[0].Payload = "tampered"
	if got := s.Events()[0].Payload; got == "tampered" {
		t.Fatal("mutating session snapshot leaked into session state")
	}
}

func TestEventsGloballySeqOrdered(t *testing.T) {
	s := newTestSession(t)
	a, _ := s.StartTurn("turn-a")
	b, _ := s.StartTurn("turn-b")
	steps := []struct {
		turn *Turn
	}{
		{a}, {b}, {a}, {b}, {a},
	}
	for _, st := range steps {
		if err := s.RecordToolResult(st.turn, "tool step"); err != nil {
			t.Fatalf("RecordToolResult: %v", err)
		}
	}
	if err := s.Complete(b, "b done"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	events := s.Events()
	if len(events) == 0 {
		t.Fatal("no events recorded")
	}
	var last uint64
	for i, e := range events {
		if e.Seq <= last {
			t.Errorf("event %d seq %d not greater than %d (global order violated)", i, e.Seq, last)
		}
		last = e.Seq
	}
	if k := events[len(events)-1].Kind; k != EventTurnComplete {
		t.Errorf("last event kind = %q, want %q", k, EventTurnComplete)
	}
}
