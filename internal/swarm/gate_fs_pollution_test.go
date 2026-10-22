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
	return newFSPollutionHarnessWithRole(t, homeDir, "kb-curator"), root
}

// newFSPollutionHarnessWithRole builds a harness whose worker member
// is attested in the manifest profile for the given role.
func newFSPollutionHarnessWithRole(t *testing.T, homeDir, role string) GateRunner {
	t.Helper()
	root := t.TempDir()
	return NewFSPollutionRunnerWithProfiles(root, []string{"vaults/baphled", filepath.Join(homeDir, "vaults", "baphled")}, map[string]FSPollutionMemberProfile{
		"worker": {Role: role, ContentTypes: []string{"source", "vault"}},
	})
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
	runner := NewFSPollutionRunnerWithProfiles(root, nil, map[string]FSPollutionMemberProfile{
		"worker": {Role: "junior", ContentTypes: []string{"source"}},
	})
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

func TestFSPollutionGateEmptyPathFails(t *testing.T) {
	runner, _ := newFSPollutionHarness(t, t.TempDir())
	payload := `{"writes":[{"path":"","content_type":"scratch","role":"junior"}]}`
	if err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload})); err == nil {
		t.Fatal("empty path should be a gate violation (fail closed), got pass")
	}
}

func TestFSPollutionGateRelativeVaultRoot(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	runner := NewFSPollutionRunnerWithProfiles(t.TempDir(), []string{"vaults/baphled"}, map[string]FSPollutionMemberProfile{
		"worker": {Role: "kb-curator", ContentTypes: []string{"vault"}},
	})
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
	runner := NewFSPollutionRunnerWithProfiles(t.TempDir(), []string{"vaults/baphled"}, map[string]FSPollutionMemberProfile{
		"worker": {Role: "kb-curator", ContentTypes: []string{"vault"}},
	})
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

// TestFSPollutionGateFlagsSymlinkInsideRepoPointingOutside verifies
// ADR-0001 gap 3: a reported path that sits lexically inside the
// repo root but passes through a symlink to a directory outside it
// must be resolved before the containment check and rejected.
func TestFSPollutionGateFlagsSymlinkInsideRepoPointingOutside(t *testing.T) {
	repoRoot := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "loot.json")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(repoRoot, "internal")
	if err := os.Symlink(outside, linkDir); err != nil {
		t.Fatal(err)
	}
	runner := NewFSPollutionRunner(repoRoot, nil)
	payload := `{"writes":[{"path":` + quoteJSON(filepath.Join(linkDir, "loot.json")) + `,"content_type":"source","role":"junior"}]}`
	err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload}))
	if err == nil {
		t.Fatal("symlink laundered write resolving outside the repo root must be a violation")
	}
}

// TestFSPollutionGateFlagsSymlinkInsideVaultPointingOutside verifies
// the same laundering against a sanctioned vault root.
func TestFSPollutionGateFlagsSymlinkInsideVaultPointingOutside(t *testing.T) {
	homeDir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(homeDir, "vaults")); err != nil {
		t.Fatal(err)
	}
	runner := NewFSPollutionRunner(t.TempDir(), []string{"vaults/baphled"})
	laundered := filepath.Join(homeDir, "vaults", "baphled", "note.md")
	payload := `{"writes":[{"path":` + quoteJSON(laundered) + `,"content_type":"vault","role":"kb-curator"}]}`
	err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload}))
	if err == nil {
		t.Fatal("symlink laundered vault write resolving outside the vault root must be a violation")
	}
}

// TestResolveSymlinkAncestry covers the ancestor-resolution helper:
// existing paths resolve fully, non-existent tails are re-joined.
func TestResolveSymlinkAncestry(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	got := resolveSymlinkAncestry(filepath.Join(dir, "link", "new.md"))
	want := filepath.Join(outside, "new.md")
	if got != want {
		t.Fatalf("resolveSymlinkAncestry: got %s want %s", got, want)
	}
	got = resolveSymlinkAncestry(filepath.Join(dir, "missing", "tail.md"))
	want = filepath.Join(dir, "missing", "tail.md")
	if got != want {
		t.Fatalf("non-existent ancestor should rejoin remainder: got %s want %s", got, want)
	}
}

