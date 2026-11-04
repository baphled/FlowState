package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// openT45 opens a migrated temp database for the acceptance 4/5 tests
// (kept independent of the Ginkgo suite's openT).
func openT45(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, _, err := Open(filepath.Join(t.TempDir(), "t45.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db, t.TempDir()
}

// TestWriterCancelMidTurn proves acceptance item 4: cancelling mid-turn
// atomically commits a cancel.checkpoint (recording last committed seq) and
// the terminal session.cancelled event, marks the session cancelled, and
// rejects all late effects afterwards.
func TestWriterCancelMidTurn(t *testing.T) {
	db, _ := openT45(t)
	w, sess, err := NewEventWriter(db, Session{Provider: "p", ModelID: "m", Harness: "t"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Half a turn: 3 committed effects (seq 0..2), then a mid-turn cancel.
	for i := 0; i < 3; i++ {
		if err := w.Append(ctx, EventIn{Actor: "provider:fake", Type: "provider.delta",
			Payload: map[string]any{"i": i}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := VerifyContiguous(ctx, db, sess.ID); err != nil {
		t.Fatalf("pre-cancel contiguity: %v", err)
	}
	if err := w.Cancel(ctx, "user abort", 2); err != nil {
		t.Fatal(err)
	}
	// Session is terminal and cancelled in the sessions table.
	var status string
	if err := db.QueryRow(`SELECT status FROM sessions WHERE id=?`, sess.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", status)
	}
	// Late effects rejected: Append, Pause, Resume, second Cancel.
	if err := w.Append(ctx, EventIn{Actor: "provider:fake", Type: "provider.delta"}); err == nil {
		t.Fatal("late append accepted after cancel")
	}
	if err := w.Pause(ctx, PauseInfo{Reason: "late"}); err == nil {
		t.Fatal("late pause accepted after cancel")
	}
	if _, err := w.Resume(ctx, ""); err == nil {
		t.Fatal("late resume accepted after cancel")
	}
	if err := w.Cancel(ctx, "again", 0); err == nil {
		t.Fatal("second cancel accepted")
	}
	// Checkpoint + terminal event committed contiguously after the effects.
	if err := VerifyContiguous(ctx, db, sess.ID); err != nil {
		t.Fatalf("post-cancel contiguity: %v", err)
	}
	rows, err := db.Query(`SELECT seq, type FROM events WHERE session_id=? ORDER BY seq`, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var types []string
	for rows.Next() {
		var seq int
		var typ string
		if err := rows.Scan(&seq, &typ); err != nil {
			t.Fatal(err)
		}
		types = append(types, typ)
	}
	if len(types) != 5 {
		t.Fatalf("events = %d, want 5 (3 effects + checkpoint + cancelled)", len(types))
	}
	if types[3] != "cancel.checkpoint" || types[4] != "session.cancelled" {
		t.Fatalf("terminal events = %v", types[3:])
	}
}

// TestWriterDurablePauseResume proves acceptance item 5: Pause durably
// records the pause checkpoint (status + session.paused event), Append is
// rejected while paused, and a fresh writer re-attached via AttachWriter
// resumes from the persisted high-water mark with a contiguous sequence.
func TestWriterDurablePauseResume(t *testing.T) {
	db, _ := openT45(t)
	w, sess, err := NewEventWriter(db, Session{Provider: "p", ModelID: "m", Harness: "t"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if err := w.Append(ctx, EventIn{Actor: "provider:fake", Type: "provider.delta",
			Payload: map[string]any{"i": i}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Pause(ctx, PauseInfo{Reason: "budget exhausted", ConsumedBudget: 4096,
		UncertainTool: "shell"}); err != nil {
		t.Fatal(err)
	}
	// Durably paused: status persisted, appends rejected.
	var status string
	if err := db.QueryRow(`SELECT status FROM sessions WHERE id=?`, sess.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "paused" {
		t.Fatalf("status = %q, want paused", status)
	}
	if err := w.Append(ctx, EventIn{Actor: "provider:fake", Type: "provider.delta"}); err == nil {
		t.Fatal("append accepted while paused")
	}
	// Simulate process restart: attach a fresh writer to the same DB.
	w2, err := AttachWriter(ctx, db, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w2.Resume(ctx, "shell"); err != nil {
		t.Fatal(err)
	}
	// Sequence continues contiguously from the persisted high-water mark.
	for i := 4; i < 7; i++ {
		if err := w2.Append(ctx, EventIn{Actor: "provider:fake", Type: "provider.delta",
			Payload: map[string]any{"i": i}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := VerifyContiguous(ctx, db, sess.ID); err != nil {
		t.Fatalf("post-resume contiguity: %v", err)
	}
	if s := w2.Session().Status; s != "active" {
		t.Fatalf("status after resume = %q, want active", s)
	}
}
