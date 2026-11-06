package tool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
)

// init dispatches helper verbs when this test binary is re-invoked
// as an exec.run child (cwd/env/block/size observables).
func init() {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		execHelperMain(os.Args[1])
	}
}

// TestMain propagates the exit status of m.Run.
func TestMain(m *testing.M) { os.Exit(m.Run()) }

// execHelperMain helper verbs: hi/x (stdout), cwd, env (leak+PATH),
// block (kill target), big (oversized), fail (exit 3).
func execHelperMain(verb string) {
	switch verb {
	case "hi", "x":
		fmt.Println(verb)
	case "cwd":
		wd, _ := os.Getwd()
		fmt.Println(wd)
	case "env":
		fmt.Println("leak=" + os.Getenv("FLOWSTATE_TEST_LEAK"))
		fmt.Println("path=" + os.Getenv("PATH"))
	case "block":
		<-time.After(10 * time.Second) // active runtime timer; child must be killed by timeout
	case "big":
		fmt.Println(strings.Repeat("x", 4096))
	case "fail":
		os.Exit(3)
	}
	os.Exit(0)
}

// newExecLoop registers t over a fresh temp session (unique db dir
// + session id per call: the store enforces one live writer per id).
func newExecLoop(t Tool, allow []string) (*Loop, *replayStore) {
	dir, err := os.MkdirTemp("", "execloop")
	must(err)
	DeferCleanup(func() { _ = os.RemoveAll(dir) })
	st, err := store.Open(filepath.Join(dir, "test.db"))
	must(err)
	DeferCleanup(func() { _ = st.Close() })
	sid := "exec-" + fmt.Sprintf("%d", time.Now().UnixNano())
	_, err = st.CreateSession(context.Background(), sid, "prov", "model", "harness", "sha")
	must(err)
	w, err := st.NewEventWriter(context.Background(), sid)
	must(err)
	DeferCleanup(func() { _ = w.Close() })
	r := NewRegistry()
	must(r.Register(t))
	l, err := NewLoop(r, w, allow)
	must(err)
	return l, &replayStore{st: st, sid: sid}
}

// helperExecTool registers the test binary ("helper" verbs) only.
func helperExecTool(workdir string, opts ...ExecOption) Tool {
	bins := map[string]ExecBinary{
		"helper": {Path: filepath.Clean(os.Args[0]), Subcommands: []string{"hi", "x", "cwd", "env", "block", "big", "fail"}},
	}
	return NewExecRun(workdir, append([]ExecOption{WithExecBinaries(bins)}, opts...)...)
}

// setExecEnv arms the env gate for one spec.
func setExecEnv(v string) {
	must(os.Setenv(ExecEnabledEnv, v))
	DeferCleanup(func() { _ = os.Unsetenv(ExecEnabledEnv) })
}

