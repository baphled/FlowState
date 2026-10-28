package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/baphled/flowstate/internal/store"
)

// result captures the exit code and stdout of one CLI run.
type result struct {
	exit   int
	stdout string
}

// runCLI executes dispatch against a temp DB with captured stdout.
func runCLI(t *testing.T, args ...string) result {
	t.Helper()
	argv := append([]string{"session", "--db", dbFor(t)}, args...)
	return runArgv(t, argv)
}

// dbMemo holds one database path per running test so that chained
// CLI invocations within a single test share state.
var dbMemo = map[*testing.T]string{}

// dbFor returns the per-test database path, creating it once.
func dbFor(t *testing.T) string {
	t.Helper()
	if p, ok := dbMemo[t]; ok {
		return p
	}
	p := t.TempDir() + "/test.db"
	dbMemo[t] = p
	t.Cleanup(func() { delete(dbMemo, t) })
	return p
}

// runArgv runs a full session argv including any --db override. Output
// is drained concurrently so a full pipe buffer cannot deadlock.
func runArgv(t *testing.T, argv []string) result {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(outR)
		done <- string(b)
	}()
	exit := dispatch(argv, outW, outW)
	_ = outW.Close()
	out := <-done
	_ = outR.Close()
	return result{exit: exit, stdout: out}
}

func TestGivenNoArgsWhenInvokedThenUsageExit2(t *testing.T) {
	r := runCLI(t)
	if r.exit != 2 {
		t.Fatalf("exit %d, want 2", r.exit)
	}
}

func TestGivenUnknownCommandWhenInvokedThenUsageExit2(t *testing.T) {
	r := runCLI(t, "bogus")
	if r.exit != 2 {
		t.Fatalf("exit %d, want 2", r.exit)
	}
}

func TestGivenSessionNewWithoutFlagsWhenInvokedThenUsageExit2(t *testing.T) {
	r := runCLI(t, "new")
	if r.exit != 2 {
		t.Fatalf("exit %d, want 2", r.exit)
	}
}

func TestGivenUnknownFlagWhenInvokedThenUsageExit2(t *testing.T) {
	r := runCLIList(t, "--nope")
	if r.exit != 2 {
		t.Fatalf("exit %d, want 2", r.exit)
	}
}

func TestGivenMissingFlagValueWhenInvokedThenUsageExit2(t *testing.T) {
	r := runCLIList(t, "--db")
	if r.exit != 2 {
		t.Fatalf("exit %d, want 2", r.exit)
	}
}

func TestGivenUnreadableTaskFileWhenNewThenUsageExit2(t *testing.T) {
	r := runCLINew(t, "--provider", "p", "--model", "m", "--task", "@/nonexistent/file")
	if r.exit != 2 {
		t.Fatalf("exit %d, want 2", r.exit)
	}
}

func TestGivenBadDBPathWhenInvokedThenConfigInvalidExit4(t *testing.T) {
	// A directory component that is actually a regular file is an
	// invalid DB path on every platform (Linux and Windows alike),
	// unlike /proc paths, which modernc.org/sqlite happily creates on
	// Windows.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	r := runArgv(t, []string{"session", "list", "--db", filepath.Join(blocker, "x.db")})
	if r.exit != 4 {
		t.Fatalf("exit %d, want 4", r.exit)
	}
}

func TestGivenFreshDBWhenSessionListThenExit0NoOutput(t *testing.T) {
	r := runCLIList(t)
	if r.exit != 0 || r.stdout != "" {
		t.Fatalf("exit %d out %q, want 0 and empty", r.exit, r.stdout)
	}
}

func TestGivenInvalidStatusFilterWhenListThenUsageExit2(t *testing.T) {
	r := runCLIList(t, "--status", "zombie")
	if r.exit != 2 {
		t.Fatalf("exit %d, want 2", r.exit)
	}
}

