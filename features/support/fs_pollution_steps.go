//go:build e2e

package support

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/swarm"
)

// fsPollutionState carries the per-scenario fs-pollution-guard BDD
// state: the runner under test, the repo root it is anchored at, the
// last error, and the fake coord-store payload.
type fsPollutionState struct {
	runner swarm.GateRunner
	root   string
	home   string
	err    error
}

// fsStore is a coordination.Store stub whose Get returns the recorded
// report payload so the gate exercises its real read path.
type fsStore struct {
	payload string
}

func (s fsStore) Get(key string) ([]byte, error) {
	if s.payload == "" {
		return nil, coordination.ErrKeyNotFound
	}
	return []byte(s.payload), nil
}

func (s fsStore) Set(key string, value []byte) error { return nil }

func (s fsStore) List(prefix string) ([]string, error) { return nil, nil }

func (s fsStore) Delete(key string) error { return nil }

func (s fsStore) Increment(key string) (int, error) { return 0, nil }

func (s fsStore) Exists(key string) (bool, error) { return s.payload != "", nil }

// RegisterFSPollutionSteps wires the fs-pollution-guard step
// definitions onto the godog scenario context.
//
// Expected: parameters for RegisterFSPollutionSteps.
//
// Side effects: None.
func RegisterFSPollutionSteps(ctx *godog.ScenarioContext) {
	st := &fsPollutionState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		st.root = tDir()
		st.home = tDir()
		st.err = nil
		st.runner = swarm.NewFSPollutionRunner(st.root, []string{filepath.Join(st.home, "vaults", "baphled")})
		return c, nil
	})
	ctx.Step(`^the fs-pollution-guard gate runner is registered$`, st.runnerIsRegistered)
	ctx.Step(`^a member reports a write to "([^"]*)" with content type "([^"]*)"$`, st.memberReportsWrite)
	ctx.Step(`^a member with role "([^"]*)" reports a write to "([^"]*)" with content type "([^"]*)"$`, st.memberWithRoleReportsWrite)
	ctx.Step(`^the fs-pollution-guard gate should fail$`, st.gateShouldFail)
	ctx.Step(`^the fs-pollution-guard gate should pass$`, st.gateShouldPass)
	ctx.Step(`^the failure should name the offending path "([^"]*)"$`, st.failureNamesPath)
	ctx.Step(`^an untracked file "([^"]*)" exists in the repo$`, st.untrackedFileExists)
	ctx.Step(`^the pre-commit pollution check should report it as blocked$`, st.preCommitBlocks)
}

func tDir() string {
	d, err := os.MkdirTemp("", "fs-pollution-bdd-*")
	if err != nil {
		return "."
	}
	return d
}

func (s *fsPollutionState) gateSpec() swarm.GateSpec {
	return swarm.GateSpec{Name: "no-fs-pollution", Kind: "builtin:fs-pollution-guard", When: "post-member", Target: "worker"}
}

func (s *fsPollutionState) args(store coordination.Store) swarm.GateArgs {
	return swarm.GateArgs{SwarmID: "s", ChainPrefix: "p", MemberID: "worker", CoordStore: store}
}

func (s *fsPollutionState) runnerIsRegistered() error {
	if s.runner == nil {
		return errors.New("runner not registered")
	}
	return nil
}

func (s *fsPollutionState) run(payload string) error {
	s.err = s.runner.Run(context.Background(), s.gateSpec(), s.args(fsStore{payload: payload}))
	return nil
}

func (s *fsPollutionState) memberReportsWrite(path, contentType string) error {
	return s.memberWithRoleReportsWrite("junior", path, contentType)
}

func (s *fsPollutionState) memberWithRoleReportsWrite(role, path, contentType string) error {
	resolved := path
	if i := strings.Index(resolved, "vaults/baphled"); i >= 0 {
		resolved = filepath.Join(s.home, "vaults", "baphled", filepath.Base(resolved))
	}
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(s.root, resolved)
	}
	return s.run(`{"writes":[{"path":"` + strings.ReplaceAll(resolved, `\`, `\\`) + `","content_type":"` + contentType + `","role":"` + role + `"}]}`)
}

func (s *fsPollutionState) gateShouldFail() error {
	if s.err == nil {
		return errors.New("expected gate failure, got pass")
	}
	return nil
}

func (s *fsPollutionState) gateShouldPass() error {
	if s.err != nil {
		return s.err
	}
	return nil
}

func (s *fsPollutionState) failureNamesPath(path string) error {
	if s.err == nil || !strings.Contains(s.err.Error(), path) {
		return errors.New("failure should name offending path: " + path)
	}
	return nil
}

func (s *fsPollutionState) untrackedFileExists(name string) error {
	full := filepath.Join(s.root, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte("{}\n"), 0o644)
}

func (s *fsPollutionState) preCommitBlocks() error {
	blocked, reason := swarm.PreCommitPollutionCheck(s.root, []string{"stray-report.json"})
	if !blocked {
		return errors.New("expected stray-report.json to be blocked")
	}
	if !strings.Contains(reason, "stray-report.json") {
		return errors.New("reason should name the offender, got: " + reason)
	}
	return nil
}