var _ = Describe("exec.run", func() {
	var workdir string
	var l *Loop

	BeforeEach(func() {
		workdir = GinkgoT().TempDir()
		setExecEnv("1")
		l, _ = newExecLoop(helperExecTool(workdir), []string{ExecRunName})
	})

	It("captures stdout on success", func() {
		res, err := invoke(l, ExecRunName, `{"cmd":"helper","args":["hi"]}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.(map[string]any)["stdout"]).To(Equal("hi\n"))
	})
	It("reports exit code zero on success", func() {
		res, err := invoke(l, ExecRunName, `{"cmd":"helper","args":["x"]}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.(map[string]any)["exit_code"]).To(Equal(0))
	})
	It("reports a non-zero exit code without failing the tool", func() {
		res, err := invoke(l, ExecRunName, `{"cmd":"helper","args":["fail"]}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.(map[string]any)["exit_code"]).To(Equal(3))
	})
	It("runs the child in the session workdir", func() {
		res, err := invoke(l, ExecRunName, `{"cmd":"helper","args":["cwd"]}`)
		Expect(err).NotTo(HaveOccurred())
		resolved, derr := filepath.EvalSymlinks(workdir)
		must(derr)
		Expect(res.(map[string]any)["stdout"]).To(Equal(resolved + "\n"))
	})
	DescribeTable("child environment",
		func(want string) {
			must(os.Setenv("FLOWSTATE_TEST_LEAK", "secret"))
			DeferCleanup(func() { _ = os.Unsetenv("FLOWSTATE_TEST_LEAK") })
			res, err := invoke(l, ExecRunName, `{"cmd":"helper","args":["env"]}`)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.(map[string]any)["stdout"]).To(ContainSubstring(want))
		},
		Entry("strips FLOWSTATE_* from the child environment", "leak=\n"),
		Entry("keeps a minimal PATH in the child environment", "path="),
	)
	It("rejects a binary outside the allowlist", func() {
		_, err := invoke(l, ExecRunName, `{"cmd":"sh","args":["-c","id"]}`)
		Expect(errors.Is(err, ErrExecNotAllowed)).To(BeTrue())
	})
	It("rejects a denied subcommand", func() {
		_, err := invoke(l, ExecRunName, `{"cmd":"helper","args":["rm","-rf","/"]}`)
		Expect(errors.Is(err, ErrExecDeniedSubcommand)).To(BeTrue())
	})
	It("forwards multiple literal args after an approved subcommand", func() {
		res, err := invoke(l, ExecRunName, `{"cmd":"helper","args":["hi","extra"]}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.(map[string]any)["stdout"]).To(Equal("hi\n"))
	})
	It("rejects flags before the approved subcommand", func() {
		_, err := invoke(l, ExecRunName, `{"cmd":"helper","args":["-c","alias.status=!sh"]}`)
		Expect(errors.Is(err, ErrExecDeniedSubcommand)).To(BeTrue())
	})
	It("rejects empty arguments and embedded NULs", func() {
		for _, payload := range []string{
			`{"cmd":"helper","args":["hi",""]}`,
			`{"cmd":"helper","args":["hi","bad\u0000arg"]}`,
		} {
			_, err := invoke(l, ExecRunName, payload)
			Expect(errors.Is(err, ErrExecDeniedSubcommand)).To(BeTrue())
		}
	})
	It("rejects malformed argument JSON", func() {
		_, err := invoke(l, ExecRunName, `not-json`)
		Expect(err).To(HaveOccurred())
	})
	It("kills the child at the timeout", func() {
		l2, _ := newExecLoop(helperExecTool(workdir, WithExecTimeout(150*time.Millisecond)), []string{ExecRunName})
		_, err := invoke(l2, ExecRunName, `{"cmd":"helper","args":["block"]}`)
		Expect(errors.Is(err, ErrExecTimeout)).To(BeTrue())
	})
	It("returns exec_not_enabled without the env gate even when allowlisted", func() {
		must(os.Unsetenv(ExecEnabledEnv))
		_, err := invoke(l, ExecRunName, `{"cmd":"helper","args":["x"]}`)
		Expect(errors.Is(err, ErrExecNotEnabled)).To(BeTrue())
	})
	It("maps exec_not_enabled to its result code", func() {
		must(os.Unsetenv(ExecEnabledEnv))
		l2, rs2 := newExecLoop(helperExecTool(workdir), []string{ExecRunName})
		_, _ = invoke(l2, ExecRunName, `{"cmd":"helper","args":["x"]}`)
		Expect(lastResultCode(rs2)).To(Equal(ErrCodeExecNotEnabled))
	})
	DescribeTable("maps typed exec errors to their result codes",
		func(args string, code string, opts ...ExecOption) {
			t2, rs2 := newExecLoop(helperExecTool(workdir, opts...), []string{ExecRunName})
			_, _ = invoke(t2, ExecRunName, args)
			Expect(lastResultCode(rs2)).To(Equal(code))
		},
		Entry("exec_not_allowed", `{"cmd":"sh"}`, ErrCodeExecNotAllowed),
		Entry("exec_denied_subcommand", `{"cmd":"helper","args":["nope"]}`, ErrCodeExecDeniedSubcommand),
		Entry("exec_timeout", `{"cmd":"helper","args":["block"]}`, ErrCodeExecTimeout, WithExecTimeout(150*time.Millisecond)),
		Entry("exec_oversize", `{"cmd":"helper","args":["big"]}`, ErrCodeExecOversize, WithMaxExecOutputBytes(64)),
	)
})

var _ = Describe("exec default allowlist (owner policy)", func() {
	BeforeEach(func() { setExecEnv("1") })

	It("excludes arbitrary shells", func() {
		Expect(DefaultExecAllowlist()).NotTo(HaveKey("sh"))
	})
	It("resolves default binaries to absolute paths at construction", func() {
		t := NewExecRun(GinkgoT().TempDir()).(*execTool)
		Expect(filepath.IsAbs(t.binaries["git"].Path)).To(BeTrue())
	})
	It("denies a LookPath-missing binary with exec_not_allowed", func() {
		t := NewExecRun(GinkgoT().TempDir(), WithExecBinaries(map[string]ExecBinary{"ghost": {Path: "/nonexistent/ghost"}}))
		l, rs := newExecLoop(t, []string{ExecRunName})
		_, _ = invoke(l, ExecRunName, `{"cmd":"ghost"}`)
		Expect(lastResultCode(rs)).To(Equal(ErrCodeExecNotAllowed))
	})
	It("runs git status as a happy path per the owner policy", func() {
		l, rs := newExecLoop(NewExecRun(GinkgoT().TempDir()), []string{ExecRunName})
		_, err := invoke(l, ExecRunName, `{"cmd":"git","args":["status"]}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(lastResultCode(rs)).To(BeEmpty())
	})
})

var _ = DescribeTable("exec env gate helper",
	func(v string, want bool) {
		must(os.Setenv(ExecEnabledEnv, v))
		Expect(ExecEnabled()).To(Equal(want))
		must(os.Unsetenv(ExecEnabledEnv))
	},
	Entry("disabled for unset", "", false),
	Entry("enabled for 1", "1", true),
	Entry("enabled for true", "true", true),
	Entry("disabled for arbitrary values", "yes-please", false),
)
