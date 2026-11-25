package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/swarm"
)

// exampleSwarmDir is the shipped worked example, relative to this package.
const exampleSwarmDir = "../../docs/examples/swarm"

// exampleSwarmFiles are the example's files, relative to its directory.
var exampleSwarmFiles = []string{
	"review-swarm.yaml", ".flowstate/agents/coordinator.md", ".flowstate/agents/reader.md",
	".flowstate/agents/reviewer.md",
}

// stageExampleSwarm copies the example into a fresh work directory, passing the
// manifest text through edit, and returns the directory.
func stageExampleSwarm(edit func(string) string) string {
	work := GinkgoT().TempDir()
	for _, name := range exampleSwarmFiles {
		raw, err := os.ReadFile(filepath.Join(exampleSwarmDir, filepath.FromSlash(name)))
		if err != nil {
			panic(err)
		}
		raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
		if name == "review-swarm.yaml" {
			raw = []byte(edit(string(raw)))
		}
		dst := filepath.Join(work, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			panic(err)
		}
		if err := os.WriteFile(dst, raw, 0o600); err != nil {
			panic(err)
		}
	}
	return work
}

// workdirRefusal is what a refused run leaves: its exit code, the session list
// and whether stderr says the workdir is not a directory.
type workdirRefusal struct {
	Exit     int
	Sessions string
	Named    bool
}

// dbRefusal is what a refused database leaves: the exit code, whether the
// database file was created and whether stderr names --db and --workdir.
type dbRefusal struct {
	Exit           int
	Created, Named bool
}

func unedited(s string) string { return s }

// runSwarmExecute runs `swarm run --execute` on the staged example.
func runSwarmExecute(work string, extra ...string) result {
	argv := []string{"swarm", "run", filepath.Join(work, "review-swarm.yaml"), "--execute", "--db", dbFor(),
		"--agents", "project", "--workdir", work, "--provider", "z.ai", "--model", "glm-5.3"}
	return runArgv(append(argv, extra...))
}

// executedSwarm is the decoded swarm-run-execute object.
type executedSwarm struct {
	Status, Reason string
	Children       []struct {
		Profile, Packet, Status string
	}
	Gates []struct {
		Name   string
		Passed bool
	}
	Packets []struct{ Admitted bool }
}

func decodeExecutedSwarm(out string) executedSwarm {
	var res executedSwarm
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		panic(err)
	}
	return res
}

// swarmScriptServer plays every model of the example: the coordinator delegates
// each packet in turn (the marker and the task text as the manifest has them,
// counting the tool results already in the request), the reviewer writes
// REVIEW.md when write is set, every other turn answers with text.
func swarmScriptServer(work string, write bool) *httptest.Server {
	m, err := swarm.LoadManifest(filepath.Join(work, "review-swarm.yaml"))
	if err != nil {
		panic(err)
	}
	call := func(id, name, args string) string {
		return `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"` + id + `","type":"function","function":` +
			`{"name":"` + name + `","arguments":` + args + `}}]},"finish_reason":"tool_calls"}]}`
	}
	delegate := func(p swarm.Packet, n int) string {
		args, _ := json.Marshal(`{"agent":"` + p.Role + `","task":"[packet ` + p.ID + `] ` + p.Task + `"}`)
		return call("delegate-"+strconv.Itoa(n), "delegate", string(args))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		chunk := `{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`
		results := strings.Count(body, `"role":"tool"`)
		switch {
		case strings.Contains(body, `"name":"delegate"`) && results < len(m.Packets):
			chunk = delegate(m.Packets[results], results)
		case write && strings.Contains(body, `"name":"fs.write"`) && results == 0:
			chunk = call("write-1", "fs.write", `"{\"path\":\"REVIEW.md\",\"content\":\"clear enough\"}"`)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + chunk + "\n\ndata: [DONE]\n\n"))
	}))
	DeferCleanup(srv.Close)
	return srv
}

