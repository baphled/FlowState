//go:build unix

package swarm_test

import (
	"context"
	"errors"
	"fmt"
	"os"
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

	// recordedPID blocks until the gate script writes its child's PID into
	// the FIFO at pidfile, so no polling is needed; it panics on timeout,
	// saying whether the gate had already finished (done holds its result).
	recordedPID := func(done <-chan swarm.GatesDecision) int {
		got := make(chan string, 1)
		go func() {
			raw, err := os.ReadFile(pidfile)
			if err != nil {
				got <- ""
				return
			}
			got <- strings.TrimSpace(string(raw))
		}()
		select {
		case text := <-got:
			pid, err := strconv.Atoi(text)
			if err != nil {
				panic("the gate script recorded no child PID")
			}
			return pid
		case <-time.After(10 * time.Second):
			panic(fmt.Sprintf("the gate script never recorded its child PID (gate finished: %t)", len(done) > 0))
		}
	}

	// track kills exactly the PID the spec started, even when the spec fails.
	track := func(pid int) {
		DeferCleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	}

	// script writes a gate script and returns a manifest that runs it
	// through /bin/sh. The script is read by the shell rather than executed
	// itself: executing a file written a moment ago is where a hosted macOS
	// runner sometimes stalled for longer than the PID wait (the first run of
	// a new executable is assessed by the system, and can also fail as busy).
	script := func(body string) swarm.Manifest {
		path := filepath.Join(dir, "gate.sh")
		if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
			panic(err)
		}
		return swarm.Manifest{Name: "s", Scope: "m2", Gates: []swarm.Gate{{Name: "g", Command: "/bin/sh " + path}}}
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
			decision swarm.GatesDecision
			child    int
		)

		BeforeEach(func() {
			m := script("sleep 300 &\necho $! > " + pidfile + "\nwait")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan swarm.GatesDecision, 1)
			go func() { done <- swarm.NewGateRunner().Evaluate(ctx, m) }()
			child = recordedPID(done)
			track(child)
			cancel()
			select {
			case decision = <-done:
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

	Context("when a gate exits normally leaving a child behind", func() {
		var (
			decision swarm.GatesDecision
			child    int
		)

		BeforeEach(func() {
			m := script("sleep 300 </dev/null >/dev/null 2>&1 &\necho $! > " + pidfile)
			done := make(chan swarm.GatesDecision, 1)
			go func() { done <- swarm.NewGateRunner().Evaluate(context.Background(), m) }()
			child = recordedPID(done)
			track(child)
			select {
			case decision = <-done:
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
