//go:build unix

package swarm_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("gate runner process groups", func() {
	var (
		dir     string
		pidfile string
	)

	// alive probes a PID with signal 0 (no signal is delivered).
	alive := func(pid int) bool {
		return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}

	// start evaluates the manifest in the background under ctx. The decision
	// is only safe to read once done is closed.
	start := func(runner *swarm.GateRunner, ctx context.Context, m swarm.Manifest) (<-chan struct{}, *swarm.GatesDecision) {
		done := make(chan struct{})
		decision := new(swarm.GatesDecision)
		go func() {
			defer close(done)
			*decision = runner.Evaluate(ctx, m)
		}()
		return done, decision
	}

	// recordedPID blocks until the gate script writes a PID line into the FIFO
	// at path. The FIFO is opened read-write so the reader never sees EOF and
	// the cleanup's Close unblocks it; the path is passed by value so a reader
	// left over from a timed-out spec cannot touch the next spec's state. It
	// panics on timeout, saying what the gate decided if it had finished.
	recordedPID := func(path string, done <-chan struct{}, decision *swarm.GatesDecision) int {
		fifo, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			panic(err)
		}
		DeferCleanup(fifo.Close)
		got := make(chan string, 1)
		go func() {
			line, _ := bufio.NewReader(fifo).ReadString('\n')
			got <- strings.TrimSpace(line)
		}()
		select {
		case text := <-got:
			pid, err := strconv.Atoi(text)
			if err != nil {
				panic("the gate script recorded no child PID")
			}
			return pid
		case <-time.After(10 * time.Second):
			finished := "no"
			select {
			case <-done:
				finished = fmt.Sprintf("yes, decision %+v", *decision)
			default:
			}
			panic("the gate script never recorded its child PID (gate finished: " + finished + ")")
		}
	}

	// groupMembers lists the processes ps reports in the process group, one
	// per line (pid, parent, group, state, command), for a failure message.
	groupMembers := func(pgid int) string {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,pgid=,stat=,command=").Output()
		if err != nil {
			return "ps failed: " + err.Error()
		}
		var members []string
		for _, line := range strings.Split(string(out), "\n") {
			if fields := strings.Fields(line); len(fields) > 2 && fields[2] == strconv.Itoa(pgid) {
				members = append(members, strings.TrimSpace(line))
			}
		}
		return strings.Join(members, "\n")
	}

	// write stores a script file the shell will read. The script is read by
	// the shell rather than executed itself: executing a file written a moment
	// ago is where a hosted macOS runner sometimes stalled for longer than the
	// PID wait (the first run of a new executable is assessed by the system,
	// and can also fail as busy).
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
			panic(err)
		}
		return path
	}

	// script writes the gate script and returns a manifest that runs it
	// through /bin/sh. Children the scripts start are bounded sleeps that end
	// on their own, so no spec has to signal a bare PID it cannot prove is
	// still its own.
	script := func(body string) swarm.Manifest {
		path := write("gate.sh", body)
		return swarm.Manifest{Name: "s", Scope: "m2", Gates: []swarm.Gate{{Name: "g", Command: "/bin/sh " + path}}}
	}

	// cancelledCtx returns a context cancelled when the spec ends, so a gate
	// that outlives a failed spec is stopped through the runner itself.
	cancelledCtx := func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		return ctx, cancel
	}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		pidfile = filepath.Join(dir, "child.pid")
		if err := syscall.Mkfifo(pidfile, 0o600); err != nil {
			panic(err)
		}
	})

	Context("when a gate that started a child is cancelled", func() {
		var (
			decision *swarm.GatesDecision
			child    int
		)

		BeforeEach(func() {
			m := script("sleep 20 &\necho $! > " + pidfile + "\nwait")
			ctx, cancel := cancelledCtx()
			done, result := start(swarm.NewGateRunner(), ctx, m)
			child = recordedPID(pidfile, done, result)
			cancel()
			select {
			case <-done:
				decision = result
			case <-time.After(15 * time.Second):
				panic("the gate was not stopped by cancellation")
			}
		})

		It("stops the gate's child process", func() {
			Eventually(func() bool { return alive(child) }).
				WithTimeout(10 * time.Second).WithPolling(10 * time.Millisecond).Should(BeFalse())
		})

		It("records the gate as failed", func() {
			Expect(decision.Outcomes[0].Passed).To(BeFalse())
		})

		It("records the gate as interrupted", func() {
			Expect(decision.Outcomes[0].Interrupted).To(BeTrue())
		})
	})

	Context("when a cancelled gate and its child handle SIGTERM and exit", func() {
		var (
			gateMark  string
			childMark string
			child     int
			elapsed   time.Duration
			lingering string
		)

		BeforeEach(func() {
			gateMark = filepath.Join(dir, "gate.term")
			childMark = filepath.Join(dir, "child.term")
			// The grandchild records its own PID once it is a fresh shell with
			// the default SIGTERM action. Recording "$!" from the child instead
			// lets the cancellation arrive while the forked shell has not yet
			// exec'd: it still carries the child's trap handler, which swallows
			// the signal, and the sleep then lives until the group is killed.
			grandchild := write("grandchild.sh", "echo $$ > "+pidfile+"\nexec sleep 20")
			childScript := write("child.sh", "trap 'echo t > "+childMark+"; exit 0' TERM\n"+
				"/bin/sh "+grandchild+" &\nwait")
			m := script("trap 'echo t > " + gateMark + "; wait; exit 0' TERM\n/bin/sh " + childScript + " &\nwait")
			ctx, cancel := cancelledCtx()
			done, result := start(swarm.NewGateRunner(), ctx, m)
			child = recordedPID(pidfile, done, result)
			group, gerr := syscall.Getpgid(child)
			if gerr != nil {
				panic(gerr)
			}
			began := time.Now()
			cancel()
			lingering = ""
			select {
			case <-done:
			case <-time.After(1500 * time.Millisecond):
				// Too slow for a group whose members all exit on SIGTERM:
				// record who is still in it, for the failure message.
				lingering = groupMembers(group)
				select {
				case <-done:
				case <-time.After(15 * time.Second):
					panic("the gate was not stopped by cancellation")
				}
			}
			elapsed = time.Since(began)
		})

		It("returns once the group has stopped, without waiting out the grace", func() {
			Expect(elapsed).To(BeNumerically("<", 2*time.Second), "still in the gate's process group after 1.5s:\n"+lingering)
		})

		It("runs the gate's SIGTERM handler instead of killing it", func() {
			Expect(gateMark).To(BeAnExistingFile())
		})

		It("runs the child's SIGTERM handler instead of killing it", func() {
			Expect(childMark).To(BeAnExistingFile())
		})

		It("stops the child's own child", func() {
			Eventually(func() bool { return alive(child) }).
				WithTimeout(10 * time.Second).WithPolling(10 * time.Millisecond).Should(BeFalse())
		})
	})

	Context("when a cancelled gate and its child ignore SIGTERM", func() {
		const grace = 500 * time.Millisecond
		var (
			done    <-chan struct{}
			result  *swarm.GatesDecision
			child   int
			cancel  context.CancelFunc
			elapsed time.Duration
		)

		BeforeEach(func() {
			childScript := write("child.sh", "trap '' TERM\nsleep 20 &\necho $! > "+pidfile+"\nwait")
			m := script("trap '' TERM\n/bin/sh " + childScript + " &\nwait")
			var ctx context.Context
			ctx, cancel = cancelledCtx()
			done, result = start(swarm.NewGateRunnerWithGrace(grace), ctx, m)
			child = recordedPID(pidfile, done, result)
		})

		It("does not kill the gate before the grace has passed", func() {
			cancel()
			Consistently(done).WithTimeout(grace / 3).WithPolling(10 * time.Millisecond).ShouldNot(BeClosed())
		})

		It("kills the gate's child once the grace has passed", func() {
			cancel()
			Eventually(func() bool { return alive(child) }).
				WithTimeout(10 * time.Second).WithPolling(10 * time.Millisecond).Should(BeFalse())
		})

		It("returns within the five second wait bound", func() {
			began := time.Now()
			cancel()
			select {
			case <-done:
				elapsed = time.Since(began)
			case <-time.After(15 * time.Second):
				panic("the gate was not stopped by cancellation")
			}
			Expect(elapsed).To(BeNumerically("<", 5*time.Second))
		})

		It("records the gate as interrupted", func() {
			cancel()
			Eventually(func() bool {
				select {
				case <-done:
					return result.Outcomes[0].Interrupted
				default:
					return false
				}
			}).WithTimeout(10 * time.Second).WithPolling(10 * time.Millisecond).Should(BeTrue())
		})
	})

	Context("when a cancelled gate exits on SIGTERM but its child ignores it", func() {
		const grace = 500 * time.Millisecond
		var (
			done   <-chan struct{}
			child  int
			cancel context.CancelFunc
		)

		BeforeEach(func() {
			childScript := write("child.sh", "trap '' TERM\nsleep 20 &\necho $! > "+pidfile+"\nwait")
			m := script("/bin/sh " + childScript + " &\nwait")
			var (
				ctx    context.Context
				result *swarm.GatesDecision
			)
			ctx, cancel = cancelledCtx()
			done, result = start(swarm.NewGateRunnerWithGrace(grace), ctx, m)
			child = recordedPID(pidfile, done, result)
		})

		It("kills the surviving child once the grace has passed", func() {
			cancel()
			Eventually(func() bool { return alive(child) }).
				WithTimeout(10 * time.Second).WithPolling(10 * time.Millisecond).Should(BeFalse())
		})

		It("does not return while the surviving child still has its grace", func() {
			cancel()
			Consistently(done).WithTimeout(grace / 3).WithPolling(10 * time.Millisecond).ShouldNot(BeClosed())
		})
	})

	Context("when the requested grace is outside the bound", func() {
		// stubborn starts a gate that ignores SIGTERM under the given grace.
		stubborn := func(grace time.Duration) (<-chan struct{}, context.CancelFunc) {
			m := script("trap '' TERM\nsleep 20 &\necho $! > " + pidfile + "\nwait")
			ctx, cancel := cancelledCtx()
			done, result := start(swarm.NewGateRunnerWithGrace(grace), ctx, m)
			recordedPID(pidfile, done, result)
			return done, cancel
		}

		It("still returns within the five second wait bound when the grace asked for is longer", func() {
			done, cancel := stubborn(30 * time.Second)
			began := time.Now()
			cancel()
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				panic("the gate was not stopped by cancellation")
			}
			Expect(time.Since(began)).To(BeNumerically("<", 5*time.Second))
		})

		It("gives the default grace, not an immediate kill, when the grace asked for is zero", func() {
			done, cancel := stubborn(0)
			cancel()
			Consistently(done).WithTimeout(500 * time.Millisecond).WithPolling(10 * time.Millisecond).ShouldNot(BeClosed())
		})
	})

	Context("when a gate exits normally leaving a child behind", func() {
		var (
			decision *swarm.GatesDecision
			child    int
		)

		BeforeEach(func() {
			m := script("sleep 20 </dev/null >/dev/null 2>&1 &\necho $! > " + pidfile)
			done, result := start(swarm.NewGateRunner(), context.Background(), m)
			child = recordedPID(pidfile, done, result)
			select {
			case <-done:
				decision = result
			case <-time.After(15 * time.Second):
				panic("the gate did not exit")
			}
		})

		It("does not signal the child", func() {
			Consistently(func() bool { return alive(child) }).
				WithTimeout(500 * time.Millisecond).WithPolling(50 * time.Millisecond).Should(BeTrue())
		})

		It("passes the gate", func() {
			Expect(decision.Outcomes[0].Passed).To(BeTrue())
		})

		It("does not flag the gate as interrupted", func() {
			Expect(decision.Outcomes[0].Interrupted).To(BeFalse())
		})
	})
})