var _ = Describe("swarm run --execute", func() {
	BeforeEach(func() {
		config := GinkgoT().TempDir()
		for _, env := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA"} {
			GinkgoT().Setenv(env, config)
		}
	})

	Context("on the shipped example with a scripted coordinator", func() {
		var (
			run  result
			work string
		)
		BeforeEach(func() {
			work = stageExampleSwarm(unedited)
			withFakeZai(swarmScriptServer(work, true))
			run = runSwarmExecute(work)
			if run.exit != 0 {
				panic("example run failed: " + run.stderr)
			}
		})

		It("is accepted", func() {
			Expect(decodeExecutedSwarm(run.stdout).Status).To(Equal("accepted"))
		})
		It("attributes the children to the packets by role", func() {
			var got []string
			for _, c := range decodeExecutedSwarm(run.stdout).Children {
				got = append(got, c.Profile+":"+c.Packet)
			}
			Expect(got).To(Equal([]string{"reader:read", "reviewer:review"}))
		})
		It("reports the engine's gate as passed", func() {
			Expect(decodeExecutedSwarm(run.stdout).Gates[0].Passed).To(BeTrue())
		})
		It("admits every packet", func() {
			Expect(decodeExecutedSwarm(run.stdout).Packets).To(HaveEach(HaveField("Admitted", true)))
		})
		It("lets the reviewer write the reviewed file in the work directory", func() {
			Expect(filepath.Join(work, "REVIEW.md")).To(BeARegularFile())
		})
		It("conforms to the swarm-run-execute schema", func() {
			Expect(validateAgainst("swarm-run-execute", run.stdout)).To(Succeed())
		})
	})

	Context("when the engine's gate fails", func() {
		var run result
		BeforeEach(func() {
			work := stageExampleSwarm(func(s string) string { return strings.Replace(s, "test -s REVIEW.md", "test -s NOPE.md", 1) })
			withFakeZai(swarmScriptServer(work, true))
			run = runSwarmExecute(work)
		})

		It("exits 8, as a not accepted receipts-only run does", func() {
			Expect(run.exit).To(Equal(8))
		})
		It("reports the gate as not passed", func() {
			Expect(decodeExecutedSwarm(run.stdout).Gates[0].Passed).To(BeFalse())
		})
		It("reports the status gate_failed", func() {
			Expect(decodeExecutedSwarm(run.stdout).Status).To(Equal("gate_failed"))
		})
		It("names the failed gate on stderr", func() {
			Expect(run.stderr).To(ContainSubstring("failed gates [review-written]"))
		})
	})

	Context("when a packet's source digest does not match", func() {
		It("exits 8 as the run is not accepted", func() {
			work := stageExampleSwarm(func(s string) string {
				return strings.Replace(s, "Read README.md and write a three", "Read README.md and write a four", 1)
			})
			withFakeZai(swarmScriptServer(work, true))
			Expect(runSwarmExecute(work).exit).To(Equal(8))
		})
	})

	Context("when the coordinator hits max_turns", func() {
		var run result
		BeforeEach(func() {
			work := stageExampleSwarm(unedited)
			withFakeZai(swarmScriptServer(work, true))
			run = runSwarmExecute(work, "--max-turns", "1")
		})

		It("exits 5", func() {
			Expect(run.exit).To(Equal(5))
		})
		It("reports the run as failed with the reason max_turns", func() {
			Expect(decodeExecutedSwarm(run.stdout).Reason).To(Equal("max_turns"))
		})
		It("runs no gate", func() {
			Expect(decodeExecutedSwarm(run.stdout).Gates).To(BeEmpty())
		})
	})

	It("exits 3 when the provider fails the coordinator", func() {
		withFakeZai(e2eErrorProvider())
		Expect(runSwarmExecute(stageExampleSwarm(unedited)).exit).To(Equal(3))
	})

	Context("when a precondition is not met", func() {
		var sessions func() string
		BeforeEach(func() {
			withFakeZai(fakeZaiServer(`{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`))
			sessions = func() string { return runArgv([]string{"session", "list", "--db", dbFor()}).stdout }
		})

		It("exits 2 for a manifest with no coordinator, naming the fix", func() {
			work := stageExampleSwarm(func(s string) string { return strings.Replace(s, "coordinator: coordinator\n", "", 1) })
			Expect(runSwarmExecute(work).stderr).To(ContainSubstring("add `coordinator: "))
		})
		It("exits 2 for a manifest with no coordinator", func() {
			work := stageExampleSwarm(func(s string) string { return strings.Replace(s, "coordinator: coordinator\n", "", 1) })
			Expect(runSwarmExecute(work).exit).To(Equal(2))
		})
		It("creates nothing for a manifest with no coordinator", func() {
			work := stageExampleSwarm(func(s string) string { return strings.Replace(s, "coordinator: coordinator\n", "", 1) })
			_ = runSwarmExecute(work)
			Expect(sessions()).To(BeEmpty())
		})
		It("exits 2 for a role with no profile", func() {
			work := stageExampleSwarm(func(s string) string { return strings.Replace(s, "role: reviewer", "role: ghost", 1) })
			Expect(runSwarmExecute(work).exit).To(Equal(2))
		})
		It("creates nothing for a role with no profile", func() {
			work := stageExampleSwarm(func(s string) string { return strings.Replace(s, "role: reviewer", "role: ghost", 1) })
			_ = runSwarmExecute(work)
			Expect(sessions()).To(BeEmpty())
		})
		It("exits 4 for a profile found in both the user and the project directory", func() {
			work := stageExampleSwarm(unedited)
			base, err := os.UserConfigDir()
			if err != nil {
				panic(err)
			}
			dup := filepath.Join(base, "flowstate", "agents")
			if err := os.MkdirAll(dup, 0o700); err != nil {
				panic(err)
			}
			raw, err := os.ReadFile(filepath.Join(work, ".flowstate", "agents", "reader.md"))
			if err != nil {
				panic(err)
			}
			if err := os.WriteFile(filepath.Join(dup, "reader.md"), raw, 0o600); err != nil {
				panic(err)
			}
			Expect(runSwarmExecute(work, "--agents", "both").exit).To(Equal(4))
		})
		DescribeTable("exits 2 and creates nothing for a --workdir that is not a directory",
			func(prepare func(work string) string, agents string) {
				work := stageExampleSwarm(unedited)
				bad := prepare(work)
				argv := []string{"swarm", "run", filepath.Join(work, "review-swarm.yaml"), "--execute", "--db", dbFor(),
					"--agents", agents, "--workdir", bad, "--provider", "z.ai", "--model", "glm-5.3"}
				got := runArgv(argv)
				Expect(workdirRefusal{got.exit, sessions(), strings.Contains(got.stderr, "is not a directory")}).To(
					Equal(workdirRefusal{2, "", true}))
			},
			Entry("a missing directory", func(work string) string { return filepath.Join(work, "nope") }, "project"),
			Entry("a missing directory under --agents project", func(work string) string { return filepath.Join(work, "x", "y") }, "project"),
			Entry("a regular file", func(work string) string { return filepath.Join(work, "review-swarm.yaml") }, "user"),
		)
		DescribeTable("exits 2 and creates nothing for a database the swarm's tools could write",
			func(prepare func(work, out string) (workdir string, dbArgs []string, dbFile string)) {
				work, out := stageExampleSwarm(unedited), GinkgoT().TempDir()
				workdir, dbArgs, dbFile := prepare(work, out)
				argv := append([]string{"swarm", "run", filepath.Join(work, "review-swarm.yaml"), "--execute",
					"--agents", "project", "--workdir", workdir, "--provider", "z.ai", "--model", "glm-5.3"}, dbArgs...)
				got := runArgv(argv)
				_, statErr := os.Lstat(dbFile)
				Expect(dbRefusal{got.exit, statErr == nil, strings.Contains(got.stderr, "--db") &&
					strings.Contains(got.stderr, "--workdir")}).To(Equal(dbRefusal{2, false, true}))
			},
			Entry("the default flowstate.db with --workdir .", func(work, _ string) (string, []string, string) {
				previous, err := os.Getwd()
				if err != nil {
					panic(err)
				}
				if err := os.Chdir(work); err != nil {
					panic(err)
				}
				DeferCleanup(func() {
					if err := os.Chdir(previous); err != nil {
						panic(err)
					}
				})
				return ".", nil, filepath.Join(work, "flowstate.db")
			}),
			Entry("a database in the work directory", func(work, _ string) (string, []string, string) {
				db := filepath.Join(work, "x.db")
				return work, []string{"--db", db}, db
			}),
			Entry("a database in a directory that does not exist yet", func(work, _ string) (string, []string, string) {
				db := filepath.Join(work, "new", "x.db")
				return work, []string{"--db", db}, db
			}),
			Entry("a database next to a sidecar that links into the work directory", func(work, out string) (string, []string, string) {
				if err := os.Symlink(filepath.Join(work, "wal"), filepath.Join(out, "x.db-wal")); err != nil {
					panic(err)
				}
				db := filepath.Join(out, "x.db")
				return work, []string{"--db", db}, db
			}),
			Entry("a work directory given as a symlink", func(work, out string) (string, []string, string) {
				link := filepath.Join(out, "link")
				if err := os.Symlink(work, link); err != nil {
					panic(err)
				}
				db := filepath.Join(work, "x.db")
				return link, []string{"--db", db}, db
			}),
		)
		It("accepts a database in the parent of the work directory", func() {
			work := stageExampleSwarm(unedited)
			withFakeZai(swarmScriptServer(work, true))
			Expect(runSwarmExecute(work, "--db", filepath.Join(filepath.Dir(work), "x.db")).exit).To(Equal(0))
		})
		It("runs in a --workdir that is a symlink to a directory", func() {
			work := stageExampleSwarm(unedited)
			link := filepath.Join(GinkgoT().TempDir(), "link")
			if err := os.Symlink(work, link); err != nil {
				panic(err)
			}
			withFakeZai(swarmScriptServer(work, true))
			Expect(runSwarmExecute(work, "--workdir", link).exit).To(Equal(0))
		})
		It("names the flag that lacks a value when --execute follows it", func() {
			argv := []string{"swarm", "run", "x.yaml", "--workdir", "--execute", "--db", dbFor()}
			Expect(runArgv(argv).stderr).To(ContainSubstring("--workdir needs a value, found --execute"))
		})
		It("exits 2 when --execute follows a flag that takes a value", func() {
			argv := []string{"swarm", "run", "x.yaml", "--workdir", "--execute", "--db", dbFor()}
			Expect(runArgv(argv).exit).To(Equal(2))
		})
		It("exits 2 without a provider and model", func() {
			work := stageExampleSwarm(unedited)
			argv := []string{"swarm", "run", filepath.Join(work, "review-swarm.yaml"), "--execute", "--db", dbFor(),
				"--agents", "project", "--workdir", work}
			Expect(runArgv(argv).exit).To(Equal(2))
		})
	})

	DescribeTable("exits 2 for a flag out of range",
		func(flag, value string) {
			Expect(runSwarmExecute(stageExampleSwarm(unedited), flag, value).exit).To(Equal(2))
		},
		Entry("--max-turns 0", "--max-turns", "0"),
		Entry("--max-turns 1001", "--max-turns", "1001"),
		Entry("--budget -1", "--budget", "-1"),
		Entry("--parent, as the engine creates the coordinator", "--parent", "some-id"),
	)

	DescribeTable("exits 2 for an execute-only flag without --execute",
		func(flag, value string) {
			argv := []string{"swarm", "run", "--db", dbFor(), "--parent", "p", "--budget", "100", flag, value, "x.yaml"}
			Expect(runArgv(argv).exit).To(Equal(2))
		},
		Entry("--agents", "--agents", "project"),
		Entry("--provider", "--provider", "z.ai"),
		Entry("--model", "--model", "glm-5.3"),
		Entry("--workdir", "--workdir", "."),
		Entry("--max-turns", "--max-turns", "3"),
		Entry("--max-tokens", "--max-tokens", "9"),
	)

	It("names the first execute-only flag in the fixed order when several are given", func() {
		argv := []string{"swarm", "run", "--db", dbFor(), "--max-tokens", "9", "--workdir", ".", "--agents", "user", "x.yaml"}
		Expect(runArgv(argv).stderr).To(ContainSubstring("--agents only applies"))
	})

	DescribeTable("maps a stopped coordinator onto the exit code",
		func(res app.ExecuteSwarmResult, want int) {
			errFile, err := os.Create(filepath.Join(GinkgoT().TempDir(), "stderr"))
			if err != nil {
				panic(err)
			}
			DeferCleanup(errFile.Close)
			Expect(swarmExecuteExit(res, &flags{}, errFile)).To(Equal(want))
		},
		Entry("a passed deadline, though its cause is a cancellation",
			app.ExecuteSwarmResult{Status: app.SwarmExecFailed, Reason: "deadline", Cause: app.ErrSessionCancelled}, 1),
		Entry("a cancellation by sigint",
			app.ExecuteSwarmResult{Status: app.SwarmExecCancelled, Reason: "sigint", Cause: app.ErrSessionCancelled}, 130),
	)

	It("names the flag that needs --execute", func() {
		argv := []string{"swarm", "run", "--db", dbFor(), "--agents", "project", "x.yaml"}
		Expect(runArgv(argv).stderr).To(ContainSubstring("--agents only applies to `swarm run --execute`"))
	})

	Context("on SIGINT during the coordinator's turn", func() {
		if runtime.GOOS == "windows" {
			return // Windows has no POSIX interrupt to send; the specs stay in the tree wherever signals exist
		}
		var run result
		BeforeEach(func() {
			work := stageExampleSwarm(unedited)
			srv, arrived := e2eStallProvider()
			cmd, done := e2eStart(srv.URL, "swarm", "run", filepath.Join(work, "review-swarm.yaml"), "--execute",
				"--db", dbFor(), "--agents", "project", "--workdir", work, "--provider", "z.ai", "--model", "glm-5.3")
			DeferCleanup(func() {
				select {
				case <-done:
				default:
					_ = cmd.Process.Kill()
				}
			})
			select {
			case <-arrived:
			case <-time.After(20 * time.Second):
				panic("provider request not observed")
			}
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				panic(err)
			}
			select {
			case run = <-done:
			case <-time.After(20 * time.Second):
				panic("the swarm did not stop on SIGINT")
			}
		})

		It("exits 130", func() {
			Expect(run.exit).To(Equal(130))
		})
		It("reports the run as cancelled with the reason sigint", func() {
			Expect(decodeExecutedSwarm(run.stdout).Reason).To(Equal("sigint"))
		})
		It("leaves no active session", func() {
			Expect(runArgv([]string{"session", "list", "--status", "active", "--db", dbFor()}).stdout).To(BeEmpty())
		})
	})
})