func TestGivenSessionNewWhenCreatedThenJSONRecordExit0(t *testing.T) {
	r := runCLINew(t, "--provider", "prov", "--model", "m1", "--task", "do things")
	if r.exit != 0 {
		t.Fatalf("exit %d, want 0", r.exit)
	}
	var rec struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(firstLine(r.stdout)), &rec); err != nil {
		t.Fatalf("stdout not JSON: %q (%v)", r.stdout, err)
	}
	if rec.Status != "active" || rec.ID == "" {
		t.Fatalf("record %+v", rec)
	}
}

func TestGivenTaskFileWhenNewThenFileContentsUsed(t *testing.T) {
	f := t.TempDir() + "/task.txt"
	if err := os.WriteFile(f, []byte("task from file"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runCLINew(t, "--provider", "p", "--model", "m", "--task", "@"+f)
	if r.exit != 0 {
		t.Fatalf("exit %d, want 0", r.exit)
	}
}

func TestGivenCreatedSessionWhenListThenSessionListedExit0(t *testing.T) {
	r := runCLINew(t, "--provider", "prov", "--model", "m1", "--task", "t")
	if r.exit != 0 {
		t.Fatalf("new exit %d", r.exit)
	}
	var rec struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(firstLine(r.stdout)), &rec); err != nil {
		t.Fatalf("unmarshal new output: %v", err)
	}
	l := runCLIList(t)
	if l.exit != 0 || len(lines(l.stdout)) != 1 {
		t.Fatalf("list exit %d out %q", l.exit, l.stdout)
	}
}

func TestGivenClosedSessionWhenListWithStatusCancelledThenListed(t *testing.T) {
	r := runCLINew(t, "--provider", "p", "--model", "m", "--task", "t")
	var rec struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(firstLine(r.stdout)), &rec); err != nil {
		t.Fatalf("unmarshal new output: %v", err)
	}
	runCLIClose(t, rec.ID)
	l := runCLIList(t, "--status", "cancelled")
	if l.exit != 0 || len(lines(l.stdout)) != 1 {
		t.Fatalf("list exit %d out %q", l.exit, l.stdout)
	}
}

func TestGivenUnknownSessionWhenEventsThenExit1SessionNotFound(t *testing.T) {
	r := runCLIEvents(t, "missing-id")
	if r.exit != 1 {
		t.Fatalf("exit %d, want 1", r.exit)
	}
}

func TestGivenMissingIDWhenEventsThenUsageExit2(t *testing.T) {
	r := runCLI(t, "events")
	if r.exit != 2 {
		t.Fatalf("exit %d, want 2", r.exit)
	}
}

func TestGivenCreatedSessionWhenEventsThenTurnStartJSONL(t *testing.T) {
	r := runCLINew(t, "--provider", "p", "--model", "m", "--task", "hello")
	var rec struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(firstLine(r.stdout)), &rec); err != nil {
		t.Fatalf("unmarshal new output: %v", err)
	}
	e := runCLIEvents(t, rec.ID)
	if e.exit != 0 || len(lines(e.stdout)) != 1 {
		t.Fatalf("events exit %d out %q", e.exit, e.stdout)
	}
	var ev struct {
		Type string `json:"type"`
		Seq  int    `json:"seq"`
	}
	if err := json.Unmarshal([]byte(firstLine(e.stdout)), &ev); err != nil {
		t.Fatalf("unmarshal events output: %v", err)
	}
	if ev.Type != "turn.start" || ev.Seq != 0 {
		t.Fatalf("event %+v", ev)
	}
}

func TestGivenUnknownSessionWhenCloseThenExit1(t *testing.T) {
	r := runCLIClose(t, "missing-id")
	if r.exit != 1 {
		t.Fatalf("exit %d, want 1", r.exit)
	}
}

