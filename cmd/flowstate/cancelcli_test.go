package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/store"
)

// Given an active session
// When `session cancel <id>` runs
// Then the exit is 0 and the state JSON reports status=cancelled with
// exactly one persisted session.cancelled event.
func TestGivenActiveSessionWhenCancelThenCancelledExactlyOnce(t *testing.T) {
	id := newSessionForCancelCLI(t)

	r := runCLICancel(t, "cancel", id)
	if r.exit != exitOK {
		t.Fatalf("exit %d, want 0", r.exit)
	}
	var state map[string]any
	if err := json.Unmarshal([]byte(strings.SplitN(r.stdout, "\n", 2)[0]), &state); err != nil {
		t.Fatalf("parse stdout %q: %v", r.stdout, err)
	}
	if state["status"] != "cancelled" {
		t.Fatalf("status %v, want cancelled", state["status"])
	}
	assertSingleCancelledEvent(t, id)

	// Given the now-terminal session
	// When cancel runs again
	// Then exit 6 with code session_terminal and still one event.
	r = runCLICancel(t, "cancel", id)
	if r.exit != exitTerminal {
		t.Fatalf("second cancel exit %d, want 6", r.exit)
	}
	assertSingleCancelledEvent(t, id)

	// When close runs on the terminal session
	// Then exit 6 too (close-then-cancel in either order).
	r = runCLICancel(t, "close", id)
	if r.exit != exitTerminal {
		t.Fatalf("close-after-cancel exit %d, want 6", r.exit)
	}
	assertSingleCancelledEvent(t, id)
}

// Given an unknown session id
// When `session cancel` runs
// Then exit 1 (session_not_found).
func TestGivenUnknownSessionWhenCancelThenExit1(t *testing.T) {
	r := runCLICancel(t, "cancel", "ghost-id")
	if r.exit != exitUnknownSession {
		t.Fatalf("exit %d, want 1", r.exit)
	}
}

// Given a missing session id
// When `session cancel` runs
// Then exit 2 (usage).
func TestGivenNoIDWhenCancelThenUsageExit2(t *testing.T) {
	r := runCLICancel(t, "cancel")
	if r.exit != exitUsage {
		t.Fatalf("exit %d, want 2", r.exit)
	}
}

// Given an active session created with a reason-capable cancel
// When `session cancel <id> --reason <text>` runs
// Then exit 0 and the reason is persisted in the event payload.
func TestGivenReasonFlagWhenCancelThenReasonPersisted(t *testing.T) {
	id := newSessionForCancelCLI(t)
	r := runCLIRaw(t, "cancel", id, "--reason", "user pressed ctrl-c")
	if r.exit != exitOK {
		t.Fatalf("exit %d, want 0", r.exit)
	}
	st, err := store.Open(dbFor(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	events, err := st.Replay(context.Background(), id)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	last := events[len(events)-1]
	if last.Type != "session.cancelled" {
		t.Fatalf("last event %q, want session.cancelled", last.Type)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(last.PayloadJSON), &payload); err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if payload["reason"] != "user pressed ctrl-c" {
		t.Fatalf("reason %v, want user pressed ctrl-c", payload["reason"])
	}
}

// newSessionForCancelCLI creates a session via the CLI and returns
// its id.
func newSessionForCancelCLI(t *testing.T) string {
	t.Helper()
	r := runCLIRaw(t, "new", "--provider", "zai", "--model", "glm-4.6", "--task", "cancel me")
	if r.exit != exitOK {
		t.Fatalf("new exit %d: %s", r.exit, r.stdout)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(strings.SplitN(r.stdout, "\n", 2)[0]), &created); err != nil {
		t.Fatalf("parse %q: %v", r.stdout, err)
	}
	return created["id"].(string)
}

// assertSingleCancelledEvent replays the log and requires exactly one
// session.cancelled event (terminal-once under repeated cancels).
func assertSingleCancelledEvent(t *testing.T, id string) {
	t.Helper()
	st, err := store.Open(dbFor(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	events, err := st.Replay(context.Background(), id)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	term := 0
	for _, ev := range events {
		if ev.Type == "session.cancelled" {
			term++
		}
	}
	if term != 1 {
		t.Fatalf("session.cancelled count %d, want exactly 1", term)
	}
}

// runCLICancel runs a session subcommand (cancel/close) against the
// per-test DB.
func runCLICancel(t *testing.T, args ...string) result {
	t.Helper()
	return runCLIRaw(t, args...)
}

// runCLIRaw runs a session subcommand with the shared per-test DB.
func runCLIRaw(t *testing.T, args ...string) result {
	t.Helper()
	return runArgv(t, append([]string{"session", args[0], "--db", dbFor(t)}, args[1:]...))
}