var _ = Describe("the shipped swarm example", func() {
	loadExample := func() (swarm.Manifest, []string) {
		m, err := swarm.LoadManifest(filepath.Join(exampleSwarmDir, "review-swarm.yaml"))
		if err != nil {
			panic(err)
		}
		profiles, err := app.LoadProfiles("project", exampleSwarmDir)
		if err != nil {
			panic(err)
		}
		var names []string
		for _, p := range profiles {
			names = append(names, p.Name)
		}
		return m, names
	}

	It("is a valid manifest", func() {
		argv := []string{"swarm", "validate", filepath.Join(exampleSwarmDir, "review-swarm.yaml")}
		Expect(runArgv(argv).exit).To(Equal(0))
	})
	It("can be executed with its own profiles", func() {
		m, names := loadExample()
		Expect(swarm.CheckExecutable(m, names)).To(Succeed())
	})
	It("lists its profiles through `agents list`", func() {
		Expect(runArgv([]string{"agents", "list", "--agents", "project", "--workdir", exampleSwarmDir}).exit).To(Equal(0))
	})
	It("lets the coordinator delegate to the reader and the reviewer", func() {
		profiles, err := app.LoadProfiles("project", exampleSwarmDir)
		if err != nil {
			panic(err)
		}
		for _, p := range profiles {
			if p.Name == "coordinator" {
				Expect(p.Delegates).To(ConsistOf("reader", "reviewer"))
			}
		}
	})
})
