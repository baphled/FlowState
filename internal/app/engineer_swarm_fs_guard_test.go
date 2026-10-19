package app_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/swarm"
)

// M06 negative guard tests: prove the Mid/Junior fs-pollution gates in
// the embedded engineer-swarm.yml actually EXECUTE at the nested
// post-member boundary (not merely that the YAML parses), and that
// out-of-scope writes / missing reports FAIL the gate.

// engineerSwarmGates loads the embedded engineer-swarm manifest and
// returns its gate specs. Mirrors the EmbeddedSwarmsFS loader the app
// wires at startup, so the manifest under test is the shipped one.
func engineerSwarmGates(t *testing.T) []swarm.GateSpec {
	t.Helper()
	swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
	if err != nil {
		t.Fatal(err)
	}
	body, err := fs.ReadFile(swarmsDir, "engineer-swarm.yml")
	if err != nil {
		t.Fatal(err)
	}
	var m swarm.Manifest
	if err := yaml.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(nil); err != nil {
		t.Fatalf("embedded engineer-swarm manifest invalid: %v", err)
	}
	return m.Harness.Gates
}

// postMemberFSGateFor returns the fs-pollution-guard gate targeting the
// given member, failing the test when absent — the guards are the
// deliverable, so absence is the failure being guarded against.
func postMemberFSGateFor(t *testing.T, member string) swarm.GateSpec {
	t.Helper()
	for _, g := range engineerSwarmGates(t) {
		if g.Kind == swarm.FSPollutionGateKind && g.When == swarm.LifecyclePostMember && g.Target == member {
			return g
		}
	}
	t.Fatalf("no post-member fs-pollution-guard targeting %s in embedded engineer-swarm.yml", member)
	return swarm.GateSpec{}
}

// runFSGate executes one gate spec against the in-memory coordination
// store and repo root via the SAME MultiRunner the app registers in
// buildSwarmGateRunner, so the gate kinds resolve as in production.
func runFSGate(t *testing.T, gate swarm.GateSpec, store coordination.Store, repoRoot string, chainID string) error {
	t.Helper()
	runner := swarm.NewMultiRunner()
	runner.Register(swarm.FSPollutionGateKind, swarm.NewFSPollutionRunner(repoRoot, nil))
	return runner.Run(context.Background(), gate, swarm.GateArgs{
		SwarmID:     "engineer-swarm",
		ChainPrefix: "engineer-swarm",
		ChainID:     chainID,
		MemberID:    gate.Target,
		CoordStore:  store,
	})
}

// TestEngineerSwarmMidJuniorFSGuardsPresent proves both guards exist on
// the embedded manifest and target only the nested write-capable
// members (Mid-Engineer / Junior-Engineer) at the post-member phase.
func TestEngineerSwarmMidJuniorFSGuardsPresent(t *testing.T) {
	gates := engineerSwarmGates(t)
	found := map[string]bool{}
	for _, g := range gates {
		if g.Kind != swarm.FSPollutionGateKind || g.When != swarm.LifecyclePostMember {
			continue
		}
		switch g.Target {
		case "Mid-Engineer", "Junior-Engineer":
			found[g.Target] = true
			if g.OutputKey != "fs-writes" {
				t.Errorf("%s guard output_key = %q, want fs-writes", g.Target, g.OutputKey)
			}
			if g.FailurePolicy != swarm.FailurePolicyHalt {
				t.Errorf("%s guard failurePolicy = %q, want halt", g.Target, g.FailurePolicy)
			}
		}
	}
	if !found["Mid-Engineer"] || !found["Junior-Engineer"] {
		t.Fatalf("engineer-swarm.yml missing Mid/Junior fs-pollution guards: %+v", found)
	}
}

// TestEngineerSwarmMidFSGuardRejectsOutOfScopeWrite — negative: a
// Mid-Engineer fs-write report naming an out-of-worktree path (/tmp
// handoff file) must FAIL the guard with the offending path named.
func TestEngineerSwarmMidFSGuardRejectsOutOfScopeWrite(t *testing.T) {
	gate := postMemberFSGateFor(t, "Mid-Engineer")
	store := coordination.NewMemoryStore()
	key := strings.Join([]string{"engineer-swarm", "Mid-Engineer", gate.OutputKey}, "/")
	payload := `{"writes":[{"path":"/tmp/mid-scratch/handoff.json","content_type":"coordination","role":"mid-engineer"}]}`
	if err := store.Set(key, []byte(payload)); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	err := runFSGate(t, gate, store, repo, "eng-test")
	if err == nil {
		t.Fatal("Mid-Engineer out-of-scope /tmp write must fail the fs-pollution guard")
	}
	if !strings.Contains(err.Error(), "/tmp/mid-scratch/handoff.json") {
		t.Fatalf("failure must name offending path, got: %v", err)
	}
}