func TestGivenActiveSessionWhenCloseThenCancelledExit0(t *testing.T) {
	r := runCLINew(t, "--provider", "p", "--model", "m", "--task", "t")
	var rec struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(firstLine(r.stdout)), &rec); err != nil {
		t.Fatalf("unmarshal new output: %v", err)
	}
	c := runCLIClose(t, rec.ID)
	if c.exit != 0 {
		t.Fatalf("close exit %d, want 0", c.exit)
	}
	var out struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(firstLine(c.stdout)), &out); err != nil {
		t.Fatalf("unmarshal close output: %v", err)
	}
	if out.Status != "cancelled" {
		t.Fatalf("status %q", out.Status)
	}
}

func TestGivenAlreadyClosedSessionWhenCloseAgainThenTerminalExit6(t *testing.T) {
	r := runCLINew(t, "--provider", "p", "--model", "m", "--task", "t")
	var rec struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(firstLine(r.stdout)), &rec); err != nil {
		t.Fatalf("unmarshal new output: %v", err)
	}
	runCLIClose(t, rec.ID)
	c := runCLIClose(t, rec.ID)
	if c.exit != 6 {
		t.Fatalf("exit %d, want 6", c.exit)
	}
}

func TestGivenEventsAfterCloseWhenInspectedThenCancelledEventLast(t *testing.T) {
	r := runCLINew(t, "--provider", "p", "--model", "m", "--task", "t")
	var rec struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(firstLine(r.stdout)), &rec); err != nil {
		t.Fatalf("unmarshal new output: %v", err)
	}
	runCLIClose(t, rec.ID)
	e := runCLIEvents(t, rec.ID)
	evs := lines(e.stdout)
	if len(evs) != 2 {
		t.Fatalf("got %d events, want 2", len(evs))
	}
	var last struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(evs[1]), &last); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if last.Type != "session.cancelled" {
		t.Fatalf("last type %q", last.Type)
	}
}

func TestGivenTamperedPayloadWhenEventsThenReplayMismatchExit7(t *testing.T) {
	db := t.TempDir() + "/t.db"
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sess, err := st.CreateSession(ctx, "sid-tamper", "p", "m", "h", "sha")
	if err != nil {
		t.Fatal(err)
	}
	w, _ := st.NewEventWriter(ctx, sess.ID)
	if _, err := w.Append(ctx, "user", "turn.start", map[string]any{"task": "t"}); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE events SET payload_json='{"task":"tampered"}' WHERE session_id=? AND seq=0`, sess.ID); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	r := runArgv(t, []string{"session", "events", sess.ID, "--db", db})
	if r.exit != 7 {
		t.Fatalf("exit %d, want 7", r.exit)
	}
	fmt.Fprintln(os.Stderr)
}

// firstLine returns the first non-empty line of s.
func firstLine(s string) string {
	for _, l := range lines(s) {
		return l
	}
	return ""
}

// lines returns the non-empty lines of s.
func lines(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			l := s[start:i]
			if l != "" {
				out = append(out, l)
			}
			start = i + 1
		}
	}
	return out
}

// runCLIClose runs `session close <id>` against a fresh temp DB.
func runCLIClose(t *testing.T, id string) result {
	t.Helper()
	return runArgv(t, []string{"session", "close", id, "--db", dbFor(t)})
}

// runCLIEvents runs `session events <id>` against a fresh temp DB.
func runCLIEvents(t *testing.T, id string) result {
	t.Helper()
	return runArgv(t, []string{"session", "events", id, "--db", dbFor(t)})
}

// runCLINew runs `session new` with the given args against a fresh
// temp DB.
func runCLINew(t *testing.T, args ...string) result {
	t.Helper()
	return runArgv(t, append([]string{"session", "new", "--db", dbFor(t)}, args...))
}

// runCLIList runs `session list` with the given args against a fresh
// temp DB.
func runCLIList(t *testing.T, args ...string) result {
	t.Helper()
	return runArgv(t, append([]string{"session", "list", "--db", dbFor(t)}, args...))
}