// TestFSPollutionGateRejectsManifestContradiction verifies ADR-0001
// gap 1: a self-declared role that contradicts the manifest-declared
// role fails closed as an unattested declaration.
func TestFSPollutionGateRejectsManifestContradiction(t *testing.T) {
	homeDir := t.TempDir()
	runner, _ := newFSPollutionHarness(t, homeDir)
	manifests := map[string]FSPollutionMemberProfile{"worker": {Role: "junior", ContentTypes: []string{"source"}}}
	runner.(*fsPollutionRunner).profiles = manifests
	vaultPath := filepath.Join(homeDir, "vaults", "baphled", "notes.md")
	payload := `{"writes":[{"path":` + quoteJSON(vaultPath) + `,"content_type":"vault","role":"kb-curator"}]}`
	err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload}))
	if err == nil {
		t.Fatal("declaration contradicting the manifest must fail closed")
	}
	if !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("violation should name the manifest mismatch, got: %v", err)
	}
}

// TestFSPollutionGateRejectsUnknownContentType verifies an unknown
// content_type fails closed rather than defaulting to sanctioned.
func TestFSPollutionGateRejectsUnknownContentType(t *testing.T) {
	homeDir := t.TempDir()
	runner, _ := newFSPollutionHarness(t, homeDir)
	manifests := map[string]FSPollutionMemberProfile{"worker": {Role: "junior", ContentTypes: []string{"source"}}}
	runner.(*fsPollutionRunner).profiles = manifests
	payload := `{"writes":[{"path":"internal/swarm/gates.go","content_type":"knowledge-base","role":"junior"}]}`
	err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload}))
	if err == nil {
		t.Fatal("content_type not in the manifest output profile must fail closed")
	}
}

// TestFSPollutionGateRejectsUnknownMember verifies a report from a
// member absent from the manifest fails closed.
func TestFSPollutionGateRejectsUnknownMember(t *testing.T) {
	runner := NewFSPollutionRunnerWithProfiles(t.TempDir(), nil, map[string]FSPollutionMemberProfile{
		"worker": {Role: "junior", ContentTypes: []string{"source"}},
	})
	payload := `{"writes":[{"path":"internal/swarm/gates.go","content_type":"source","role":"junior"}]}`
	err := runner.Run(context.Background(), fsPollutionGate(), GateArgs{SwarmID: "s", MemberID: "ghost", CoordStore: fsCoordStore{payload: payload}})
	if err == nil {
		t.Fatal("write from a member with no manifest entry must fail closed")
	}
}

// TestFSPollutionGateProfiledMemberPasses verifies a declaration
// consistent with the manifest passes.
func TestFSPollutionGateProfiledMemberPasses(t *testing.T) {
	homeDir := t.TempDir()
	runner := NewFSPollutionRunnerWithProfiles(t.TempDir(), nil, map[string]FSPollutionMemberProfile{
		"worker": {Role: "kb-curator", ContentTypes: []string{"source", "vault"}},
	})
	runner.(*fsPollutionRunner).vaultRoots = []string{filepath.Join(homeDir, "vaults", "baphled")}
	vaultPath := filepath.Join(homeDir, "vaults", "baphled", "notes.md")
	payload := `{"writes":[{"path":` + quoteJSON(vaultPath) + `,"content_type":"vault","role":"kb-curator"}]}`
	if err := runner.Run(context.Background(), fsPollutionGate(), fsArgs(fsCoordStore{payload: payload})); err != nil {
		t.Fatalf("manifest-consistent declaration should pass, got: %v", err)
	}
}
