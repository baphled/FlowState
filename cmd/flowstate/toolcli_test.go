package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/tool"
)

// runTool executes `session tool` against the shared test DB.
func runTool(t *testing.T, id string, extra ...string) result {
	t.Helper()
	argv := append([]string{"session", "tool", "--db", dbFor(t), id}, extra...)
	return runArgv(t, argv)
}

// newTestSession creates a session via the CLI and returns its id.
func newTestSession(t *testing.T) string {
	t.Helper()
	r := runCLINew(t, "--provider", "zai", "--model", "glm", "--task", "x")
	if r.exit != 0 {
		t.Fatalf("new: exit %d out %s", r.exit, r.stdout)
	}
	var created struct{ ID string }
	if err := json.Unmarshal([]byte(strings.SplitN(r.stdout, "\n", 2)[0]), &created); err != nil {
		t.Fatalf("parse %q: %v", r.stdout, err)
	}
	return created.ID
}

func TestGivenSessionAndAllowFlagWhenInvokeEchoThenToolCallAndResultEventsPersisted(t *testing.T) {
	id := newTestSession(t)
	r := runTool(t, id, "--tool", "echo", "--tool-arg", `{"a":1}`, "--allow", "echo")
	if r.exit != 0 {
		t.Fatalf("exit %d out %s", r.exit, r.stdout)
	}
	if !strings.Contains(r.stdout, `"ok":true`) {
		t.Fatalf("out %s", r.stdout)
	}
	ev := runCLIEvents(t, id)
	if ev.exit != 0 {
		t.Fatalf("events exit %d", ev.exit)
	}
	if !strings.Contains(ev.stdout, tool.TypeToolCall) || !strings.Contains(ev.stdout, tool.TypeToolResult) {
		t.Fatalf("missing tool events: %s", ev.stdout)
	}
	if !strings.Contains(ev.stdout, `"tool:echo"`) {
		t.Fatalf("missing tool actor: %s", ev.stdout)
	}
}

func TestGivenNoAllowlistWhenInvokeEchoThenDeniedWithTypedEventAndUsageExit(t *testing.T) {
	id := newTestSession(t)
	r := runTool(t, id, "--tool", "echo")
	if r.exit != exitUsage {
		t.Fatalf("exit %d, want %d", r.exit, exitUsage)
	}
	ev := runCLIEvents(t, id)
	if !strings.Contains(ev.stdout, "tool_denied") {
		t.Fatalf("no typed deny event: %s", ev.stdout)
	}
}

func TestGivenUnknownToolWhenInvokeThenUsageExitWithoutToolCallEvent(t *testing.T) {
	id := newTestSession(t)
	r := runTool(t, id, "--tool", "ghost", "--allow", "ghost")
	if r.exit != exitUsage {
		t.Fatalf("exit %d", r.exit)
	}
	ev := runCLIEvents(t, id)
	if strings.Contains(ev.stdout, tool.TypeToolCall) {
		t.Fatalf("unexpected tool.call: %s", ev.stdout)
	}
}

func TestGivenAllowFileManifestWhenInvokeEchoThenAllowed(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "allow.json")
	if err := os.WriteFile(manifest, []byte(`["echo"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	id := newTestSession(t)
	r := runTool(t, id, "--tool", "echo", "--allow-file", manifest)
	if r.exit != 0 {
		t.Fatalf("exit %d out %s", r.exit, r.stdout)
	}
}

func TestGivenBadAllowFileWhenInvokeThenUsageExit(t *testing.T) {
	id := newTestSession(t)
	r := runTool(t, id, "--tool", "echo", "--allow-file", "/nonexistent.json")
	if r.exit != exitUsage {
		t.Fatalf("exit %d", r.exit)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte(`{"not":"an array"}`), 0o644)
	r = runTool(t, id, "--tool", "echo", "--allow-file", bad)
	if r.exit != exitUsage {
		t.Fatalf("exit %d (bad json)", r.exit)
	}
}

func TestGivenTerminalSessionWhenInvokeToolThenTerminalExit(t *testing.T) {
	id := newTestSession(t)
	if r := runCLIClose(t, id); r.exit != 0 {
		t.Fatalf("close: %d", r.exit)
	}
	r := runTool(t, id, "--tool", "echo", "--allow", "echo")
	if r.exit != exitTerminal {
		t.Fatalf("exit %d, want %d", r.exit, exitTerminal)
	}
}

func TestGivenUnknownSessionWhenInvokeToolThenUnknownSessionExit(t *testing.T) {
	r := runTool(t, "no-such-session", "--tool", "echo", "--allow", "echo")
	if r.exit != exitUnknownSession {
		t.Fatalf("exit %d", r.exit)
	}
}

func TestGivenNoToolFlagWhenInvokeToolThenUsageExit(t *testing.T) {
	id := newTestSession(t)
	r := runTool(t, id)
	if r.exit != exitUsage {
		t.Fatalf("exit %d", r.exit)
	}
}

func TestGivenPriorEventsWhenInvokeTwiceThenSeqContinuesContiguously(t *testing.T) {
	id := newTestSession(t)
	if r := runTool(t, id, "--tool", "echo", "--allow", "echo"); r.exit != 0 {
		t.Fatalf("invoke1: %d", r.exit)
	}
	if r := runTool(t, id, "--tool", "echo", "--allow", "echo"); r.exit != 0 {
		t.Fatalf("invoke2: %d", r.exit)
	}
	ev := runCLIEvents(t, id)
	var seqs []int
	for _, line := range strings.Split(strings.TrimSpace(ev.stdout), "\n") {
		var e struct {
			Seq int `json:"seq"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		seqs = append(seqs, e.Seq)
	}
	want := []int{0, 1, 2, 3, 4} // turn.start, call, result, call, result
	if len(seqs) != len(want) {
		t.Fatalf("seqs %v", seqs)
	}
	for i := range want {
		if seqs[i] != want[i] {
			t.Fatalf("seqs %v, want %v", seqs, want)
		}
	}
}
