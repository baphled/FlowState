package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
)

// captureLoopRequests swaps the loop seam for one that records each request on
// the returned channel and completes the run.
func captureLoopRequests() chan app.SessionExecLoopRequest {
	got := make(chan app.SessionExecLoopRequest, 4)
	old := execLoopRunner
	execLoopRunner = func(_ context.Context, _ *store.Store, _ string, _ *store.Session, _ provider.Streamer,
		req app.SessionExecLoopRequest) (*app.ExecLoopResult, error) {
		got <- req
		return &app.ExecLoopResult{}, nil
	}
	DeferCleanup(func() { execLoopRunner = old })
	return got
}

// captureSwarmConfigs is captureLoopRequests for the executed-swarm seam.
func captureSwarmConfigs() chan app.ExecuteSwarmConfig {
	got := make(chan app.ExecuteSwarmConfig, 4)
	old := swarmExecutor
	swarmExecutor = func(_ context.Context, _ *app.Store, _ string, cfg app.ExecuteSwarmConfig) (app.ExecuteSwarmResult, error) {
		got <- cfg
		return app.ExecuteSwarmResult{Status: app.SwarmExecAccepted}, nil
	}
	DeferCleanup(func() { swarmExecutor = old })
	return got
}

// runLoopWith runs `session run --allow echo <extra> <id>` against a fresh
// session and returns the loop request the run built.
func runLoopWith(extra ...string) (app.SessionExecLoopRequest, int) {
	withFakeZai(fakeZaiServer())
	got := captureLoopRequests()
	run := runCLILoop(newLoopSession(), append([]string{"--allow", "echo"}, extra...)...)
	if run.exit != 0 {
		return app.SessionExecLoopRequest{}, run.exit
	}
	return <-got, run.exit
}

// runSwarmWith runs the staged example with --execute and returns the config the
// swarm run built.
func runSwarmWith(extra ...string) (app.ExecuteSwarmConfig, int) {
	withFakeZai(fakeZaiServer())
	got := captureSwarmConfigs()
	run := runSwarmExecute(stageExampleSwarm(unedited), extra...)
	if run.exit != 0 {
		return app.ExecuteSwarmConfig{}, run.exit
	}
	return <-got, run.exit
}

// runServeWith boots serve with an echo allowlist and the extra flags, posts one
// run and returns the loop request it built.
func runServeWith(extra ...string) app.SessionExecLoopRequest {
	cfg := filepath.Join(GinkgoT().TempDir(), "flowstate.json")
	if err := os.WriteFile(cfg, []byte(`{"tool_allowlist":["echo"]}`), 0o600); err != nil {
		panic(err)
	}
	var id string
	base, stop := serveBootAt(filepath.Join(GinkgoT().TempDir(), "serve.db"), func(st *app.Store) {
		sess, err := app.CreateSession(context.Background(), st, "z.ai", "glm-5.3", app.DefaultHarness, "3f7a", "greet")
		if err != nil {
			panic(err)
		}
		id = sess.ID
	}, append([]string{"--config", cfg}, extra...)...)
	defer func() { mustExit(stop()) }()
	withFakeZai(fakeZaiServer())
	got := captureLoopRequests()
	res := serveReq(http.MethodPost, base+"/sessions/"+id+"/run", "")
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		panic("serve run answered " + res.Status)
	}
	return <-got
}

var _ = Describe("--hooks on session run", func() {
	for _, c := range []struct {
		name  string
		extra []string
		want  app.HookScope
	}{
		{"defaults to user", nil, app.HookScopeUser},
		{"carries user", []string{"--hooks", "user"}, app.HookScopeUser},
		{"carries project", []string{"--hooks", "project"}, app.HookScopeProject},
		{"carries both", []string{"--hooks", "both"}, app.HookScopeBoth},
		{"carries none", []string{"--hooks", "none"}, app.HookScopeNone},
		{"carries the = form", []string{"--hooks=both"}, app.HookScopeBoth},
	} {
		It(c.name, func() {
			req, _ := runLoopWith(c.extra...)
			Expect(req.HooksScope).To(Equal(c.want))
		})
	}

	It("leaves ProjectRoot empty when --workdir is given, so hooks never follow the workdir", func() {
		req, _ := runLoopWith("--hooks", "project", "--workdir", GinkgoT().TempDir())
		Expect(app.HooksProjectRoot(req)).To(BeEmpty())
	})

	It("still passes --workdir to the request", func() {
		work := GinkgoT().TempDir()
		req, _ := runLoopWith("--hooks", "project", "--workdir", work)
		Expect(req.Workdir).To(Equal(work))
	})

	for _, bad := range [][]string{{"--hooks", "all"}, {"--hooks=Both"}, {"--hooks="}, {"--hooks"}} {
		It("exits 2 for "+bad[0], func() {
			_, exit := runLoopWith(bad...)
			Expect(exit).To(Equal(2))
		})
	}

	It("names the accepted values for an unknown scope", func() {
		withFakeZai(fakeZaiServer())
		run := runCLILoop(newLoopSession(), "--allow", "echo", "--hooks", "all")
		Expect(run.stderr).To(ContainSubstring("user, project, both or none"))
	})
})

