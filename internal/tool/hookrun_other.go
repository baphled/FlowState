//go:build !unix

package tool

import (
	"os"
	"os/exec"
	"time"
)

// isolateHook (Windows and other non-Unix platforms) does nothing yet: stopping a hook kills only its direct
// process (the returned stop kills it). The job object that reaches descendants is packet 2b. The returned settle
// function is a no-op.
func isolateHook(cmd *exec.Cmd, _ time.Duration) (func() error, func()) {
	return func() error { return cmd.Process.Kill() }, func() {}
}

// markCloseOnExec is a no-op: Go opens files with non-inheritable handles on these platforms.
func markCloseOnExec(*os.File) error { return nil }
