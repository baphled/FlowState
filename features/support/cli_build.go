package support

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// flowStateTestBinaryPath is the deterministic location of the CLI test
// binary shared by all BDD step glue.
//
// Expected: Derived from os.TempDir so the path respects TMPDIR overrides
// while remaining stable across every scenario in a single test run.
var flowStateTestBinaryPath = filepath.Join(os.TempDir(), "flowstate-test")

var flowStateTestBinaryOnce sync.Once
var flowStateTestBinaryErr error

// EnsureFlowStateTestBinary builds the flowstate CLI exactly once per test
// process into flowStateTestBinaryPath.
//
// Expected: After the first successful call, subsequent calls return
// immediately without re-invoking the Go toolchain, eliminating the
// duplicate 30-90s cold builds previously performed independently by
// steps.go and failure_reason_steps.go.
// Returns: nil once the binary has been built successfully, or the build
// error from the single failed attempt.
// Side effects: Invokes `go build` in the project root and writes the
// binary to the shared temp path.
func EnsureFlowStateTestBinary() error {
	flowStateTestBinaryOnce.Do(func() {
		cwd, err := os.Getwd()
		if err != nil {
			flowStateTestBinaryErr = fmt.Errorf("resolving cwd: %w", err)
			return
		}
		projectRoot := filepath.Dir(filepath.Dir(cwd))
		build := exec.Command("go", "build", "-o", flowStateTestBinaryPath, "./cmd/flowstate")
		build.Dir = projectRoot
		var stderr bytes.Buffer
		build.Stderr = &stderr
		if err := build.Run(); err != nil {
			flowStateTestBinaryErr = fmt.Errorf("building CLI in %s: %w: %s", projectRoot, err, stderr.String())
		}
	})
	return flowStateTestBinaryErr
}

// FlowStateTestBinaryPath reports the shared CLI test binary path.
//
// Expected: Callers use this instead of hardcoding /tmp/flowstate-test so
// the build location and execution location can never diverge.
// Returns: The absolute path of the built test binary.
// Side effects: None.
func FlowStateTestBinaryPath() string {
	return flowStateTestBinaryPath
}
