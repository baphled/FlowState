package main

import (
	"context"
	"errors"
	"fmt"
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

// skillsRefusedArgv is every command that cannot take the skills settings: those of hookRefusedArgv, plus serve
// and swarm run, which take no skills settings until a later packet adds their flags. A command dispatch.go routes
// must be listed here or in skillsAcceptedArgv: the specs below fail for an unlisted one.
var skillsRefusedArgv = append(append([][]string{}, hookRefusedArgv...), []string{"serve"}, []string{"swarm", "run", "x"})

// skillsAcceptedArgv is every command that takes the skills settings today.
var skillsAcceptedArgv = [][]string{{"skills", "list"}, {"skills", "validate"}, {"session", "run", "x"}}

var _ = Describe("The skills settings on commands that cannot take them", func() {
	for _, argv := range skillsRefusedArgv {
		for _, form := range [][]string{{"--skills", "user"}, {"--skills=both"}, {"--skills-path", "/skills"}, {"--skills-default", "tdd"}, {"--skills-path=/skills"}, {"--skills-default=tdd"}} {
			It("exits 2 for "+strings.Join(argv, " ")+" "+form[0], func() {
				full := append(append(append([]string{}, argv...), form...), "--db", dbFor())
				Expect(runArgv(full).exit).To(Equal(2))
			})
		}
	}
	for _, argv := range skillsAcceptedArgv {
		It("does not refuse "+strings.Join(argv, " "), func() {
			Expect(acceptsSkillsSettings(argv)).To(BeTrue())
		})
	}
	It("names the commands that accept them", func() {
		run := runArgv([]string{"session", "list", "--skills", "user", "--db", dbFor()})
		Expect(run.stderr).To(ContainSubstring("`skills list`, `skills validate` and `session run`"))
	})
	It("lists every top-level command dispatch.go routes", func() {
		src, err := os.ReadFile("dispatch.go")
		if err != nil {
			panic(err)
		}
		for _, m := range regexp.MustCompile(`case "(\w+)":\n\t\treturn route`).FindAllStringSubmatch(string(src), -1) {
			Expect(skillsDecisionLists(m[1]) || m[1] == "session").To(BeTrue(), m[1])
		}
	})
})

var _ = Describe("The skills settings on session run", func() {
	It("defaults the scope to user", func() {
		req, _ := runLoopWith()
		Expect(req.SkillsScope).To(Equal(app.SkillsScopeUser))
	})
	It("carries --skills both", func() {
		req, _ := runLoopWith("--skills", "both")
		Expect(req.SkillsScope).To(Equal(app.SkillsScopeBoth))
	})
	It("carries --skills=none", func() {
		req, _ := runLoopWith("--skills=none")
		Expect(req.SkillsScope).To(Equal(app.SkillsScopeNone))
	})
	It("carries --skills-path, repeatable, in order", func() {
		a, b := absDir("a"), absDir("b")
		req, _ := runLoopWith("--skills-path", a, "--skills-path", b)
		Expect(req.SkillsPaths).To(Equal([]string{a, b}))
	})
	It("carries --skills-default, splitting a comma-separated value", func() {
		req, _ := runLoopWith("--skills-default", "tdd,code-*", "--skills-default", "review")
		Expect(req.SkillsDefault).To(Equal([]string{"tdd", "code-*", "review"}))
	})
	It("leaves ProjectRoot empty under --workdir, so project skills never follow the workdir", func() {
		req, _ := runLoopWith("--skills", "project", "--workdir", GinkgoT().TempDir())
		Expect(req.ProjectRoot).To(BeEmpty())
	})
	It("exits 2 for a relative --skills-path", func() {
		_, exit := runLoopWith("--skills-path", "skills")
		Expect(exit).To(Equal(2))
	})
	It("exits 2 for an unknown scope", func() {
		_, exit := runLoopWith("--skills", "all")
		Expect(exit).To(Equal(2))
	})
	It("exits 4 for a relative skills_paths entry in --config", func() {
		_, exit := runLoopWith("--config", writeConfigFixture(`{"tool_allowlist":["echo"],"skills_paths":["skills"]}`))
		Expect(exit).To(Equal(4))
	})
	It("exits 4 for an ambient flowstate.json naming skills_paths", func() {
		id := newLoopSession()
		dir := inTempDir()
		if err := os.WriteFile(filepath.Join(dir, "flowstate.json"), []byte(`{"skills_paths":["/skills"]}`), 0o600); err != nil {
			panic(err)
		}
		Expect(runCLILoop(id, "--allow", "echo").exit).To(Equal(4))
	})
})

var _ = Describe("The skills settings of session run from --config", func() {
	var paths string

	BeforeEach(func() {
		paths = absDir("a", "skills")
	})

	It("are read from the config keys when the flags are absent", func() {
		cfg := writeConfigFixture(fmt.Sprintf(`{"tool_allowlist":["echo"],"skills_paths":[%q],"skills_default":["tdd"]}`, paths))
		req, _ := runLoopWith("--config", cfg)
		Expect(req.SkillsPaths).To(Equal([]string{paths}))
	})
	It("read skills_default from the config", func() {
		cfg := writeConfigFixture(`{"tool_allowlist":["echo"],"skills_default":["tdd"]}`)
		req, _ := runLoopWith("--config", cfg)
		Expect(req.SkillsDefault).To(Equal([]string{"tdd"}))
	})
	It("let --skills-path replace the config paths", func() {
		cfg := writeConfigFixture(fmt.Sprintf(`{"tool_allowlist":["echo"],"skills_paths":[%q],"skills_default":["tdd"]}`, paths))
		other := absDir("b")
		req, _ := runLoopWith("--config", cfg, "--skills-path", other)
		Expect(req.SkillsPaths).To(Equal([]string{other}))
	})
	It("keep the config default beside --skills-path", func() {
		cfg := writeConfigFixture(fmt.Sprintf(`{"tool_allowlist":["echo"],"skills_paths":[%q],"skills_default":["tdd"]}`, paths))
		req, _ := runLoopWith("--config", cfg, "--skills-path", absDir("b"))
		Expect(req.SkillsDefault).To(Equal([]string{"tdd"}))
	})
})

var _ = Describe("A session created over HTTP from --config carrying the skills settings", func() {
	It("records a composition hash that includes them", func() {
		paths := absDir("a", "skills")
		db := absDir("serve.db")
		cfg := absDir("flowstate.json")
		if err := os.WriteFile(cfg, []byte(fmt.Sprintf(`{"default_provider":"z.ai","default_model":"m","skills_paths":[%q]}`, paths)), 0o600); err != nil {
			panic(err)
		}
		base, stop := serveBootAt(db, nil, "--config", cfg)
		defer func() { mustExit(stop()) }()
		res := serveReq(http.MethodPost, base+"/sessions", `{"task":"greet"}`)
		if res.StatusCode != http.StatusOK {
			panic("create answered " + res.Status)
		}
		var rec map[string]any
		serveJSON(res, &rec)
		var row map[string]any
		serveJSON(serveReq(http.MethodGet, base+"/sessions/"+rec["id"].(string), ""), &row)
		want, err := compositionSHA256("z.ai", "m", db, cfg, nil, skillsSettings{Paths: []string{paths}})
		if err != nil {
			panic(err)
		}
		Expect(row["config_sha256"]).To(Equal(want))
	})
})

// skillsDecisionLists reports whether cmd is in the refused or the accepted argv list.
func skillsDecisionLists(cmd string) bool {
	for _, list := range [][][]string{skillsRefusedArgv, skillsAcceptedArgv} {
		for _, a := range list {
			if a[0] == cmd {
				return true
			}
		}
	}
	return false
}

var _ = Describe("app.SkillsRoots", func() {
	It("refuses the zero scope, so an unset request never loads the user directory", func() {
		_, err := app.SkillsRoots(app.SkillsRequest{})
		Expect(errors.Is(err, app.ErrConfigInvalid)).To(BeTrue())
	})
	It("reads no directory under the none scope, paths aside", func() {
		roots, err := app.SkillsRoots(app.SkillsRequest{Scope: app.SkillsScopeNone, Paths: []string{"/a"}})
		if err != nil {
			panic(err)
		}
		Expect(roots).To(HaveLen(1))
	})
	It("finds the project directory under the project root, never under a workdir", func() {
		repo := absDir("repo")
		roots, err := app.SkillsRoots(app.SkillsRequest{Scope: app.SkillsScopeProject, ProjectRoot: repo})
		if err != nil {
			panic(err)
		}
		Expect(roots[0].Dir).To(Equal(filepath.Join(repo, ".flowstate", "skills")))
	})
	It("resolves an empty project root to the process directory", func() {
		Expect(app.ProjectSkillsPath("")).To(Equal(filepath.Join(".", ".flowstate", "skills")))
	})
	It("is not the user scope, so the flag layer must apply the default", func() {
		Expect(app.SkillsScope("")).NotTo(Equal(app.SkillsScopeUser))
	})
})
