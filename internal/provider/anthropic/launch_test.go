package anthropic_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
)

const (
	helperEnv = "FLOWSTATE_ANTHROPIC_HELPER" // selects the fake claude behaviour
	pidEnv    = "FLOWSTATE_ANTHROPIC_PIDFILE"
	tailMark  = "TAIL-MARK"
)

// init turns the test binary into the fake claude when helperEnv is set. It
// runs before flag parsing, so the headless flags the provider passes are
// never seen by the testing package. The helper sleeps stand in for a hung child.
func init() {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		return
	}
	if f := os.Getenv(pidEnv); f != "" {
		_ = os.WriteFile(f, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
	say := func(l string) { _, _ = fmt.Fprintln(os.Stdout, l) }
	switch mode {
	case "echo":
		in, _ := io.ReadAll(os.Stdin)
		say(helperDelta(string(in)))
		say(okEnd)
	case "cwd":
		wd, _ := os.Getwd()
		say(helperDelta(wd))
		say(okEnd)
	case "hang":
		say(deltaA)
		<-time.After(time.Minute)
	case "result-hang":
		say(okEnd)
		<-time.After(time.Minute)
	case "stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 200000)+tailMark)
		os.Exit(3)
	case "orphan":
		// A descendant inherits stdout and outlives the child, holding the pipe open.
		gc := exec.Command(os.Args[0])
		gc.Env = append(os.Environ(), helperEnv+"=quiet", pidEnv+"=")
		gc.Stdout = os.Stdout
		if err := gc.Start(); err != nil {
			os.Exit(4)
		}
		_ = os.WriteFile(os.Getenv(pidEnv)+".gc", []byte(strconv.Itoa(gc.Process.Pid)), 0o600)
		say(deltaA)
		<-time.After(time.Minute)
	case "quiet":
		<-time.After(time.Minute)
	case "flood":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 4096))
		<-time.After(time.Minute)
	}
	os.Exit(0)
}

func helperDelta(s string) string {
	b, _ := json.Marshal(s)
	return strings.Replace(deltaA, `"A"`, string(b), 1)
}

// alive reports whether a process with this pid still exists.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		_ = p.Release()
		return true
	}
	return p.Signal(syscall.Signal(0)) == nil
}

