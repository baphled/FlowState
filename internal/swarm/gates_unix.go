//go:build unix

package swarm

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// gateProbe is how often a cancelled gate's process group is checked for
// having emptied while it is given its grace.
const gateProbe = 20 * time.Millisecond

// isolateGate starts the gate in its own process group and makes
// cancellation stop the whole group, so a gate's children (make, go
// test, a script) do not outlive a cancelled run. Only cancellation
// signals the group: after a normal exit os/exec never calls Cancel, so
// a daemon a successful gate left behind is untouched, and a later
// gate's cancellation cannot reach it (it lives in the earlier gate's
// group).
//
// The stop is graceful: the group gets SIGTERM, and SIGKILL only if it
// has not emptied within grace (see stopGroup). The caller must call the
// returned settle function once cmd.Run has returned: after a
// cancellation it waits until the group has emptied or been killed, so
// the run does not return while a member that ignored SIGTERM is still
// running, even when the gate itself exited at once. Because the gate no
// longer shares the terminal's foreground group, the context
// cancellation is what stops it.
//
// os/exec hands the result of Cancel to Wait over a channel, so by the
// time cmd.Run has returned Cancel has either finished or will never be
// called; settle may read what Cancel set without a lock.
func isolateGate(cmd *exec.Cmd, grace time.Duration) (settle func()) {
	var stopped chan struct{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		pgid := cmd.Process.Pid
		if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		stopped = make(chan struct{})
		go func() {
			defer close(stopped)
			stopGroup(pgid, grace)
		}()
		return nil
	}
	return func() {
		if stopped != nil {
			<-stopped
		}
	}
}

// stopGroup gives a process group that was sent SIGTERM its grace: it
// returns as soon as the group has emptied and kills the group when the
// grace expires first.
//
// A process group id cannot be reused while the group has a member, so
// the kill is safe for as long as every probe has found the group alive,
// whether or not the gate itself has been reaped. Once a probe finds the
// group gone its id may be reused, and it is never signalled again.
func stopGroup(pgid int, grace time.Duration) {
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	probe := time.NewTicker(gateProbe)
	defer probe.Stop()
	for {
		select {
		case <-deadline.C:
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			return
		case <-probe.C:
			if err := syscall.Kill(-pgid, 0); err != nil {
				return
			}
		}
	}
}