// TestEngineerSwarmJuniorFSGuardRejectsOutOfScopeWrite — same negative
// for Junior-Engineer.
func TestEngineerSwarmJuniorFSGuardRejectsOutOfScopeWrite(t *testing.T) {
	gate := postMemberFSGateFor(t, "Junior-Engineer")
	store := coordination.NewMemoryStore()
	key := strings.Join([]string{"engineer-swarm", "Junior-Engineer", gate.OutputKey}, "/")
	payload := `{"writes":[{"path":"/tmp/junior/report.md","content_type":"scratch","role":"junior-engineer"}]}`
	if err := store.Set(key, []byte(payload)); err != nil {
		t.Fatal(err)
	}
	err := runFSGate(t, gate, store, t.TempDir(), "eng-test")
	if err == nil {
		t.Fatal("Junior-Engineer out-of-scope /tmp write must fail the fs-pollution guard")
	}
}

// TestEngineerSwarmFSGuardOmittedReportFails — negative: when the member
// OMITS the fs-writes report entirely (no coord-store key) the guard
// must fail rather than silently pass the non-reporting member.
func TestEngineerSwarmFSGuardOmittedReportFails(t *testing.T) {
	gate := postMemberFSGateFor(t, "Junior-Engineer")
	store := coordination.NewMemoryStore()
	err := runFSGate(t, gate, store, t.TempDir(), "eng-test")
	if err == nil {
		t.Fatal("omitted fs-writes report must fail the guard at the nested member boundary")
	}
}

// TestEngineerSwarmFSGuardFalseReportFails — negative: a report claiming
// only in-worktree source writes while an out-of-scope write is also
// reported must fail (a report contradicting itself aggregates and
// fails).
func TestEngineerSwarmFSGuardFalseReportFails(t *testing.T) {
	gate := postMemberFSGateFor(t, "Mid-Engineer")
	store := coordination.NewMemoryStore()
	key := strings.Join([]string{"engineer-swarm", "Mid-Engineer", gate.OutputKey}, "/")
	repo := t.TempDir()
	inRepo := filepath.Join(repo, "internal", "app", "app.go")
	payload := `{"writes":[{"path":` + quote(inRepo) + `,"content_type":"source","role":"mid-engineer"},` +
		`{"path":"/etc/tmp-dump.json","content_type":"coordination","role":"mid-engineer"}]}`
	if err := store.Set(key, []byte(payload)); err != nil {
		t.Fatal(err)
	}
	err := runFSGate(t, gate, store, repo, "eng-test")
	if err == nil {
		t.Fatal("mixed report containing an out-of-scope write must fail")
	}
	if !strings.Contains(err.Error(), "/etc/tmp-dump.json") {
		t.Fatalf("failure must name the out-of-scope path, got: %v", err)
	}
}

// TestEngineerSwarmFSGuardCleanSourcePasses — control: an in-worktree
// source write passes, proving the guard discriminates rather than
// failing everything.
func TestEngineerSwarmFSGuardCleanSourcePasses(t *testing.T) {
	gate := postMemberFSGateFor(t, "Mid-Engineer")
	store := coordination.NewMemoryStore()
	key := strings.Join([]string{"engineer-swarm", "Mid-Engineer", gate.OutputKey}, "/")
	repo := t.TempDir()
	payload := `{"writes":[{"path":` + quote(filepath.Join(repo, "internal", "app", "x.go")) + `,"content_type":"source","role":"mid-engineer"}]}`
	if err := store.Set(key, []byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := runFSGate(t, gate, store, repo, "eng-test"); err != nil {
		t.Fatalf("in-worktree source write must pass, got: %v", err)
	}
}

func quote(s string) string { return "\"" + s + "\"" }

// keep the agent import referenced for manifest typing parity with the
// embedded-swarms suite.
var _ = agent.Manifest{}
