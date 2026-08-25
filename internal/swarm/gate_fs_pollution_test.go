package swarm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/coordination"
)

// newFSPollutionHarness builds a runner anchored at a fake repo root
// with a controlled home so vault-root expansion is deterministic.
func newFSPollutionHarness(t *testing.T, homeDir string) (GateRunner, string) {
	t.Helper()
	root := t.TempDir()
	return NewFSPollutionRunner(root, []string{"vaults/baphled", filepath.Join(homeDir, "vaults", "baphled")}), root
}

func fsPollutionGate() GateSpec {
	return GateSpec{Name: "no-fs-pollution", Kind: FSPollutionGateKind, When: LifecyclePostMember, Target: "worker"}
}

// fsCoordStore is a minimal coordination.Store returning a fixed
// payload. Avoids importing the real store for a focused unit test.
type fsCoordStore struct {
	payload string
	missing bool
}

func (s fsCoordStore) Get(key string) ([]byte, error) {
	if s.missing {
		return nil, coordination.ErrKeyNotFound
	}
	return []byte(s.payload), nil
}

func (s fsCoordStore) Set(key string, value []byte) error { return nil }

func (s fsCoordStore) List(prefix string) ([]string, error) { return nil, nil }

func (s fsCoordStore) Delete(key string) error { return nil }

func (s fsCoordStore) Increment(key string) (int, error) { return 0, nil }

func (s fsCoordStore) Exists(key string) (bool, error) { return !s.missing, nil }

func fsArgs(store coordination.Store) GateArgs {
	return GateArgs{SwarmID: "s", ChainPrefix: "p", MemberID: "worker", CoordStore: store}
}

func TestFSPollutionGateRejectsCoordinationWrite(t *testing.T) {
	homeDir := t.TempDir()
	runner, root := newFSPollutionHarness(t, homeDir)
	payload := `{"writes":[{"path":"/tmp/chain/handoff.json","content_type":"coordination","role":"junior"}]}`
	err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload}))
	if err == nil {
		t.Fatal("expected gate failure for /tmp coordination write")
	}
	if !strings.Contains(err.Error(), "/tmp/chain/handoff.json") {
		t.Fatalf("failure should name offending path, got: %v", err)
	}
	if !strings.Contains(err.Error(), "coordination store") {
		t.Fatalf("failure should name the coordination store remedy, got: %v", err)
	}
	_ = root
}

func TestFSPollutionGateRejectsScratchWrite(t *testing.T) {
	runner, _ := newFSPollutionHarness(t, t.TempDir())
	payload := `{"writes":[{"path":"scratch-notes.md","content_type":"scratch","role":"junior"}]}`
	err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload}))
	if err == nil {
		t.Fatal("expected gate failure for scratch write inside repo working dir? (cwd-relative notes are not source)")
	}
}

func TestFSPollutionGatePassesWorktreeSource(t *testing.T) {
	root := t.TempDir()
	runner := NewFSPollutionRunner(root, nil)
	payload := `{"writes":[{"path":"internal/swarm/gates.go","content_type":"source","role":"junior"}]}`
	if err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload})); err != nil {
		t.Fatalf("worktree source write should pass, got: %v", err)
	}
}

func TestFSPollutionGatePassesVaultWriteForKBCurator(t *testing.T) {
	homeDir := t.TempDir()
	runner, _ := newFSPollutionHarness(t, homeDir)
	vaultPath := filepath.Join(homeDir, "vaults", "baphled", "notes.md")
	payload := `{"writes":[{"path":` + quoteJSON(vaultPath) + `,"content_type":"vault","role":"kb-curator"}]}`
	if err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload})); err != nil {
		t.Fatalf("sanctioned vault write for kb-curator should pass, got: %v", err)
	}
}

func TestFSPollutionGateRejectsVaultWriteForNonCurator(t *testing.T) {
	homeDir := t.TempDir()
	runner, _ := newFSPollutionHarness(t, homeDir)
	vaultPath := filepath.Join(homeDir, "vaults", "baphled", "notes.md")
	payload := `{"writes":[{"path":` + quoteJSON(vaultPath) + `,"content_type":"vault","role":"junior"}]}`
	if err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload})); err == nil {
		t.Fatal("vault write from non-curator role should fail")
	}
}

func TestFSPollutionGateNilCoordStoreFails(t *testing.T) {
	runner, _ := newFSPollutionHarness(t, t.TempDir())
	err := runner.Run(context.Background(), fsPollutionGate(), GateArgs{SwarmID: "s"})
	if err == nil || !strings.Contains(err.Error(), "coordination store unavailable") {
		t.Fatalf("expected typed unavailable failure, got: %v", err)
	}
}

func TestFSPollutionGateNoWritesPasses(t *testing.T) {
	runner, _ := newFSPollutionHarness(t, t.TempDir())
	if err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: `{}`})); err != nil {
		t.Fatalf("empty report should pass, got: %v", err)
	}
}

func TestFSPollutionGateBadJSONFails(t *testing.T) {
	runner, _ := newFSPollutionHarness(t, t.TempDir())
	err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: `not-json`}))
	if err == nil || !strings.Contains(err.Error(), "decoding fs-write report") {
		t.Fatalf("expected decode failure, got: %v", err)
	}
}

