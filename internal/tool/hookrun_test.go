//go:build unix

package tool

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("command hook runner", func() {
	var (
		dir    string
		runner HookRunner
	)

	BeforeEach(func() {
		real, err := filepath.EvalSymlinks(GinkgoT().TempDir())
		if err != nil {
			panic(err)
		}
		dir = real
		runner = HookRunner{Grace: 300 * time.Millisecond}
	})

	// script builds a command running the shell script with args as $1, $2, ...
	script := func(body string, args ...string) HookCommand {
		return HookCommand{Argv: append([]string{"sh", "-c", body, "sh"}, args...), Dir: dir, Timeout: 10 * time.Second}
	}
	run := func(c HookCommand, stdin string) HookResult {
		return runner.Run(context.Background(), c, []byte(stdin))
	}
	// emit is a script that exits 0 and prints text on stdout.
	emit := func(text string) HookCommand { return script("printf '%s' '" + text + "'") }
	alive := func(pid int) bool { return syscall.Kill(pid, 0) == nil }
	// recordedPID waits for a hook script to write a PID into path; it panics when none arrives.
	recordedPID := func(path string) int {
		deadline := time.After(10 * time.Second)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			if raw, err := os.ReadFile(path); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
					return pid
				}
			}
			select {
			case <-deadline:
				panic("the hook recorded no PID")
			case <-tick.C:
			}
		}
	}
	// startAndCancel runs a hook that records a PID, cancels the run once it has, and returns the PID and result.
	startAndCancel := func(body string) (int, HookResult) {
		pidfile := filepath.Join(dir, "pid")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan HookResult, 1)
		go func() { done <- runner.Run(ctx, script(body, pidfile), nil) }()
		pid := recordedPID(pidfile)
		cancel()
		select {
		case res := <-done:
			return pid, res
		case <-time.After(10 * time.Second):
			panic("the hook run did not return after cancellation")
		}
	}

	// objectOfSize builds (the 64 KiB boundary is written literally so a changed cap constant fails the specs) a valid stdout object of exactly size bytes; it panics when the size is wrong.
	objectOfSize := func(size int) string {
		const head, tail = `{"reason":"`, `"}`
		doc := head + strings.Repeat("a", size-len(head)-len(tail)) + tail
		if len(doc) != size {
			panic("the stdout object has the wrong size")
		}
		return doc
	}

	Describe("the exit contract", func() {
		It("allows an exit 0 with empty stdout", func() {
			Expect(run(script("exit 0"), "").Verdict).To(Equal(HookAllow))
		})
		It("denies an exit 2", func() {
			Expect(run(script("echo blocked >&2; exit 2"), "").Verdict).To(Equal(HookDeny))
		})
		It("takes the deny reason from stderr", func() {
			Expect(run(script("printf blocked >&2; exit 2"), "").Reason).To(Equal("blocked"))
		})
		It("truncates a stderr reason to 1 KiB", func() {
			Expect(run(script("head -c 3000 /dev/zero | tr '\\0' r >&2; exit 2"), "").Reason).To(HaveLen(HookReasonCap))
		})
		It("strips control characters from a reason", func() {
			Expect(run(script("printf 'a\\033[1mb\\nc' >&2; exit 2"), "").Reason).To(Equal("a[1mbc"))
		})
		DescribeTable("treats any other exit status as a failure, not a deny",
			func(status string) {
				Expect(run(script("exit "+status), "").Verdict).To(Equal(HookFailure))
			},
			Entry("exit 1", "1"),
			Entry("exit 3", "3"),
			Entry("exit 127", "127"),
		)
		It("names an ordinary non-zero exit exit_status", func() {
			Expect(run(script("exit 1"), "").Failure).To(Equal(HookFailureExit))
		})
		It("reports the exit code", func() {
			Expect(run(script("exit 3"), "").ExitCode).To(Equal(3))
		})
		It("keeps stdout line breaks and drops other control characters", func() {
			Expect(run(script("printf 'x\\033y\\n'; exit 1"), "").Stdout).To(Equal("xy\n"))
		})
		It("caps the kept stderr at 8 KiB", func() {
			Expect(run(script("head -c 20000 /dev/zero | tr '\\0' b >&2; exit 1"), "").Stderr).To(HaveLen(HookStderrCap))
		})
		It("fails a hook that cannot be started", func() {
			Expect(run(HookCommand{Argv: []string{filepath.Join(dir, "missing")}}, "").Failure).To(Equal(HookFailureStart))
		})
		It("refuses a relative executable path", func() {
			Expect(run(HookCommand{Argv: []string{"./hook.sh"}}, "").Failure).To(Equal(HookFailureStart))
		})
		It("refuses an empty argv", func() {
			Expect(run(HookCommand{}, "").Failure).To(Equal(HookFailureStart))
		})
		It("runs in the given directory", func() {
			Expect(run(script("pwd"), "").Stdout).To(Equal(dir + "\n"))
		})
	})

	Describe("the stdout object", func() {
		It("allows an explicit allow", func() {
			Expect(run(emit(`{"decision":"allow"}`), "").Verdict).To(Equal(HookAllow))
		})
		It("denies a stdout deny", func() {
			Expect(run(emit(`{"decision":"deny","reason":"no"}`), "").Verdict).To(Equal(HookDeny))
		})
		It("carries the stdout deny reason", func() {
			Expect(run(emit(`{"decision":"deny","reason":"no"}`), "").Reason).To(Equal("no"))
		})
		DescribeTable("fails an unacceptable stdout",
			func(out, code string) {
				Expect(run(emit(out), "").Failure).To(Equal(code))
			},
			Entry("a deny without a reason", `{"decision":"deny"}`, HookFailureInvalidOutput),
			Entry("a deny with a blank reason", `{"decision":"deny","reason":"  "}`, HookFailureInvalidOutput),
			Entry("text that is not JSON", `allowed`, HookFailureInvalidOutput),
			Entry("two objects", `{"decision":"allow"}{"decision":"allow"}`, HookFailureInvalidOutput),
			Entry("an unknown field", `{"nope":"allow"}`, HookFailureInvalidOutput),
			Entry("an unknown decision", `{"decision":"maybe"}`, HookFailureInvalidOutput),
			Entry("args at a point that forbids them", `{"args":{"a":1}}`, HookFailureFieldNotPermitted),
			Entry("stop at a point that forbids it", `{"stop":true}`, HookFailureFieldNotPermitted),
			Entry("stop false at a point that forbids it", `{"stop":false}`, HookFailureFieldNotPermitted),
			Entry("context", `{"context":"hi"}`, HookFailureContextUnsupported),
		)
		It("fails a context even where args and stop are permitted", func() {
			c := emit(`{"context":"hi"}`)
			c.AllowArgs, c.AllowStop = true, true
			Expect(run(c, "").Failure).To(Equal(HookFailureContextUnsupported))
		})
		It("returns permitted args", func() {
			c := emit(`{"args":{"a":1}}`)
			c.AllowArgs = true
			Expect(string(run(c, "").Output.Args)).To(Equal(`{"a":1}`))
		})
		It("fails permitted args that are not an object", func() {
			c := emit(`{"args":[1]}`)
			c.AllowArgs = true
			Expect(run(c, "").Failure).To(Equal(HookFailureInvalidOutput))
		})
		It("returns a permitted stop", func() {
			c := emit(`{"stop":true}`)
			c.AllowStop = true
			Expect(run(c, "").Output.Stop).To(BeTrue())
		})
		It("ignores stdout on exit 2", func() {
			Expect(run(script(`printf '{"decision":"allow"}'; echo why >&2; exit 2`), "").Verdict).To(Equal(HookDeny))
		})
	})

	Describe("the caps", func() {
		It("fails stdout over 64 KiB even when it would be a valid object", func() {
			body := `printf '{"decision":"allow","reason":"'; head -c 70000 /dev/zero | tr '\0' a; printf '"}'`
			Expect(run(script(body), "").Failure).To(Equal(HookFailureOutputTooLarge))
		})
		It("accepts stdout of exactly 64 KiB", func() {
			Expect(run(script(`printf '%s' "$1"`, objectOfSize(64<<10)), "").Verdict).To(Equal(HookAllow))
		})
		It("fails stdout one byte over 64 KiB", func() {
			Expect(run(script(`printf '%s' "$1"`, objectOfSize(64<<10+1)), "").Failure).
				To(Equal(HookFailureOutputTooLarge))
		})
		It("stops a hook that floods stdout instead of waiting for its deadline", func() {
			Expect(run(script("yes"), "").Duration).To(BeNumerically("<", 5*time.Second))
		})
		It("refuses a stdin document over 256 KiB", func() {
			Expect(run(script("exit 0"), strings.Repeat("a", HookStdinCap+1)).Failure).To(Equal(HookFailureInputTooLarge))
		})
		It("does not start a hook for an oversize stdin document", func() {
			marker := filepath.Join(dir, "started")
			run(script(`touch "$1"`, marker), strings.Repeat("a", HookStdinCap+1))
			Expect(marker).NotTo(BeAnExistingFile())
		})
		It("accepts a stdin document of exactly 256 KiB", func() {
			Expect(run(script("cat >/dev/null"), strings.Repeat("a", HookStdinCap)).Verdict).To(Equal(HookAllow))
		})
		It("writes the stdin document to the hook", func() {
			Expect(run(script("cat"), `{"decision":"deny","reason":"echoed"}`).Reason).To(Equal("echoed"))
		})
		It("bounds a hook that never reads a large stdin document by the deadline", func() {
			c := script("sleep 30")
			c.Timeout = 300 * time.Millisecond
			Expect(run(c, strings.Repeat("a", 200<<10)).Failure).To(Equal(HookFailureTimeout))
		})
	})

	Describe("the environment", func() {
		It("does not pass a provider key", func() {
			GinkgoT().Setenv("ANTHROPIC_API_KEY", "sk-provider-secret")
			Expect(run(script("env; exit 1"), "").Stdout).NotTo(ContainSubstring("sk-provider-secret"))
		})
		It("does not pass an unlisted variable", func() {
			GinkgoT().Setenv("HOOK_UNLISTED", "visible")
			Expect(run(script("env; exit 1"), "").Stdout).NotTo(ContainSubstring("HOOK_UNLISTED"))
		})
		It("does not pass FLOWSTATE_ variables", func() {
			GinkgoT().Setenv("FLOWSTATE_EXEC_ENABLED", "1")
			Expect(run(script("env; exit 1"), "").Stdout).NotTo(ContainSubstring("FLOWSTATE_"))
		})
		It("passes PATH", func() {
			Expect(run(script("env; exit 1"), "").Stdout).To(ContainSubstring("PATH="))
		})
		It("passes a listed variable", func() {
			GinkgoT().Setenv("HOOK_LISTED", "visible")
			c := script("env; exit 1")
			c.EnvNames = []string{"HOOK_LISTED"}
			Expect(run(c, "").Stdout).To(ContainSubstring("HOOK_LISTED=visible"))
		})
		DescribeTable("refuses a listed name that looks like a secret or an engine variable",
			func(name string) {
				c := script("exit 0")
				c.EnvNames = []string{name}
				Expect(run(c, "").Failure).To(Equal(HookFailureEnvRefused))
			},
			Entry("a key", "ANTHROPIC_API_KEY"),
			Entry("a token", "GITHUB_TOKEN"),
			Entry("a password", "DB_PASSWORD"),
			Entry("an engine variable", "FLOWSTATE_HOME"),
			Entry("a lower-case engine variable", "flowstate_home"),
			Entry("an assignment", "A=B"),
			Entry("an empty name", ""),
		)
	})

	Describe("stopping a hook", func() {
		It("fails a hung hook at its deadline", func() {
			c := script("sleep 30")
			c.Timeout = 300 * time.Millisecond
			Expect(run(c, "").Failure).To(Equal(HookFailureTimeout))
		})
		It("clamps a deadline above the cap to 60 seconds", func() {
			Expect(hookTimeout(time.Hour)).To(Equal(MaxHookTimeout))
		})
		It("gives a hook with no deadline the 10 second default", func() {
			Expect(hookTimeout(0)).To(Equal(DefaultHookTimeout))
		})
		It("reports a cancelled run as canceled", func() {
			_, res := startAndCancel(`sleep 30 & echo $! > "$1"; wait`)
			Expect(res.Failure).To(Equal(HookFailureCanceled))
		})
		It("fails a timed-out hook that exits 0 on SIGTERM as a timeout", func() {
			c := script(`trap 'exit 0' TERM; sleep 30 & wait`)
			c.Timeout = 300 * time.Millisecond
			Expect(run(c, "").Failure).To(Equal(HookFailureTimeout))
		})
		It("fails a timed-out hook that prints an allow on SIGTERM as a timeout", func() {
			c := script(`trap 'printf "{\"decision\":\"allow\"}"; exit 0' TERM; sleep 30 & wait`)
			c.Timeout = 300 * time.Millisecond
			Expect(run(c, "").Failure).To(Equal(HookFailureTimeout))
		})
		It("fails a cancelled hook that exits 0 on SIGTERM as canceled", func() {
			_, res := startAndCancel(`trap 'exit 0' TERM; sleep 30 & echo $! > "$1"; wait`)
			Expect(res.Failure).To(Equal(HookFailureCanceled))
		})
		It("still allows a hook that exits 0 at 90% of its deadline", func() {
			c := script("sleep 1.8")
			c.Timeout = 2 * time.Second
			Expect(run(c, "").Verdict).To(Equal(HookAllow))
		})
		It("stops a member of the hook's process group", func() {
			pid, _ := startAndCancel(`sleep 30 & echo $! > "$1"; wait`)
			Eventually(func() bool { return alive(pid) }).WithTimeout(5 * time.Second).WithPolling(20 * time.Millisecond).
				Should(BeFalse())
		})
		It("kills a group member that ignores SIGTERM once the grace has passed", func() {
			pid, _ := startAndCancel(`(trap '' TERM; exec sleep 30) & echo $! > "$1"; wait`)
			Eventually(func() bool { return alive(pid) }).WithTimeout(5 * time.Second).WithPolling(20 * time.Millisecond).
				Should(BeFalse())
		})
		It("sends SIGTERM first so the hook can clean up", func() {
			marker := filepath.Join(dir, "term")
			startAndCancel(`trap 'echo t > ` + marker + `; exit 0' TERM; sleep 30 & echo $! > "$1"; wait`)
			Expect(marker).To(BeAnExistingFile())
		})
		It("fails a hook whose descendant holds the output pipes after it exits", func() {
			Expect(run(script("sleep 2 &"), "").Failure).To(Equal(HookFailureOutputIncomplete))
		})
	})

	Describe("the lease descriptor", func() {
		var lease *os.File

		BeforeEach(func() {
			f, err := os.Create(filepath.Join(dir, "lease.lock"))
			if err != nil {
				panic(err)
			}
			DeferCleanup(f.Close)
			// Simulate a descriptor opened without close-on-exec.
			if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_SETFD, 0); errno != 0 {
				panic(errno)
			}
			lease = f
		})

		probe := func() HookCommand {
			return script(`[ -e /dev/fd/"$1" ] && printf '{"decision":"deny","reason":"inherited"}'; exit 0`,
				strconv.Itoa(int(lease.Fd())))
		}

		It("is inherited by a child when nothing marks it (the check itself works)", func() {
			Expect(run(probe(), "").Verdict).To(Equal(HookDeny))
		})
		It("is not inherited by a hook", func() {
			runner.LeaseFiles = []*os.File{lease}
			Expect(run(probe(), "").Verdict).To(Equal(HookAllow))
		})
		It("fails closed when a lease descriptor cannot be marked", func() {
			closed, err := os.Open(filepath.Join(dir, "lease.lock"))
			if err != nil {
				panic(err)
			}
			if err := closed.Close(); err != nil {
				panic(err)
			}
			runner.LeaseFiles = []*os.File{closed}
			Expect(run(script("exit 0"), "").Failure).To(Equal(HookFailureLeaseFD))
		})
	})
})
