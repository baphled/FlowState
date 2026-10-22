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
