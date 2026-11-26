//go:build unix

package tool

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// hookProbe is how often a stopped hook's process group is checked for having emptied during its grace.
const hookProbe = 20 * time.Millisecond

// isolateHook starts the hook in its own process group and returns the function that stops the whole group on cancellation (the hook's deadline or the run's
// context): SIGTERM first, SIGKILL only if the group has not emptied within grace. The caller
// must call the returned settle function once cmd.Run has returned: after a stop it waits until the group has
// emptied or been killed, so the run does not return while a member that ignored SIGTERM still lives. Only a stop
// signals the group; a descendant that left the group is not reached (ADR-012, known gap 2). Adapted from
// swarm.isolateGate.
func isolateHook(cmd *exec.Cmd, grace time.Duration) (stop func() error, settle func()) {
	var stopped chan struct{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stop = func() error {
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
			stopHookGroup(pgid, grace)
		}()
		return nil
	}
	return stop, func() {
		if stopped != nil {
			<-stopped
		}
	}
}

// stopHookGroup returns once the group, already sent SIGTERM, has emptied, and kills it when the grace expires
// first. A group id is not reused while the group has a member, so the kill is safe while every probe found the
// group alive; once a probe finds it gone the id is never signalled again.
func stopHookGroup(pgid int, grace time.Duration) {
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	probe := time.NewTicker(hookProbe)
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

// markCloseOnExec sets FD_CLOEXEC on f, so the descriptor is closed when a hook is exec'd even if f was opened
// without the flag (Go opens files close-on-exec; this does not rely on how a caller opened the lease).
func markCloseOnExec(f *os.File) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	return rc.Control(func(fd uintptr) { syscall.CloseOnExec(int(fd)) })
}
