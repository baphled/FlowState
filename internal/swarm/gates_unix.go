//go:build unix

package swarm

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// isolateGate starts the gate in its own process group and makes
// cancellation kill the whole group, so a gate's children (make, go
// test, a script) do not outlive a cancelled run. Only cancellation
// signals the group: after a normal exit os/exec never calls Cancel, so
// a daemon a successful gate left behind is untouched, and a later
// gate's cancellation cannot reach it (it lives in the earlier gate's
// group). The group gets SIGKILL, matching the direct-process behaviour
// this replaces. Because the gate no longer shares the terminal's
// foreground group, the context cancellation is what stops it.
func isolateGate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
