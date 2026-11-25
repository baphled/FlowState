package tool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	case "environ":
		for _, kv := range os.Environ() {
			fmt.Println("env:" + kv)
		}
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
	return helperNamedExecTool("helper", workdir, opts...)
}

// helperNamedExecTool registers the test binary under the allowlist name, so a
// spec can observe the environment a go, make, git or gh child would get.
func helperNamedExecTool(name, workdir string, opts ...ExecOption) Tool {
	bins := map[string]ExecBinary{
		name: {Path: filepath.Clean(os.Args[0]), Subcommands: []string{"hi", "x", "cwd", "env", "environ", "block", "big", "fail"}},
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
	It("names the allowed binaries in the denial", func() {
		_, err := invoke(l, ExecRunName, `{"cmd":"sh","args":["-c","id"]}`)
		Expect(err.Error()).To(ContainSubstring("sh (allowed: helper [hi x cwd env environ block big fail])"))
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
	It("renders the default allowlist summary in policy order", func() {
		want := "gh [pr run]; git [worktree switch checkout add status diff log commit push fetch pull]; go [test build vet fmt mod tool]; make"
		Expect(AllowlistSummary(DefaultExecAllowlist())).To(Equal(want))
	})
	It("renders the allowlist summary deterministically", func() {
		first := AllowlistSummary(DefaultExecAllowlist())
		Expect(AllowlistSummary(DefaultExecAllowlist())).To(Equal(first))
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

// childEnviron runs the helper's environ verb under opts and returns the
// child's environment as NAME=value lines.
func childEnviron(opts ...ExecOption) []string { return childEnvironAs("helper", opts...) }

// childEnvironAs is childEnviron for a child registered under the given
// allowlist name.
func childEnvironAs(name string, opts ...ExecOption) []string {
	l, _ := newExecLoop(helperNamedExecTool(name, GinkgoT().TempDir(), opts...), []string{ExecRunName})
	res, err := invoke(l, ExecRunName, `{"cmd":"`+name+`","args":["environ"]}`)
	if err != nil {
		panic(err)
	}
	var out []string
	for _, line := range strings.Split(res.(map[string]any)["stdout"].(string), "\n") {
		if rest, ok := strings.CutPrefix(line, "env:"); ok {
			out = append(out, rest)
		}
	}
	return out
}

// setParentEnv sets one variable in the engine's environment for the spec.
func setParentEnv(k, v string) { GinkgoT().Setenv(k, v) }

// unsetParentEnv removes one variable from the engine's environment for the spec.
func unsetParentEnv(k string) {
	GinkgoT().Setenv(k, "")
	must(os.Unsetenv(k))
}

var _ = Describe("exec.run child environment allowlist", func() {
	BeforeEach(func() { setExecEnv("1") })

	DescribeTable("forwards the toolchain set to go and make",
		func(bin, name, value string) {
			setParentEnv(name, value)
			Expect(childEnvironAs(bin)).To(ContainElement(name + "=" + value))
		},
		Entry("go sees HOME", "go", "HOME", "/home/someone"),
		Entry("go sees GOCACHE", "go", "GOCACHE", "/cache/go-build"),
		Entry("go sees GOMODCACHE", "go", "GOMODCACHE", "/cache/mod"),
		Entry("go sees GOTOOLCHAIN", "go", "GOTOOLCHAIN", "go1.26.11"),
		Entry("go sees a GOPROXY without userinfo", "go", "GOPROXY", "https://proxy.golang.org,direct"),
		Entry("make sees HOME", "make", "HOME", "/home/someone"),
		Entry("make sees GOCACHE", "make", "GOCACHE", "/cache/go-build"),
	)

	DescribeTable("never forwards the toolchain set to git or gh",
		func(bin, name string) {
			setParentEnv(name, "/some/value")
			Expect(childEnvironAs(bin)).NotTo(ContainElement(name + "=/some/value"))
		},
		Entry("git does not see HOME", "git", "HOME"),
		Entry("git does not see XDG_CONFIG_HOME", "git", "XDG_CONFIG_HOME"),
		Entry("git does not see GOCACHE", "git", "GOCACHE"),
		Entry("git does not see GOPATH", "git", "GOPATH"),
		Entry("gh does not see HOME", "gh", "HOME"),
		Entry("gh does not see XDG_CONFIG_HOME", "gh", "XDG_CONFIG_HOME"),
		Entry("gh does not see GOCACHE", "gh", "GOCACHE"),
		Entry("gh does not see GOPATH", "gh", "GOPATH"),
	)

	DescribeTable("forwards the common set to every binary",
		func(bin, name, value string) {
			setParentEnv(name, value)
			Expect(childEnvironAs(bin)).To(ContainElement(name + "=" + value))
		},
		Entry("git sees LANG", "git", "LANG", "en_GB.UTF-8"),
		Entry("gh sees TZ", "gh", "TZ", "UTC"),
		Entry("go sees TMPDIR", "go", "TMPDIR", os.TempDir()),
		Entry("make sees LC_ALL", "make", "LC_ALL", "C"),
		Entry("git sees PATH", "git", "PATH", os.Getenv("PATH")),
	)

	DescribeTable("drops a go-tool variable that could carry a credential or run a program",
		func(name, value string) {
			setParentEnv(name, value)
			Expect(childEnvironAs("go")).NotTo(ContainElement(name + "=" + value))
		},
		Entry("GOPROXY with userinfo", "GOPROXY", "https://user:pass@proxy.example/"),
		Entry("GOPROXY with userinfo later in the list", "GOPROXY", "https://proxy.golang.org|https://u@p.example,direct"),
		Entry("GOPROXY with scheme-less userinfo", "GOPROXY", "user:pass@proxy.example/x"),
		Entry("GOPROXY with a query token", "GOPROXY", "https://proxy.example/p?token=a"),
		Entry("GOPROXY with userinfo before a query", "GOPROXY", "https://u@proxy.example?x"),
		Entry("GOSUMDB with userinfo", "GOSUMDB", "sum.example https://u:p@sum.example"),
	)

	DescribeTable("never forwards GOFLAGS or the XDG config and data directories to go or make",
		func(bin, name, value string) {
			setParentEnv(name, value)
			Expect(childEnvironAs(bin)).NotTo(ContainElement(name + "=" + value))
		},
		Entry("go does not see GOFLAGS -mod=vendor", "go", "GOFLAGS", "-mod=vendor"),
		Entry("make does not see GOFLAGS -mod=vendor", "make", "GOFLAGS", "-mod=vendor"),
		Entry("go does not see a quoted -toolexec", "go", "GOFLAGS", `"-toolexec=/bin/false"`),
		Entry("make does not see a quoted -toolexec", "make", "GOFLAGS", `"-toolexec=/bin/false"`),
		Entry("go does not see XDG_CONFIG_HOME", "go", "XDG_CONFIG_HOME", "/home/u/.config"),
		Entry("make does not see XDG_CONFIG_HOME", "make", "XDG_CONFIG_HOME", "/home/u/.config"),
		Entry("go does not see XDG_DATA_HOME", "go", "XDG_DATA_HOME", "/home/u/.local/share"),
		Entry("make does not see XDG_DATA_HOME", "make", "XDG_DATA_HOME", "/home/u/.local/share"),
	)

	DescribeTable("forwards a go-tool variable that carries no credential and runs nothing",
		func(name, value string) {
			setParentEnv(name, value)
			Expect(childEnvironAs("go")).To(ContainElement(name + "=" + value))
		},
		Entry("GOPROXY host containing exec", "GOPROXY", "my-exec.example"),
		Entry("GOPROXY direct", "GOPROXY", "direct"),
		Entry("GOPROXY off", "GOPROXY", "off"),
		Entry("GOPROXY default list", "GOPROXY", "https://proxy.golang.org,direct"),
		Entry("GOPROXY file URL", "GOPROXY", "file:///var/cache/proxy"),
		Entry("GOPROXY with @ only in the path", "GOPROXY", "https://h/a@b"),
		Entry("go sees XDG_CACHE_HOME", "XDG_CACHE_HOME", "/home/u/.cache"),
	)

	DescribeTable("forwards the harmless toolchain names to go",
		func(name string) {
			setParentEnv(name, "v")
			Expect(childEnvironAs("go")).To(ContainElement(name + "=v"))
		},
		Entry("GOPRIVATE", "GOPRIVATE"), Entry("GONOSUMDB", "GONOSUMDB"), Entry("GONOPROXY", "GONOPROXY"),
		Entry("GONOSUMCHECK", "GONOSUMCHECK"), Entry("GOINSECURE", "GOINSECURE"), Entry("GOVCS", "GOVCS"),
		Entry("GOOS", "GOOS"), Entry("GOARCH", "GOARCH"), Entry("CGO_ENABLED", "CGO_ENABLED"),
		Entry("CC", "CC"), Entry("CXX", "CXX"),
	)

	DescribeTable("never forwards proxy or GOAUTH variables to go",
		func(name string) {
			setParentEnv(name, "http://u:p@proxy.example")
			Expect(childEnvironAs("go")).NotTo(ContainElement(HavePrefix(name + "=")))
		},
		Entry("HTTPS_PROXY", "HTTPS_PROXY"), Entry("HTTP_PROXY", "HTTP_PROXY"),
		Entry("NO_PROXY", "NO_PROXY"), Entry("GOAUTH", "GOAUTH"),
	)

	DescribeTable("sets fixed neutralisers for go and make that the engine cannot override",
		func(bin, kv string) {
			setParentEnv(strings.SplitN(kv, "=", 2)[0], "/home/someone/.netrc")
			Expect(childEnvironAs(bin)).To(ContainElement(kv))
		},
		Entry("go GIT_CONFIG_GLOBAL", "go", "GIT_CONFIG_GLOBAL="+os.DevNull),
		Entry("go GIT_CONFIG_NOSYSTEM", "go", "GIT_CONFIG_NOSYSTEM=1"),
		Entry("go GIT_TERMINAL_PROMPT", "go", "GIT_TERMINAL_PROMPT=0"),
		Entry("go NETRC", "go", "NETRC="+os.DevNull),
		Entry("make NETRC", "make", "NETRC="+os.DevNull),
	)

	DescribeTable("does not give git or gh the neutralisers",
		func(bin, name string) {
			Expect(childEnvironAs(bin)).NotTo(ContainElement(HavePrefix(name + "=")))
		},
		Entry("git GIT_CONFIG_GLOBAL", "git", "GIT_CONFIG_GLOBAL"),
		Entry("git NETRC", "git", "NETRC"),
		Entry("gh GIT_TERMINAL_PROMPT", "gh", "GIT_TERMINAL_PROMPT"),
		Entry("gh GIT_CONFIG_NOSYSTEM", "gh", "GIT_CONFIG_NOSYSTEM"),
	)

	DescribeTable("never forwards a secret-looking variable to go",
		func(name string) {
			setParentEnv(name, "hunter2")
			Expect(childEnvironAs("go")).NotTo(ContainElement(name + "=hunter2"))
		},
		Entry("ZAI_API_KEY", "ZAI_API_KEY"),
		Entry("ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY"),
		Entry("OPENAI_API_KEY", "OPENAI_API_KEY"),
		Entry("GITHUB_TOKEN", "GITHUB_TOKEN"),
		Entry("AWS_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY"),
		Entry("MY_PASSWORD", "MY_PASSWORD"),
		Entry("X_CREDENTIAL_Y matches only credential", "X_CREDENTIAL_Y"),
		Entry("MY_SECRET matches only secret", "MY_SECRET"),
		Entry("SSH_AUTH_SOCK matches only auth", "SSH_AUTH_SOCK"),
		Entry("OP_SERVICE_ACCOUNT_TOKEN matches only token", "OP_SERVICE_ACCOUNT_TOKEN"),
		Entry("MY_KEY_X matches only key", "MY_KEY_X"),
	)

	DescribeTable("the secret filter matches credential-looking names",
		func(name string) {
			Expect(execSecretEnv.MatchString(name)).To(BeTrue())
		},
		Entry("SSH_AUTH_SOCK", "SSH_AUTH_SOCK"),
		Entry("MY_KEY_X", "MY_KEY_X"),
		Entry("X_TOKEN", "X_TOKEN"),
		Entry("A_PASSWORD", "A_PASSWORD"),
		Entry("B_SECRET", "B_SECRET"),
		Entry("C_CREDENTIAL", "C_CREDENTIAL"),
	)

	It("lists no name that the secret filter matches", func() {
		var masked []string
		for _, name := range append(commonExecEnv(), toolchainExecEnv()...) {
			if execSecretEnv.MatchString(name) {
				masked = append(masked, name)
			}
		}
		Expect(masked).To(BeEmpty())
	})

	DescribeTable("never forwards a FLOWSTATE or provider variable to any binary",
		func(bin, name string) {
			setParentEnv(name, "1")
			Expect(childEnvironAs(bin)).NotTo(ContainElement(HavePrefix(name + "=")))
		},
		Entry("go FLOWSTATE_EXEC_ENABLED", "go", "FLOWSTATE_EXEC_ENABLED"),
		Entry("make FLOWSTATE_EXEC_ENABLED", "make", "FLOWSTATE_EXEC_ENABLED"),
		Entry("git FLOWSTATE_EXEC_ENABLED", "git", "FLOWSTATE_EXEC_ENABLED"),
		Entry("gh FLOWSTATE_EXEC_ENABLED", "gh", "FLOWSTATE_EXEC_ENABLED"),
		Entry("go FLOWSTATE_HOME", "go", "FLOWSTATE_HOME"),
		Entry("git FLOWSTATE_HOME", "git", "FLOWSTATE_HOME"),
		Entry("go ZAI_API_KEY", "go", "ZAI_API_KEY"),
		Entry("make ZAI_API_KEY", "make", "ZAI_API_KEY"),
		Entry("git ZAI_API_KEY", "git", "ZAI_API_KEY"),
		Entry("gh ZAI_API_KEY", "gh", "ZAI_API_KEY"),
	)

	It("sets GOPROXY=off when the engine's GOPROXY is dropped", func() {
		setParentEnv("GOPROXY", "https://user:pass@proxy.example/")
		Expect(childEnvironAs("go")).To(ContainElement("GOPROXY=off"))
	})
	It("leaves GOSUMDB unset when the engine's GOSUMDB is dropped", func() {
		setParentEnv("GOSUMDB", "sum.example https://u:p@sum.example/")
		Expect(childEnvironAs("go")).NotTo(ContainElement(HavePrefix("GOSUMDB=")))
	})
	It("does not give git a GOPROXY when the engine's GOPROXY is dropped", func() {
		setParentEnv("GOPROXY", "https://user:pass@proxy.example/")
		Expect(childEnvironAs("git")).NotTo(ContainElement(HavePrefix("GOPROXY=")))
	})

	It("keeps the PATH the engine has", func() {
		setParentEnv("PATH", "/opt/x/bin")
		Expect(childEnv("git")).To(ContainElement("PATH=/opt/x/bin"))
	})
	It("falls back to a default PATH when the engine has none", func() {
		unsetParentEnv("PATH")
		Expect(childEnv("git")).To(ContainElement(HavePrefix("PATH=/usr/local/sbin:")))
	})

	It("does not forward an unlisted variable", func() {
		setParentEnv("FOO", "bar")
		Expect(childEnviron()).NotTo(ContainElement("FOO=bar"))
	})
	It("does not forward an unset variable as empty", func() {
		unsetParentEnv("GOCACHE")
		Expect(childEnvironAs("go")).NotTo(ContainElement(HavePrefix("GOCACHE=")))
	})
})

// On Windows the engine's variable names are case-insensitive: a mixed-case
// name in the engine environment is forwarded under its canonical name.
func windowsNameSpecs() {
	DescribeTable("forwards the Windows names found case-insensitively",
		func(set, want string) {
			setParentEnv(set, `C:\x`)
			Expect(childEnv("git")).To(ContainElement(want + `=C:\x`))
		},
		Entry("SystemRoot", "SystemRoot", "SYSTEMROOT"),
		Entry("PathExt", "PathExt", "PATHEXT"),
		Entry("ComSpec", "ComSpec", "COMSPEC"),
		Entry("WinDir", "WinDir", "WINDIR"),
	)
}

var _ = func() bool {
	if runtime.GOOS == "windows" {
		Describe("exec.run Windows environment names", windowsNameSpecs)
	}
	return true
}()
