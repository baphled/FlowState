// Smoke that exercises the v0 ext-gate subprocess runner against the
// in-tree fixture gate (internal/gates/testdata/echo-pass). Reports
// pass/fail + timing. Reusable from the Creating Custom Swarms guide
// §3a verification recipe.
//
// Run from the repo root:
//
//	go run ./tools/smoke/ext_gate_subprocess
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/baphled/flowstate/internal/gates"
	"github.com/baphled/flowstate/internal/swarm"
)

func main() {
	swarm.ResetExtGateRegistryForTest()

	root, err := repoRoot()
	must("repo root", err)
	manifestPath := filepath.Join(root, "internal", "gates", "testdata", "echo-pass", "manifest.yml")

	m, err := gates.LoadManifest(manifestPath)
	must("load manifest", err)
	must("register", swarm.RegisterExtGateFromManifest(m))

	runner, ok := swarm.LookupExtGate("echo-pass")
	if !ok {
		failf("echo-pass did not register")
	}

	start := time.Now()
	resp, err := runner.Evaluate(context.Background(), swarm.ExtGateRequest{
		MemberID: "smoke", When: "post-member", Payload: []byte("hello world"),
	})
	must("evaluate", err)

	fmt.Printf("evaluate: pass=%v reason=%q evidence=%d elapsed=%s\n",
		resp.Pass, resp.Reason, len(resp.Evidence), time.Since(start))
	if !resp.Pass {
		failf("fixture gate did not return pass:true")
	}
	fmt.Println("PASS")
}

func repoRoot() (string, error) {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func must(label string, err error) {
	if err != nil {
		failf("%s: %v", label, err)
	}
}

func failf(format string, args ...any) {
	fmt.Printf("FAIL: "+format+"\n", args...)
	os.Exit(1)
}