func TestFSPollutionGateAggregatesViolations(t *testing.T) {
	runner, _ := newFSPollutionHarness(t, t.TempDir())
	payload := `{"writes":[{"path":"/tmp/a.json","content_type":"coordination","role":"junior"},{"path":"/tmp/b.md","content_type":"scratch","role":"junior"}]}`
	err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload}))
	if err == nil {
		t.Fatal("expected aggregated failure")
	}
	if !strings.Contains(err.Error(), "2 polluting filesystem write(s)") {
		t.Fatalf("expected aggregate count, got: %v", err)
	}
	if !strings.Contains(err.Error(), "/tmp/a.json") || !strings.Contains(err.Error(), "/tmp/b.md") {
		t.Fatalf("expected both paths named, got: %v", err)
	}
}

func TestFSPollutionGateMissingKeyFails(t *testing.T) {
	runner, _ := newFSPollutionHarness(t, t.TempDir())
	err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{missing: true}))
	if err == nil {
		t.Fatal("expected gate failure on coord-store read error")
	}
}

func TestExpandVaultRoot(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	if got := expandVaultRoot("~/vaults"); got != "/home/tester/vaults" {
		t.Fatalf("tilde expansion: got %s", got)
	}
	if got := expandVaultRoot("vaults/baphled"); got != "/home/tester/vaults/baphled" {
		t.Fatalf("bare-name expansion: got %s", got)
	}
	if got := expandVaultRoot("/srv/abs"); got != "/srv/abs" {
		t.Fatalf("absolute passthrough: got %s", got)
	}
}

func TestFSPathWithinRoot(t *testing.T) {
	if !fsPathWithinRoot("/home/x/vaults/baphled/notes.md", "/home/x/vaults/baphled") {
		t.Fatal("nested path should match root")
	}
	if fsPathWithinRoot("/home/x/vaults/other/notes.md", "/home/x/vaults/baphled") {
		t.Fatal("sibling path should not match root")
	}
}

func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

func TestNewFSPollutionRunnerEmptyRootFallsBackToCwd(t *testing.T) {
	r := NewFSPollutionRunner("", nil).(*fsPollutionRunner)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if r.repoRoot != cwd {
		t.Fatalf("expected cwd fallback %s, got %s", cwd, r.repoRoot)
	}
	if len(r.vaultRoots) == 0 {
		t.Fatal("empty vault roots should select defaults")
	}
}

func TestFSPollutionGateEmptyPathSkipped(t *testing.T) {
	runner, _ := newFSPollutionHarness(t, t.TempDir())
	payload := `{"writes":[{"path":"","content_type":"scratch","role":"junior"}]}`
	if err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload})); err != nil {
		t.Fatalf("empty path should be skipped, got: %v", err)
	}
}

func TestFSPollutionGateRelativeVaultRoot(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	runner := NewFSPollutionRunner(t.TempDir(), []string{"vaults/baphled"})
	vaultPath := filepath.Join(homeDir, "vaults", "baphled", "kb", "note.md")
	payload := `{"writes":[{"path":` + quoteJSON(vaultPath) + `,"content_type":"vault","role":"kb-curator"}]}`
	if err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload})); err != nil {
		t.Fatalf("tilde vault root should match for kb-curator, got: %v", err)
	}
}

func TestPreCommitPollutionCheck(t *testing.T) {
	cases := []struct {
		name      string
		untracked []string
		blocked   bool
	}{
		{"nil skips", nil, false},
		{"clean", []string{"internal/swarm/gates.go"}, false},
		{"stray report", []string{"stray-report.json"}, true},
		{"scratch notes", []string{"scratch-notes.md"}, true},
		{"nested report ignored", []string{"docs/report.md"}, false},
		{"handoff blocked", []string{"handoff.json"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocked, reason := PreCommitPollutionCheck("/repo", tc.untracked)
			if blocked != tc.blocked {
				t.Fatalf("blocked=%v want %v (reason=%s)", blocked, tc.blocked, reason)
			}
			if blocked && !strings.Contains(reason, "pollution") {
				t.Fatalf("reason should mention pollution, got: %s", reason)
			}
		})
	}
}

func TestFSPollutionGateVaultPathOutsideRootsFails(t *testing.T) {
	homeDir := t.TempDir()
	runner := NewFSPollutionRunner(t.TempDir(), []string{"vaults/baphled"})
	outside := filepath.Join(homeDir, "elsewhere", "notes.md")
	payload := `{"writes":[{"path":` + quoteJSON(outside) + `,"content_type":"vault","role":"kb-curator"}]}`
	if err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload})); err == nil {
		t.Fatal("vault write outside configured roots should fail")
	}
}

func TestFSNameMatchesPollution(t *testing.T) {
	for _, yes := range []string{"report.json", "notes.md", "handoff", "summary.txt", "STRAY-REPORT.md"} {
		if !fsNameMatchesPollution(yes) {
			t.Fatalf("%s should match", yes)
		}
	}
	for _, no := range []string{"gates.go", "readme.md", "feature.spec"} {
		if fsNameMatchesPollution(no) {
			t.Fatalf("%s should not match", no)
		}
	}
}