var _ = Describe("--hooks on swarm run --execute", func() {
	for _, c := range []struct {
		name  string
		extra []string
		want  app.HookScope
	}{
		{"defaults to user", nil, app.HookScopeUser},
		{"carries project", []string{"--hooks", "project"}, app.HookScopeProject},
		{"carries both", []string{"--hooks", "both"}, app.HookScopeBoth},
		{"carries none", []string{"--hooks=none"}, app.HookScopeNone},
	} {
		It(c.name, func() {
			cfg, _ := runSwarmWith(c.extra...)
			Expect(cfg.HooksScope).To(Equal(c.want))
		})
	}

	It("leaves ProjectRoot empty when --workdir differs, so hooks never follow the workdir", func() {
		cfg, _ := runSwarmWith("--hooks", "project")
		Expect(cfg.ProjectRoot).To(BeEmpty())
	})

	It("exits 2 for an unknown scope", func() {
		_, exit := runSwarmWith("--hooks", "all")
		Expect(exit).To(Equal(2))
	})

	It("is refused without --execute", func() {
		manifest := filepath.Join(stageExampleSwarm(unedited), "review-swarm.yaml")
		run := runArgv([]string{"swarm", "run", manifest, "--parent", "x", "--budget", "5", "--hooks", "user", "--db", dbFor()})
		Expect(run.stderr).To(ContainSubstring("--hooks only applies to `swarm run --execute`"))
	})
})

var _ = Describe("--hooks on serve", func() {
	for _, c := range []struct {
		name  string
		extra []string
		want  app.HookScope
	}{
		{"defaults to user", nil, app.HookScopeUser},
		{"carries both", []string{"--hooks", "both"}, app.HookScopeBoth},
		{"carries none", []string{"--hooks=none"}, app.HookScopeNone},
		{"carries project", []string{"--hooks", "project"}, app.HookScopeProject},
	} {
		It(c.name, func() {
			Expect(runServeWith(c.extra...).HooksScope).To(Equal(c.want))
		})
	}

	It("resolves the project root as the server's, not a per-session directory", func() {
		Expect(app.HooksProjectRoot(runServeWith("--hooks", "project"))).To(BeEmpty())
	})

	It("exits 2 for an unknown scope before listening", func() {
		run := runArgv([]string{"serve", "--hooks", "all", "--addr", "127.0.0.1:0", "--db", dbFor()})
		Expect(run.exit).To(Equal(2))
	})
})

// hookRefusedArgv is every command the dispatcher knows that cannot run hooks,
// each with the arguments it needs to get past argument checks. A command that
// dispatch.go routes must be listed here: the spec below fails for an unlisted one.
var hookRefusedArgv = [][]string{
	{"session", "new", "--provider", "z.ai", "--model", "m", "--task", "t"},
	{"session", "list"}, {"session", "events", "x"}, {"session", "doctor", "x"}, {"session", "replay", "x"},
	{"session", "close", "x"}, {"session", "cancel", "x"}, {"session", "compact", "x"},
	{"session", "unlock", "x"}, {"session", "tool", "x"}, {"session", "message", "x"},
	{"session", "budget", "x"}, {"replay", "x"}, {"receipt", "validate", "x"},
	{"swarm", "validate", filepath.Join(exampleSwarmDir, "review-swarm.yaml")},
	{"agents", "list"}, {"agents", "validate"}, {"store", "info"}, {"store"}, {"swarm"}, {"agents"},
	{"schema"}, {"config"}, {"doctor"}, {"bogus"},
}

var _ = Describe("--hooks on commands that cannot run hooks", func() {
	for _, argv := range hookRefusedArgv {
		for _, form := range [][]string{{"--hooks", "user"}, {"--hooks=user"}} {
			It("exits 2 for "+strings.Join(argv, " ")+" "+form[0], func() {
				full := append(append(append([]string{}, argv...), form...), "--db", dbFor())
				Expect(runArgv(full).exit).To(Equal(2))
			})
		}
	}

	It("lists every session subcommand dispatchSession routes", func() {
		src, err := os.ReadFile("dispatch.go")
		Expect(err).NotTo(HaveOccurred())
		for _, m := range regexp.MustCompile(`case "(\w+)":\n\t\treturn cmd`).FindAllStringSubmatch(string(src), -1) {
			Expect(hookRefusedArgvHas("session", m[1]) || m[1] == "run").To(BeTrue(), m[1])
		}
	})

	for _, argv := range [][]string{{"session", "run", "x"}, {"swarm", "run", "x"}, {"serve"}} {
		It("does not refuse "+strings.Join(argv, " "), func() {
			Expect(acceptsHooks(argv)).To(BeTrue())
		})
	}

	It("names the commands that accept it", func() {
		run := runArgv([]string{"session", "list", "--hooks", "user", "--db", dbFor()})
		Expect(run.stderr).To(ContainSubstring("`session run`, `serve` and `swarm run --execute`"))
	})
})

var _ = Describe("app.HooksProjectRoot", func() {
	It("is the request's ProjectRoot, not the per-run Workdir", func() {
		req := app.SessionExecLoopRequest{ProjectRoot: "/repo", Workdir: "/elsewhere"}
		Expect(app.HooksProjectRoot(req)).To(Equal("/repo"))
	})

	It("is empty, the process directory, when ProjectRoot is unset and Workdir is given", func() {
		Expect(app.HooksProjectRoot(app.SessionExecLoopRequest{Workdir: "/elsewhere"})).To(BeEmpty())
	})
})

func hookRefusedArgvHas(cmd, sub string) bool {
	for _, a := range hookRefusedArgv {
		if a[0] == cmd && len(a) > 1 && a[1] == sub {
			return true
		}
	}
	return false
}

var _ = Describe("--hooks scope zero value", func() {
	It("is not the user scope, so an unset request scope never loads the user file", func() {
		Expect(app.HookScope("")).NotTo(Equal(app.HookScopeUser))
	})
})