var _ = Describe("the real launcher", func() {
	var (
		p       *anthropic.Subscription
		pidFile string
	)

	BeforeEach(func() {
		self, err := os.Executable()
		if err != nil {
			panic(err)
		}
		pidFile = filepath.Join(GinkgoT().TempDir(), "pid")
		GinkgoT().Setenv(pidEnv, pidFile)
		p = anthropic.NewSubscription(self, anthropic.ExecLauncher, anthropic.WithResultGrace(200*time.Millisecond), anthropic.WithMaxLine(1024))
	})

	chat := func(mode, prompt string) (*provider.ChatResponse, error) {
		GinkgoT().Setenv(helperEnv, mode)
		return p.Chat(context.Background(), userReq(prompt))
	}
	content := func(mode, prompt string) string {
		resp, err := chat(mode, prompt)
		if err != nil {
			panic(err)
		}
		return resp.Content
	}
	// readPID returns the pid the helper recorded before it printed anything.
	readPID := func() int {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			panic(err)
		}
		pid, err := strconv.Atoi(string(b))
		if err != nil {
			panic(err)
		}
		return pid
	}
	// startHang starts a child (mode) that prints one line and then never answers.
	startHang := func(mode string) (pid int, cancel context.CancelFunc, closed chan struct{}) {
		GinkgoT().Setenv(helperEnv, mode)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch := p.Stream(ctx, userReq("hi"))
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			panic("child never started")
		}
		closed = make(chan struct{})
		go func() { drain(ch); close(closed) }()
		return readPID(), cancel, closed
	}
	isGone := func(pid int) func() bool { return func() bool { return !alive(pid) } }

	It("delivers the transcript on stdin", func() {
		Expect(content("echo", "/usage")).To(Equal("User:\n/usage"))
	})

	It("runs the child in the private working directory", func() {
		Expect(filepath.Base(content("cwd", "hi"))).To(HavePrefix(anthropic.WorkDirPrefix))
	})

	It("removes the private working directory after the turn", func() {
		dir := content("cwd", "hi")
		Eventually(func() error { _, err := os.Stat(dir); return err }).
			WithTimeout(10 * time.Second).WithPolling(20 * time.Millisecond).Should(MatchError(os.ErrNotExist))
	})

	It("does not block the call when a silent child is cancelled", func() {
		_, cancel, closed := startHang("hang")
		cancel()
		Eventually(closed).WithTimeout(10 * time.Second).WithPolling(10 * time.Millisecond).Should(BeClosed())
	})

	It("kills a silent child on cancellation", func() {
		pid, cancel, _ := startHang("hang")
		cancel()
		Eventually(isGone(pid)).WithTimeout(10 * time.Second).WithPolling(20 * time.Millisecond).Should(BeTrue())
	})

	It("ends the stream on cancellation although a descendant holds stdout open", func() {
		_, cancel, closed := startHang("orphan")
		DeferCleanup(func() {
			b, err := os.ReadFile(pidFile + ".gc")
			if err != nil {
				panic(err)
			}
			pid, err := strconv.Atoi(string(b))
			if err != nil {
				panic(err)
			}
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Kill()
			}
		})
		cancel()
		Eventually(closed).WithTimeout(10 * time.Second).WithPolling(10 * time.Millisecond).Should(BeClosed())
	})

	// The call must return on the grace period, not when the helper gives up
	// by itself a minute later, so the wait is bounded well inside that.
	It("returns once the grace period has ended a child that lingers after its result line", func() {
		GinkgoT().Setenv(helperEnv, "result-hang")
		done := make(chan error, 1)
		go func() {
			_, err := p.Chat(context.Background(), userReq("hi"))
			done <- err
		}()
		Eventually(done).WithTimeout(10 * time.Second).WithPolling(20 * time.Millisecond).Should(Receive(BeNil()))
	})

	It("kills a child blocked writing an over-long line without waiting out the grace period", func() {
		self, err := os.Executable()
		if err != nil {
			panic(err)
		}
		p = anthropic.NewSubscription(self, anthropic.ExecLauncher, anthropic.WithResultGrace(time.Hour), anthropic.WithMaxLine(1024))
		GinkgoT().Setenv(helperEnv, "flood")
		done := make(chan error, 1)
		go func() { _, err := p.Chat(context.Background(), userReq("hi")); done <- err }()
		Eventually(done).WithTimeout(10 * time.Second).WithPolling(10 * time.Millisecond).
			Should(Receive(MatchError(ContainSubstring("1024-byte limit"))))
	})

	Describe("a child that floods stderr and exits non-zero", func() {
		var err error

		BeforeEach(func() { _, err = chat("stderr", "hi") })

		It("keeps the end of stderr in the error", func() {
			Expect(err).To(MatchError(ContainSubstring(tailMark)))
		})

		It("bounds the stderr it reports", func() {
			Expect(len(err.Error())).To(BeNumerically("<", 2300))
		})
	})

	Describe("a missing binary", func() {
		var err error

		BeforeEach(func() {
			_, err = anthropic.NewSubscription(filepath.Join(GinkgoT().TempDir(), "no-such-claude"), anthropic.ExecLauncher).
				Chat(context.Background(), userReq("hi"))
		})

		It("is marked with ErrBinaryNotFound", func() {
			Expect(err).To(MatchError(anthropic.ErrBinaryNotFound))
		})

		It("is named in the message", func() {
			Expect(err).To(MatchError(ContainSubstring("no-such-claude")))
		})
	})
})
