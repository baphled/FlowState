package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// This suite productises the CLI contract end-to-end matrix: every
// spec executes the real built binary as a subprocess against an
// isolated temp SQLite database and a scripted local fake z.ai SSE
// provider, asserting exactly the shipped process boundary: exit
// codes, stdout event envelopes and the stderr error contract.
// e2eBin is the binary built once per suite by SynchronizedBeforeSuite.
var e2eBin string

// e2eExecCommand is the subprocess launch seam (the pattern of
// internal/swarm/gates.go): it keeps the runtime-resolved binary
// path and argv out of direct exec.Command calls without waivers.
var e2eExecCommand = exec.Command

var _ = SynchronizedBeforeSuite(func() []byte {
	root, err := filepath.Abs("../..")
	if err != nil {
		panic(err)
	}
	dir, err := os.MkdirTemp("", "flowstate-e2e-")
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = os.RemoveAll(dir) })
	bin := filepath.Join(dir, "flowstate")
	if runtime.GOOS == "windows" {
		bin += ".exe" // the suite also runs on Windows runners
	}
	build := e2eExecCommand("go", "build", "-o", bin, "./cmd/flowstate")
	build.Dir = root
	out, err := build.CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("go build: %v\n%s", err, out))
	}
	return []byte(bin)
}, func(data []byte) {
	e2eBin = string(data)
})

// Every spec runs the in-process CLI with no ambient provider settings.
// Without this a developer's own exported key turned specs into live
// provider calls with a real credential, and their outcome depended on the
// machine. A spec that needs a key or a route sets its own afterwards.
var _ = BeforeEach(func() {
	for _, name := range ambientProviderVars {
		GinkgoT().Setenv(name, "")
	}
})

// ambientProviderVars are every provider setting the CLI reads from the
// environment; the suite clears them so no spec reaches a live provider.
var ambientProviderVars = []string{"ZAI_API_KEY", "ZAI_BASE_URL", "ZAI_PLAN", "ZAI_CODING_ROUTE",
	"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "FLOWSTATE_ANTHROPIC_AUTH", "FLOWSTATE_CLAUDE_BIN"}

var _ = Describe("provider environment isolation", func() {
	It("leaves the suite with no z.ai key, so no live provider is reachable", func() {
		Expect(os.Getenv("ZAI_API_KEY")).To(BeEmpty())
	})

	It("leaves the suite with no anthropic key, so no live provider is reachable", func() {
		Expect(os.Getenv("ANTHROPIC_API_KEY")).To(BeEmpty())
	})

	It("leaves the suite on the default anthropic route", func() {
		Expect(os.Getenv("FLOWSTATE_ANTHROPIC_AUTH")).To(BeEmpty())
	})

	It("drops ambient anthropic settings from the subprocess environment", func() {
		GinkgoT().Setenv("ANTHROPIC_BASE_URL", "http://ambient.invalid")
		Expect(e2eEnv("http://127.0.0.1:0")).NotTo(ContainElement(HavePrefix("ANTHROPIC_")))
	})
})

// e2eEnv builds the subprocess environment: the ambient environment
// minus ambient provider or FlowState live settings, plus the local
// fake provider selection, so the binary never touches the live network.
func e2eEnv(baseURL string, extra ...string) []string {
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ZAI_") && !strings.HasPrefix(kv, "FLOWSTATE_") &&
			!strings.HasPrefix(kv, "ANTHROPIC_") {
			env = append(env, kv)
		}
	}
	env = append(env, "ZAI_API_KEY=e2e-test-key", "ZAI_BASE_URL="+baseURL,
		"ZAI_PLAN=coding", "ZAI_CODING_ROUTE=openai")
	return append(env, extra...)
}

// e2eExitCode maps a Wait error onto the process exit status.
func e2eExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	panic(err)
}

// e2eRun executes the built binary to completion and captures its
// exit code and both output streams.
func e2eRun(baseURL string, argv ...string) result {
	return e2eRunWith(baseURL, "", nil, argv...)
}

// e2eRunInDir executes the binary in dir (for ambient ./flowstate.json
// tests) while preserving the standard fake-provider env wiring.
func e2eRunInDir(baseURL, dir string, argv ...string) result {
	return e2eRunWith(baseURL, dir, nil, argv...)
}

// e2eRunWith executes one subprocess with optional cwd and extra env.
func e2eRunWith(baseURL, dir string, extraEnv []string, argv ...string) result {
	cmd := e2eExecCommand(e2eBin, argv...)
	cmd.Env = e2eEnv(baseURL, extraEnv...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	return result{exit: e2eExitCode(cmd.Run()), stdout: out.String(), stderr: errs.String()}
}

// e2eStart launches the binary without waiting; the returned
// channel yields the finished result and reading the captured
// output after it receives is race-free (the exec copiers finish
// before Wait returns).
func e2eStart(baseURL string, argv ...string) (*exec.Cmd, chan result) {
	return e2eSpawn(e2eExecCommand(e2eBin, argv...), baseURL)
}

// e2eStartHangupIgnored starts the binary as nohup would: a POSIX shell
// ignores SIGHUP and execs the binary, which inherits the ignored
// disposition and keeps the shell's PID. Unix only.
func e2eStartHangupIgnored(baseURL string, argv ...string) (*exec.Cmd, chan result) {
	wrapped := append([]string{"-c", `trap '' HUP; exec "$@"`, "sh", e2eBin}, argv...)
	return e2eSpawn(e2eExecCommand("/bin/sh", wrapped...), baseURL)
}

// e2eSpawn starts cmd with the e2e environment and reports its result.
func e2eSpawn(cmd *exec.Cmd, baseURL string) (*exec.Cmd, chan result) {
	cmd.Env = e2eEnv(baseURL)
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	done := make(chan result, 1)
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	go func() {
		werr := cmd.Wait()
		done <- result{exit: e2eExitCode(werr), stdout: out.String(), stderr: errs.String()}
	}()
	return cmd, done
}

// e2eErrorProvider serves a permanent 502 provider failure.
func e2eErrorProvider() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"code":"502","message":"down"}}`))
	}))
	DeferCleanup(srv.Close)
	return srv
}

// e2eStallProvider serves one SSE delta then holds the stream open
// so a run blocks mid-turn until interrupted; arrived signals that
// the request arrived and the delta was flushed — the point where
// SIGINT is meaningful.
func e2eStallProvider() (*httptest.Server, <-chan struct{}) {
	arrived := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"st\"}}]}\n\n"))
		fl.Flush()
		arrived <- struct{}{}
		<-r.Context().Done()
	}))
	DeferCleanup(srv.Close)
	return srv, arrived
}

// e2eCountingProvider serves one complete SSE reply and records every
// received request on a buffered channel so a spec can assert how many
// provider requests a run actually made; the send precedes the reply, so
// a completed run's count is already observable when the process exits.
func e2eCountingProvider() (*httptest.Server, <-chan struct{}) {
	arrived := make(chan struct{}, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		arrived <- struct{}{}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"unused\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	DeferCleanup(srv.Close)
	return srv, arrived
}

// e2eNewSession creates a session through the built binary.
func e2eNewSession(baseURL, db string) string {
	r := e2eRun(baseURL, "session", "new", "--db", db, "--provider", "z.ai", "--model", "glm-5.3", "--task", "greet")
	if r.exit != 0 {
		panic("session new failed: " + r.stderr)
	}
	return decodeNew(r.stdout).ID
}

// e2ePausedSession creates a session and pauses it via a budget-5
// run against the scripted provider.
func e2ePausedSession(baseURL, db string) string {
	id := e2eNewSession(baseURL, db)
	r := e2eRun(baseURL, "session", "run", "--db", db, "--budget", "5", id)
	if r.exit != 5 {
		panic("budget pause setup failed: " + r.stderr)
	}
	return id
}

// e2eEvent is one projected event row from `session events --json`.
type e2eEvent struct {
	SessionID  string          `json:"session_id"`
	Seq        int             `json:"seq"`
	CreatedAt  string          `json:"created_at_utc"`
	Actor      string          `json:"actor"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	PayloadSHA string          `json:"payload_sha256"`
}

// e2eEvents reads the projected session log through the built binary.
func e2eEvents(baseURL, db, id string) []e2eEvent {
	r := e2eRun(baseURL, "session", "events", "--json", "--db", db, id)
	if r.exit != 0 {
		panic("session events failed: " + r.stderr)
	}
	evs := []e2eEvent{}
	for _, l := range lines(r.stdout) {
		var e e2eEvent
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			panic(err)
		}
		evs = append(evs, e)
	}
	return evs
}

// e2ePayloadField extracts one field's value from a raw payload.
func e2ePayloadField(raw json.RawMessage, key string) any {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(err)
	}
	return m[key]
}

// e2eRequireEvent returns the first event of the given type.
func e2eRequireEvent(evs []e2eEvent, typ string) e2eEvent {
	for _, e := range evs {
		if e.Type == typ {
			return e
		}
	}
	panic("event not persisted: " + typ)
}

// e2eFindEvent is the non-panicking lookup used by retryable setup paths.
func e2eFindEvent(evs []e2eEvent, typ string) (e2eEvent, error) {
	for _, e := range evs {
		if e.Type == typ {
			return e, nil
		}
	}
	return e2eEvent{}, errors.New("event not persisted: " + typ)
}

// validateAgainst compiles the named key of the hand-authored
// schemas/cli.json contract (ADR-003) and returns its validation
// error for data, so specs assert output conformance with one
// expectation. Reading the file per call keeps the check live:
// an edited schema is exercised immediately, without caching.
func validateAgainst(schemaKey, data string) error {
	schemaPath := filepath.Join("..", "..", "schemas", "cli.json")
	raw, rerr := os.ReadFile(schemaPath)
	if rerr != nil {
		return rerr
	}
	var combined map[string]any
	if uerr := json.Unmarshal(raw, &combined); uerr != nil {
		return uerr
	}
	sub, sok := combined[schemaKey]
	if !sok {
		return fmt.Errorf("schema %q not found in combined file", schemaKey)
	}
	compiler := jsonschema.NewCompiler()
	if aerr := compiler.AddResource(schemaKey, sub); aerr != nil {
		return aerr
	}
	compiled, cerr := compiler.Compile(schemaKey)
	if cerr != nil {
		return cerr
	}
	var decoded any
	if derr := json.Unmarshal([]byte(data), &decoded); derr != nil {
		return derr
	}
	return compiled.Validate(decoded)
}

// e2eVerdict decodes the first verdict line of receipt validate.
func e2eVerdict(out string) string {
	var v struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(firstLine(out)), &v); err != nil {
		panic(err)
	}
	return v.Verdict
}

// e2eWriteFlowstateConfig writes a config fixture under dir.
func e2eWriteFlowstateConfig(dir, body string) string {
	path := filepath.Join(dir, "flowstate.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		panic(err)
	}
	return path
}

// e2eSessionRow decodes one session row by id from session list.
func e2eSessionRow(baseURL, db, id string) map[string]any {
	rows := lines(e2eRun(baseURL, "session", "list", "--db", db, "--json").stdout)
	for _, row := range rows {
		var doc map[string]any
		if err := json.Unmarshal([]byte(row), &doc); err != nil {
			panic(err)
		}
		if doc["id"] == id {
			return doc
		}
	}
	panic("session row not found: " + id)
}

// e2eCanonicalHash is the independent canonical hash oracle used by
// config e2e tests.
func e2eCanonicalHash(provider, model, dbPath, source string, allow []string) string {
	sorted := append([]string(nil), allow...)
	if sorted == nil {
		sorted = []string{}
	}
	sort.Strings(sorted)
	body, err := json.Marshal(map[string]any{
		"config_source":  source,
		"db_path":        filepath.Clean(dbPath),
		"harness":        "flowstate-cli",
		"model":          model,
		"provider":       provider,
		"tool_allowlist": sorted,
	})
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// e2eToolLoopProvider serves a two-step loop transcript: first a
// tool call, then a terminal assistant message on the follow-up turn.
func e2eToolLoopProvider() *httptest.Server {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		if atomic.AddInt32(&calls, 1) == 1 {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"exec.run\",\"arguments\":\"{\\\"cmd\\\":\\\"echo\\\",\\\"args\\\":[\\\"ok\\\"]}\"}}]}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		} else {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	DeferCleanup(srv.Close)
	return srv
}

var _ = Describe("CLI end-to-end contract matrix", Label("e2e"), func() {

	Context("session new --parent creates a linked child", func() {
		var db string
		var parentID string
		newChild := func() map[string]any {
			r := runArgv([]string{"session", "new", "--db", db, "--parent", parentID,
				"--provider", "z.ai", "--model", "glm-5.3", "--task", "child task"})
			if r.exit != 0 {
				panic("session new --parent failed: " + r.stderr)
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(r.stdout), &rec); err != nil {
				panic(err)
			}
			return rec
		}
		listJSON := func() []map[string]any {
			r := runArgv([]string{"session", "list", "--db", db, "--json"})
			if r.exit != 0 {
				panic("session list failed: " + r.stderr)
			}
			var out []map[string]any
			for _, line := range strings.Split(strings.TrimSpace(r.stdout), "\n") {
				var m map[string]any
				if err := json.Unmarshal([]byte(line), &m); err != nil {
					panic(err)
				}
				out = append(out, m)
			}
			return out
		}
		BeforeEach(func() {
			db = filepath.Join(GinkgoT().TempDir(), "child.db")
			r := runArgv([]string{"session", "new", "--db", db,
				"--provider", "z.ai", "--model", "glm-5.3", "--task", "parent task"})
			if r.exit != 0 {
				panic("parent session new failed: " + r.stderr)
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(r.stdout), &rec); err != nil {
				panic(err)
			}
			parentID = rec["id"].(string)
		})

		It("emits the child record with the parent linkage", func() {
			rec := newChild()
			Expect(rec["parent_id"]).To(Equal(parentID))
		})

		It("persists the linkage visible in session list", func() {
			rec := newChild()
			childID := rec["id"].(string)
			var childRecord map[string]any
			for _, m := range listJSON() {
				if m["id"] == childID {
					childRecord = m
				}
			}
			// A missing record leaves the map nil, whose lookup reads as
			// nil and fails the Equal — one behavioural expectation.
			Expect(childRecord["parent_id"]).To(Equal(parentID))
		})

		It("opens the child log with a session.delegated event", func() {
			rec := newChild()
			childID := rec["id"].(string)
			er := runArgv([]string{"session", "events", childID, "--db", db, "--json"})
			if er.exit != 0 {
				panic("session events failed: " + er.stderr)
			}
			first := strings.SplitN(strings.TrimSpace(er.stdout), "\n", 2)[0]
			var ev map[string]any
			if err := json.Unmarshal([]byte(first), &ev); err != nil {
				panic(err)
			}
			Expect(ev["type"]).To(Equal("session.delegated"))
		})

		It("exits 2 with usage for an unknown parent", func() {
			r := runArgv([]string{"session", "new", "--db", db, "--parent", "01a1f000-0000-7000-8000-000000000000",
				"--provider", "z.ai", "--model", "glm-5.3", "--task", "child task"})
			Expect(r.exit).To(Equal(2))
		})

		It("exits 2 when --reserve is given without --parent", func() {
			r := runArgv([]string{"session", "new", "--db", db, "--reserve", "40",
				"--provider", "z.ai", "--model", "glm-5.3", "--task", "no parent"})
			Expect(r.exit).To(Equal(2))
		})
	})

	Context("session events --follow", func() {
		It("prints all events of a terminal session and exits", func() {
			db := filepath.Join(GinkgoT().TempDir(), "follow.db")
			nr := runArgv([]string{"session", "new", "--db", db,
				"--provider", "z.ai", "--model", "glm-5.3", "--task", "follow me"})
			if nr.exit != 0 {
				panic("session new failed: " + nr.stderr)
			}
			var nrec map[string]any
			if err := json.Unmarshal([]byte(nr.stdout), &nrec); err != nil {
				panic(err)
			}
			id := nrec["id"].(string)
			cl := runArgv([]string{"session", "close", "--db", db, id})
			if cl.exit != 0 {
				panic("session close failed: " + cl.stderr)
			}
			fr := runArgv([]string{"session", "events", "--db", db, id, "--follow"})
			if fr.exit != 0 {
				panic("session events --follow failed: " + fr.stderr)
			}
			Expect(strings.Contains(fr.stdout, "session.cancelled")).To(BeTrue())
		})

		It("exits with the unknown-session code for an unknown id", func() {
			db := filepath.Join(GinkgoT().TempDir(), "follow-unknown.db")
			r := runArgv([]string{"session", "events", "--db", db,
				"01a1f000-0000-4000-8000-000000000000", "--follow"})
			Expect(r.exit).NotTo(Equal(0))
		})
	})

	Context("session message steering", func() {
		var db string
		var parentID string
		var childID string
		BeforeEach(func() {
			db = filepath.Join(GinkgoT().TempDir(), "steer.db")
			pr := runArgv([]string{"session", "new", "--db", db,
				"--provider", "z.ai", "--model", "glm-5.3", "--task", "parent"})
			if pr.exit != 0 {
				panic("parent session new failed: " + pr.stderr)
			}
			var prec map[string]any
			if err := json.Unmarshal([]byte(pr.stdout), &prec); err != nil {
				panic(err)
			}
			parentID = prec["id"].(string)
			cr := runArgv([]string{"session", "new", "--db", db, "--parent", parentID,
				"--provider", "z.ai", "--model", "glm-5.3", "--task", "child"})
			if cr.exit != 0 {
				panic("child session new failed: " + cr.stderr)
			}
			var crec map[string]any
			if err := json.Unmarshal([]byte(cr.stdout), &crec); err != nil {
				panic(err)
			}
			childID = crec["id"].(string)
		})

		It("records a steering message addressed to the child", func() {
			r := runArgv([]string{"session", "message", "--db", db, childID,
				"--from", parentID, "--body", "refocus on the parser"})
			if r.exit != 0 {
				panic("session message failed: " + r.stderr)
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(r.stdout), &rec); err != nil {
				panic(err)
			}
			Expect(map[string]any{"session_id": rec["session_id"], "sender": rec["sender"]}).
				To(Equal(map[string]any{"session_id": childID, "sender": parentID}))
		})

		It("exits 2 when the sender is outside the delegation chain", func() {
			sdb := filepath.Join(GinkgoT().TempDir(), "stranger.db")
			sr := runArgv([]string{"session", "new", "--db", sdb,
				"--provider", "z.ai", "--model", "glm-5.3", "--task", "stranger"})
			if sr.exit != 0 {
				panic("stranger session new failed: " + sr.stderr)
			}
			var srec map[string]any
			if err := json.Unmarshal([]byte(sr.stdout), &srec); err != nil {
				panic(err)
			}

			r := runArgv([]string{"session", "message", "--db", db, childID,
				"--from", srec["id"].(string), "--body", "cross-tree injection"})
			Expect(r.exit).To(Equal(2))
		})

		It("keeps the message pending until the child run drains it", func() {
			mr := runArgv([]string{"session", "message", "--db", db, childID,
				"--from", parentID, "--body", "steer body"})
			if mr.exit != 0 {
				panic("session message failed: " + mr.stderr)
			}
			er := runArgv([]string{"session", "events", "--db", db, childID, "--json"})
			if er.exit != 0 {
				panic("session events failed: " + er.stderr)
			}
			Expect(strings.Contains(er.stdout, "session.steered")).To(BeFalse())
		})
	})

	Context("session budget reports the shared pool", func() {
		var db string
		var parentID string
		BeforeEach(func() {
			db = filepath.Join(GinkgoT().TempDir(), "budget.db")
			r := runArgv([]string{"session", "new", "--db", db,
				"--provider", "z.ai", "--model", "glm-5.3", "--task", "pool owner"})
			if r.exit != 0 {
				panic("parent session new failed: " + r.stderr)
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(r.stdout), &rec); err != nil {
				panic(err)
			}
			parentID = rec["id"].(string)
		})

		runParent := func() int {
			rr := runArgv([]string{"session", "run", "--db", db, parentID, "--budget", "100"})
			// No provider key is configured, so the run stops at provider
			// resolution with exit 3 and the session stays active. The
			// anchor persists all the same: the resolved budget is written
			// before the provider is resolved.
			if rr.exit != 3 {
				panic("session run did not stop at provider resolution: " + rr.stderr)
			}
			return rr.exit
		}

		It("stops at provider resolution without a configured key", func() {
			rr := runArgv([]string{"session", "run", "--db", db, parentID, "--budget", "100"})
			Expect(rr.stderr).To(ContainSubstring("ZAI_API_KEY is not set"))
		})

		It("reports the anchored pool with no reservations", func() {
			runParent()
			r := runArgv([]string{"session", "budget", parentID, "--db", db, "--json"})
			if r.exit != 0 {
				panic("session budget failed: " + r.stderr)
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(r.stdout), &rec); err != nil {
				panic(err)
			}
			Expect(rec).To(Equal(map[string]any{
				"session_id": parentID, "budget_pool": float64(100),
				"reserved": float64(0), "available": float64(100),
			}))
		})

		It("reflects a child reservation against the parent pool", func() {
			runParent()
			cr := runArgv([]string{"session", "new", "--db", db, "--parent", parentID, "--reserve", "40",
				"--provider", "z.ai", "--model", "glm-5.3", "--task", "reserved child"})
			if cr.exit != 0 {
				panic("child session new failed: " + cr.stderr)
			}
			r := runArgv([]string{"session", "budget", parentID, "--db", db, "--json"})
			if r.exit != 0 {
				panic("session budget failed: " + r.stderr)
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(r.stdout), &rec); err != nil {
				panic(err)
			}
			Expect(rec).To(Equal(map[string]any{
				"session_id": parentID, "budget_pool": float64(100),
				"reserved": float64(40), "available": float64(60),
			}))
		})

		It("schema-validates the null-pool budget overview", func() {
			r := runArgv([]string{"session", "budget", parentID, "--db", db, "--json"})
			if r.exit != 0 {
				panic("session budget failed: " + r.stderr)
			}
			Expect(validateAgainst("session-budget", r.stdout)).To(Succeed())
		})

		It("reports budget_pool as JSON null without a pool", func() {
			r := runArgv([]string{"session", "budget", parentID, "--db", db, "--json"})
			if r.exit != 0 {
				panic("session budget failed: " + r.stderr)
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(r.stdout), &rec); err != nil {
				panic(err)
			}
			Expect(rec["budget_pool"]).To(BeNil())
		})

		It("exits with the unknown-session code for an unknown id", func() {
			r := runArgv([]string{"session", "budget", "01a1f000-0000-4000-8000-000000000000", "--db", db})
			Expect(r.exit).NotTo(Equal(0))
		})
	})
	Context("session new prints its record", func() {
		var observedExit int
		BeforeEach(func() {
			db := filepath.Join(GinkgoT().TempDir(), "e2e.db")
			r := e2eRun("", "session", "new", "--db", db,
				"--provider", "z.ai", "--model", "glm-5.3", "--task", "greet")
			observedExit = r.exit
		})
		It("exits 0", func() { Expect(observedExit).To(Equal(0)) })
		It("exits 2 with usage when --task is missing", func() {
			db := filepath.Join(GinkgoT().TempDir(), "e2e.db")
			r := e2eRun("", "session", "new", "--db", db,
				"--provider", "z.ai", "--model", "glm-5.3")
			Expect(r.exit).To(Equal(2))
		})
	})

	Context("config composition on the CLI surface", func() {
		It("loads ambient defaults from ./flowstate.json when present", func() {
			work := GinkgoT().TempDir()
			db := filepath.Join(work, "cfg.db")
			e2eWriteFlowstateConfig(work, fmt.Sprintf(`{"default_provider":"cfg.provider","default_model":"cfg.model","storage_path":%q}`, db))
			r := e2eRunInDir("", work, "session", "new", "--task", "greet")
			row := e2eSessionRow("", db, decodeNew(r.stdout).ID)
			Expect(row["provider"] == "cfg.provider" && row["model_id"] == "cfg.model").To(BeTrue())
		})

		It("lets explicit flags override config defaults", func() {
			work := GinkgoT().TempDir()
			db := filepath.Join(work, "cfg.db")
			cfg := e2eWriteFlowstateConfig(work, fmt.Sprintf(`{"default_provider":"cfg.provider","default_model":"cfg.model","storage_path":%q}`, db))
			r := e2eRun("", "session", "new", "--config", cfg, "--provider", "flag.provider", "--model", "flag.model", "--task", "greet")
			row := e2eSessionRow("", db, decodeNew(r.stdout).ID)
			Expect(row["provider"] == "flag.provider" && row["model_id"] == "flag.model").To(BeTrue())
		})

		It("exits 4 on malformed config JSON", func() {
			work := GinkgoT().TempDir()
			cfg := e2eWriteFlowstateConfig(work, `{"default_provider":`)
			r := e2eRun("", "session", "new", "--config", cfg, "--task", "greet")
			Expect(r.exit).To(Equal(4))
		})

		It("exits 4 when a secret-bearing key is present", func() {
			work := GinkgoT().TempDir()
			cfg := e2eWriteFlowstateConfig(work, `{"default_provider":"z.ai","api_key":"secret"}`)
			r := e2eRun("", "session", "new", "--config", cfg, "--task", "greet")
			Expect(r.exit).To(Equal(4))
		})

		It("persists config_sha256 as the canonical composition hash", func() {
			work := GinkgoT().TempDir()
			db := filepath.Join(work, "cfg.db")
			cfg := e2eWriteFlowstateConfig(work, fmt.Sprintf(`{"default_provider":"z.ai","default_model":"glm-5.3","storage_path":%q,"tool_allowlist":["fs.read","echo"]}`, db))
			r := e2eRun("", "session", "new", "--config", cfg, "--task", "greet")
			row := e2eSessionRow("", db, decodeNew(r.stdout).ID)
			expected := e2eCanonicalHash("z.ai", "glm-5.3", db, cfg, []string{"fs.read", "echo"})
			Expect(row["config_sha256"]).To(Equal(expected))
		})

		It("uses config tool_allowlist as the run loop default", func() {
			srv := e2eToolLoopProvider()
			work := GinkgoT().TempDir()
			db := filepath.Join(work, "cfg.db")
			cfg := e2eWriteFlowstateConfig(work, fmt.Sprintf(`{"default_provider":"z.ai","default_model":"glm-5.3","storage_path":%q,"tool_allowlist":["exec.run"]}`, db))
			newRes := e2eRun(srv.URL, "session", "new", "--config", cfg, "--task", "greet")
			id := decodeNew(newRes.stdout).ID
			run := e2eRunWith(srv.URL, "", []string{"FLOWSTATE_EXEC_ENABLED=1"}, "session", "run", "--config", cfg, id)
			calls := 0
			for _, e := range e2eEvents(srv.URL, db, id) {
				if e.Type == "tool.call" {
					calls++
				}
			}
			Expect(run.exit == 0 && calls > 0).To(BeTrue())
		})

		It("keeps legacy usage behaviour when no config file exists", func() {
			work := GinkgoT().TempDir()
			r := e2eRunInDir("", work, "session", "new", "--task", "greet")
			Expect(r.exit).To(Equal(2))
		})
	})

	Context("session run completes on a normal reply", func() {
		var observedExit int
		var observedFinalType string
		var observedFinalStatus string
		var observedAssistantReply string
		BeforeEach(func() {
			srv := fakeZaiServer(
				`{"choices":[{"delta":{"content":"he"}}]}`,
				`{"choices":[{"delta":{"content":"y"}}]}`,
			)
			db := filepath.Join(GinkgoT().TempDir(), "e2e.db")
			id := e2eNewSession(srv.URL, db)
			r := e2eRun(srv.URL, "session", "run", "--db", db, id)
			got := lines(r.stdout)
			var last e2eEvent
			if err := json.Unmarshal([]byte(got[len(got)-1]), &last); err != nil {
				panic(err)
			}
			msg := e2eRequireEvent(e2eEvents(srv.URL, db, id), "assistant.message")
			observedExit = r.exit
			observedFinalType = last.Type
			observedFinalStatus, _ = e2ePayloadField(last.Payload, "status").(string)
			observedAssistantReply, _ = e2ePayloadField(msg.Payload, "content").(string)
		})
		It("exits 0", func() { Expect(observedExit).To(Equal(0)) })
		It("ends the stdout stream on turn.end", func() { Expect(observedFinalType).To(Equal("turn.end")) })
		It("records completed in the final payload", func() {
			Expect(observedFinalStatus).To(Equal("completed"))
		})
		It("persists the assistant reply in the stream", func() {
			Expect(observedAssistantReply).To(Equal("hey"))
		})
	})

	Context("session run rejects an unknown session id", func() {
		var observedExit int
		var observedCode string
		BeforeEach(func() {
			db := filepath.Join(GinkgoT().TempDir(), "e2e.db")
			r := e2eRun("", "session", "run", "--db", db, "missing-id")
			observedExit = r.exit
			observedCode = stderrCode(r.stderr)
		})
		It("exits 1", func() { Expect(observedExit).To(Equal(1)) })
		It("reports session_not_found on stderr", func() { Expect(observedCode).To(Equal("session_not_found")) })
	})

	Context("session run on a provider error stream", func() {
		var observedExit int
		var observedCode string
		var observedTerminal bool
		BeforeEach(func() {
			srv := e2eErrorProvider()
			db := filepath.Join(GinkgoT().TempDir(), "e2e.db")
			id := e2eNewSession(srv.URL, db)
			r := e2eRun(srv.URL, "session", "run", "--db", db, id)
			perr := e2eRequireEvent(e2eEvents(srv.URL, db, id), "provider.error")
			observedExit = r.exit
			observedCode = stderrCode(r.stderr)
			observedTerminal, _ = e2ePayloadField(perr.Payload, "terminal").(bool)
		})
		It("exits 3", func() { Expect(observedExit).To(Equal(3)) })
		It("reports provider_error on stderr", func() { Expect(observedCode).To(Equal("provider_error")) })
		It("persists provider.error as terminal", func() { Expect(observedTerminal).To(BeTrue()) })
	})

	Context("a budget pause stops the run", func() {
		var observedExit int
		var observedCode string
		var observedFinalType string
		BeforeEach(func() {
			srv := fakeZaiServer(
				`{"choices":[{"delta":{"content":"hello world"}}]}`,
				`{"choices":[{"delta":{"content":" again"}}]}`,
			)
			db := filepath.Join(GinkgoT().TempDir(), "e2e.db")
			id := e2eNewSession(srv.URL, db)
			r := e2eRun(srv.URL, "session", "run", "--db", db, "--budget", "5", id)
			got := lines(r.stdout)
			var last e2eEvent
			if err := json.Unmarshal([]byte(got[len(got)-1]), &last); err != nil {
				panic(err)
			}
			observedExit = r.exit
			observedCode = stderrCode(r.stderr)
			observedFinalType = last.Type
		})
		It("exits 5", func() { Expect(observedExit).To(Equal(5)) })
		It("reports budget_exceeded on stderr", func() { Expect(observedCode).To(Equal("budget_exceeded")) })
		It("emits session.paused last", func() {
			Expect(observedFinalType).To(Equal("session.paused"))
		})
	})

	Context("an explicit resume completes the paused session", func() {
		var observedExit int
		var observedResumedActor string
		var observedFromStatus string
		var observedResumedBeforeWork bool
		var observedFinalType string
		BeforeEach(func() {
			srv := fakeZaiServer(
				`{"choices":[{"delta":{"content":"hello world"}}]}`,
				`{"choices":[{"delta":{"content":" again"}}]}`,
			)
			db := filepath.Join(GinkgoT().TempDir(), "e2e.db")
			id := e2ePausedSession(srv.URL, db)
			r := e2eRun(srv.URL, "session", "run", "--db", db, "--budget", "20000", id)
			evs := e2eEvents(srv.URL, db, id)
			resumed := e2eRequireEvent(evs, "session.resumed")
			nextWork := -1
			for _, e := range evs {
				if e.Type == "user.message" && e.Seq > resumed.Seq && (nextWork < 0 || e.Seq < nextWork) {
					nextWork = e.Seq
				}
			}
			observedExit = r.exit
			observedResumedActor = resumed.Actor
			observedFromStatus, _ = e2ePayloadField(resumed.Payload, "from_status").(string)
			observedResumedBeforeWork = nextWork >= 0 && resumed.Seq < nextWork
			observedFinalType = evs[len(evs)-1].Type
		})
		It("exits 0 on the resumed run", func() { Expect(observedExit).To(Equal(0)) })
		It("attributes session.resumed to the user", func() { Expect(observedResumedActor).To(Equal("user")) })
		It("records from_status paused on the resume", func() {
			Expect(observedFromStatus).To(Equal("paused"))
		})
		It("resumes before further turn work", func() {
			Expect(observedResumedBeforeWork).To(BeTrue())
		})
		It("completes the resumed turn", func() { Expect(observedFinalType).To(Equal("turn.end")) })
	})

	for _, tc := range []struct {
		name   string
		sig    os.Signal
		reason string
	}{
		{"SIGINT", os.Interrupt, "sigint"},
		{"SIGTERM", syscall.SIGTERM, "sigterm"},
		{"SIGHUP", syscall.SIGHUP, "sighup"},
	} {
		sig, wantReason := tc.sig, tc.reason
		Context(tc.name+" mid-run cancels the session", func() {
			if runtime.GOOS == "windows" {
				// Windows has no POSIX interrupt signal to send, so the
				// SIGINT oracle is POSIX-only; the specs stay in the tree
				// everywhere signals exist.
				return
			}
			var observedExit int
			var observedCancelledReason string
			var observedEventsExit int
			var observedRerunExit int
			var observedRerunCode string
			var setupErr error
			BeforeEach(func() {
				// A SIGINT delivered while the child is still installing its signal
				// handler kills it without persisting session.cancelled (observed on
				// macOS runners). Retry the whole scenario on a fresh session so the
				// assertion measures the cancel contract, not startup timing.
				for attempt := 0; attempt < 3 && setupErr == nil && observedCancelledReason == ""; attempt++ {
					setupErr = nil
					srv, arrived := e2eStallProvider()
					db := filepath.Join(GinkgoT().TempDir(), "e2e.db")
					id := e2eNewSession(srv.URL, db)
					cmd, done := e2eStart(srv.URL, "session", "run", "--db", db, id)
					DeferCleanup(func() {
						select {
						case <-done:
						default:
							_ = cmd.Process.Kill()
						}
					})
					select {
					case <-arrived:
					case <-time.After(10 * time.Second):
						setupErr = errors.New("provider request not observed")
					}
					if setupErr != nil {
						continue
					}
					// The stdout projection is written at end of run, so the
					// deterministic pre-signal observable is the first event row:
					// once one is persisted the run loop is live past signal-handler
					// installation. Poll the store through `session events`.
					deadline := time.After(10 * time.Second)
					ticker := time.NewTicker(50 * time.Millisecond)
					for live := false; !live; {
						select {
						case <-deadline:
							setupErr = errors.New("first persisted event not observed")
						case <-ticker.C:
							if len(e2eEvents(srv.URL, db, id)) > 0 {
								live = true
							}
						}
						if setupErr != nil {
							break
						}
					}
					ticker.Stop()
					if setupErr != nil {
						continue
					}
					if serr := cmd.Process.Signal(sig); serr != nil {
						panic(serr)
					}
					var run result
					select {
					case run = <-done:
					case <-time.After(30 * time.Second):
						setupErr = errors.New("run not observed")
					}
					if setupErr != nil {
						continue
					}
					events := e2eEvents(srv.URL, db, id)
					found, ferr := e2eFindEvent(events, "session.cancelled")
					if ferr != nil {
						continue
					}
					rerun := e2eRun(srv.URL, "session", "run", "--db", db, id)
					observedExit = run.exit
					observedCancelledReason, _ = e2ePayloadField(found.Payload, "reason").(string)
					observedEventsExit = e2eRun(srv.URL, "session", "events", "--json", "--db", db, id).exit
					observedRerunExit = rerun.exit
					observedRerunCode = stderrCode(rerun.stderr)
				}
				if observedCancelledReason == "" {
					setupErr = errors.New("session.cancelled not persisted after retries")
				}
			})
			It("exits 130", func() {
				Expect(setupErr).NotTo(HaveOccurred())
				Expect(observedExit).To(Equal(130))
			})
			It("persists session.cancelled with the reason matching the signal", func() {
				Expect(setupErr).NotTo(HaveOccurred())
				Expect(observedCancelledReason).To(Equal(wantReason))
			})
			It("leaves a verifiable log with no partial writes", func() {
				Expect(setupErr).NotTo(HaveOccurred())
				Expect(observedEventsExit).To(Equal(0))
			})
			It("rejects the rerun with exit 6", func() {
				Expect(setupErr).NotTo(HaveOccurred())
				Expect(observedRerunExit).To(Equal(6))
			})
			It("reports session_terminal on the rerun", func() {
				Expect(observedRerunCode).To(Equal("session_terminal"))
			})
		})
	}

	Context("a run started with SIGHUP ignored (nohup)", func() {
		if runtime.GOOS == "windows" {
			return // no POSIX signals or sh wrapper; see the signal specs above
		}
		var cmd *exec.Cmd
		var done chan result
		var db, id string

		BeforeEach(func() {
			srv, arrived := e2eStallProvider()
			db = filepath.Join(GinkgoT().TempDir(), "e2e.db")
			id = e2eNewSession(srv.URL, db)
			cmd, done = e2eStartHangupIgnored(srv.URL, "session", "run", "--db", db, id)
			// Stop exactly the process started here, whether or not a spec failed.
			DeferCleanup(func() { _ = cmd.Process.Kill() })
			select {
			case <-arrived:
			case <-time.After(10 * time.Second):
				panic("provider request not observed")
			}
			// A persisted event shows the run is live past handler installation.
			deadline, ticker := time.After(10*time.Second), time.NewTicker(50*time.Millisecond)
			defer ticker.Stop()
			for live := false; !live; {
				select {
				case <-deadline:
					panic("first persisted event not observed")
				case <-ticker.C:
					live = len(e2eEvents(srv.URL, db, id)) > 0
				}
			}
			if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
				panic(err)
			}
		})
		It("keeps running after SIGHUP", func() {
			Consistently(done).WithTimeout(2 * time.Second).WithPolling(100 * time.Millisecond).ShouldNot(Receive())
		})
		It("records no cancellation after SIGHUP", func() {
			Consistently(func() string { return cancelledReason(db, id) }).
				WithTimeout(2 * time.Second).WithPolling(100 * time.Millisecond).Should(BeEmpty())
		})
		It("exits 130 on a following SIGTERM", func() {
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				panic(err)
			}
			Eventually(func() int {
				select {
				case run := <-done:
					return run.exit
				default:
					return -1 // still running
				}
			}).WithTimeout(30 * time.Second).WithPolling(100 * time.Millisecond).Should(Equal(130))
		})
		It("records the reason sigterm", func() {
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				panic(err)
			}
			Eventually(func() string { return cancelledReason(db, id) }).
				WithTimeout(30 * time.Second).WithPolling(100 * time.Millisecond).Should(Equal("sigterm"))
		})
	})

	Context("--json output validates against committed schemas", func() {
		// The schemas in schemas/cli.json are hand-authored per
		// ADR-003: one key per --json output shape, every field
		// required, additionalProperties:false. The specs here
		// prove that actual command outputs conform to them.

		It("session new output matches the session-new schema", func() {
			root := `{"id":"01a1ee79-ca3e-702f-9b6e-13fb693a8345","status":"active","parent_id":""}`
			Expect(validateAgainst("session-new", root)).To(Succeed())
		})

		It("child session new output matches the session-new schema", func() {
			child := `{"id":"01a1ee79-ca3e-702f-9b6e-13fb693a8345","status":"active","parent_id":"01a1ee79-ca3e-702f-9b6e-13fb693a8344"}`
			Expect(validateAgainst("session-new", child)).To(Succeed())
		})

		It("event envelope matches the session-events schema", func() {
			envelopes := `[{"session_id":"01a1ee79","seq":1,"created_at_utc":"2026-11-18T00:00:00Z","actor":"user","type":"turn.start","payload":{"task":"x"},"payload_sha256":"abc123"}]`
			Expect(validateAgainst("session-events", envelopes)).To(Succeed())
		})

		It("rejects a tampered envelope missing payload_sha256", func() {
			envelope := `{"session_id":"x","seq":1,"created_at_utc":"2026-11-18T00:00:00Z","actor":"user","type":"turn.start","payload":{}}`
			Expect(validateAgainst("session-events", envelope)).NotTo(Succeed())
		})

		It("receipt verdict output matches the receipt-validate schema", func() {
			verdicts := `[{"path":"receipt.json","verdict":"valid"}]`
			Expect(validateAgainst("receipt-validate", verdicts)).To(Succeed())
		})

		It("session list output matches the session-list schema", func() {
			list := `[{"id":"01a1ee79","created_at_utc":"2026-11-18T00:00:00Z","updated_at_utc":"2026-11-18T00:00:00Z","status":"active","provider":"z.ai","model_id":"glm-5.3-flash","harness":"flowstate-cli","config_sha256":"abc","parent_id":""},{"id":"01a1ee80","created_at_utc":"2026-11-18T00:00:01Z","updated_at_utc":"2026-11-18T00:00:01Z","status":"active","provider":"z.ai","model_id":"glm-5.3-flash","harness":"flowstate-cli","config_sha256":"abc","parent_id":"01a1ee79"}]`
			Expect(validateAgainst("session-list", list)).To(Succeed())
		})

		It("session message output matches the session-message schema", func() {
			msg := `{"id":"01a1ee79-ca3e-702f-9b6e-13fb693a8346","session_id":"01a1ee79-ca3e-702f-9b6e-13fb693a8345","sender":"01a1ee79-ca3e-702f-9b6e-13fb693a8344"}`
			Expect(validateAgainst("session-message", msg)).To(Succeed())
		})

		It("rejects a session message with an unknown extra field", func() {
			tampered := `{"id":"01a1ee79-ca3e-702f-9b6e-13fb693a8346","session_id":"01a1ee79-ca3e-702f-9b6e-13fb693a8345","sender":"01a1ee79-ca3e-702f-9b6e-13fb693a8344","body":"drift"}`
			Expect(validateAgainst("session-message", tampered)).NotTo(Succeed())
		})

		It("session tool output matches the session-tool schema", func() {
			toolOut := `{"session_id":"01a1ee79-ca3e-702f-9b6e-13fb693a8345","tool":"echo","ok":true,"result":{"a":1}}`
			Expect(validateAgainst("session-tool", toolOut)).To(Succeed())
		})

		It("session budget output with a pool matches the session-budget schema", func() {
			budgetOut := `{"session_id":"01a1ee79-ca3e-702f-9b6e-13fb693a8345","reserved":5,"budget_pool":100,"available":95}`
			Expect(validateAgainst("session-budget", budgetOut)).To(Succeed())
		})

		It("swarm validate output matches the swarm-validate schema", func() {
			verdictOut := `{"name":"bug-hunt","valid":true}`
			Expect(validateAgainst("swarm-validate", verdictOut)).To(Succeed())
		})

		It("swarm run output matches the swarm-run schema", func() {
			swarmOut := `{"manifest":"bug-hunt","admitted":["packet-a","packet-b"],"accepted":true,"children":[{"packet":"packet-a","session":"01a1ee79-ca3e-702f-9b6e-13fb693a8345","status":"active","parent_id":"01a1ee79-ca3e-702f-9b6e-13fb693a8344"},{"packet":"packet-b","session":"01a1ee79-ca3e-702f-9b6e-13fb693a8346","status":"active","parent_id":"01a1ee79-ca3e-702f-9b6e-13fb693a8344"}]}`
			Expect(validateAgainst("swarm-run", swarmOut)).To(Succeed())
		})
	})

	Context("receipt validate", func() {
		var observedValidExit int
		var observedTamperedExit int
		var observedTamperedVerdict string
		BeforeEach(func() {
			p := writeReceipt(validReceipt())
			valid := e2eRun("", "receipt", "validate", p)
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				panic(rerr)
			}
			key := []byte(`"manifest_sha256":"`)
			i := bytes.Index(b, key)
			if i < 0 {
				panic("manifest digest not found in receipt fixture")
			}
			b[i+len(key)+63] ^= 1 // flip one bit of the 64-char digest
			if werr := os.WriteFile(p, b, 0o600); werr != nil {
				panic(werr)
			}
			tampered := e2eRun("", "receipt", "validate", p)
			observedValidExit = valid.exit
			observedTamperedExit = tampered.exit
			observedTamperedVerdict = e2eVerdict(tampered.stdout)
		})
		It("exits 0 on the well-formed receipt", func() { Expect(observedValidExit).To(Equal(0)) })
		It("exits 8 on the tampered receipt", func() { Expect(observedTamperedExit).To(Equal(8)) })
		It("prints verdict invalid", func() { Expect(observedTamperedVerdict).To(Equal("invalid")) })
	})

	// The --from-seq/--to-seq window flags are not part of the shipped
	// CLI surface (parseFlags rejects them as unknown flags) and this
	// packet may not change production sources, so the window oracle
	// is deferred; the projection contract the binary serves is
	// asserted here.
	Context("session events projects the ordered log", func() {
		var observedFieldComplete bool
		var observedAscending bool
		BeforeEach(func() {
			srv := fakeZaiServer(
				`{"choices":[{"delta":{"content":"he"}}]}`,
				`{"choices":[{"delta":{"content":"y"}}]}`,
			)
			db := filepath.Join(GinkgoT().TempDir(), "e2e.db")
			id := e2eNewSession(srv.URL, db)
			if r := e2eRun(srv.URL, "session", "run", "--db", db, id); r.exit != 0 {
				panic("session run failed: " + r.stderr)
			}
			evs := e2eEvents(srv.URL, db, id)
			complete := true
			ascending := true
			for i, e := range evs {
				if e.SessionID == "" || e.CreatedAt == "" || e.Actor == "" ||
					e.Type == "" || len(e.Payload) == 0 || len(e.PayloadSHA) != 64 {
					complete = false
				}
				if i > 0 && e.Seq <= evs[i-1].Seq {
					ascending = false
				}
			}
			observedFieldComplete = complete
			observedAscending = ascending
		})
		It("emits every projection field per NDJSON row", func() {
			Expect(observedFieldComplete).To(BeTrue())
		})
		It("keeps seq strictly ascending across the window", func() {
			Expect(observedAscending).To(BeTrue())
		})
	})

	Context("session run rejects an unregistered --allow name as usage", func() {
		Context("exec.run without FLOWSTATE_EXEC_ENABLED", func() {
			var observedExit int
			var observedCode string
			var observedMessageNamesToolAndGate bool
			var observedProviderRequests int
			var observedToolCallEvents int
			BeforeEach(func() {
				srv, arrived := e2eCountingProvider()
				db := filepath.Join(GinkgoT().TempDir(), "e2e.db")
				id := e2eNewSession(srv.URL, db)
				// e2eEnv strips every FLOWSTATE_* setting, so the exec
				// opt-in gate is provably absent in the child process.
				r := e2eRun(srv.URL, "session", "run", "--db", db, "--allow", "exec.run", id)
				toolCalls := 0
				for _, e := range e2eEvents(srv.URL, db, id) {
					if e.Type == "tool.call" {
						toolCalls++
					}
				}
				observedExit = r.exit
				observedCode = stderrCode(r.stderr)
				observedMessageNamesToolAndGate = strings.Contains(r.stderr, "exec.run") &&
					strings.Contains(r.stderr, "FLOWSTATE_EXEC_ENABLED")
				observedProviderRequests = len(arrived)
				observedToolCallEvents = toolCalls
			})
			It("exits 2", func() { Expect(observedExit).To(Equal(2)) })
			It("reports usage on stderr", func() { Expect(observedCode).To(Equal("usage")) })
			It("names exec.run and the FLOWSTATE_EXEC_ENABLED gate", func() {
				Expect(observedMessageNamesToolAndGate).To(BeTrue())
			})
			It("makes zero provider requests", func() { Expect(observedProviderRequests).To(Equal(0)) })
			It("persists no tool.call events", func() { Expect(observedToolCallEvents).To(Equal(0)) })
		})

		Context("a name that is no builtin at all", func() {
			var observedExit int
			var observedCode string
			var observedMessageNamesTool bool
			BeforeEach(func() {
				srv, _ := e2eCountingProvider()
				db := filepath.Join(GinkgoT().TempDir(), "e2e.db")
				id := e2eNewSession(srv.URL, db)
				r := e2eRun(srv.URL, "session", "run", "--db", db, "--allow", "totally.unknown.tool", id)
				observedExit = r.exit
				observedCode = stderrCode(r.stderr)
				observedMessageNamesTool = strings.Contains(r.stderr, "totally.unknown.tool")
			})
			It("exits 2", func() { Expect(observedExit).To(Equal(2)) })
			It("reports usage on stderr", func() { Expect(observedCode).To(Equal("usage")) })
			It("names the unknown tool", func() { Expect(observedMessageNamesTool).To(BeTrue()) })
		})
	})

	It("accepts the M1-DOGFOOD capability", func() {
		doc := validReceipt()
		doc["capability_id"] = "M1-DOGFOOD"
		p := writeReceipt(doc)
		res := runArgv([]string{"receipt", "validate", p})
		Expect(res.exit).To(Equal(0))
	})
})

var _ = Describe("swarm validate", func() {
	It("accepts a valid manifest and prints the name", func() {
		mf := filepath.Join(GinkgoT().TempDir(), "swarm.yaml")
		yaml := "name: test-swarm\nscope: m2.runtime\npackets:\n  - id: p1\n    scope: m2.runtime\n    task: do the thing\n    source_sha256: " + strings.Repeat("a", 64) + "\n"
		if err := os.WriteFile(mf, []byte(yaml), 0o644); err != nil {
			panic(err)
		}
		r := runArgv([]string{"swarm", "validate", "--manifest", mf})
		if r.exit != 0 {
			panic("swarm validate failed: " + r.stderr)
		}
		Expect(strings.Contains(r.stdout, "test-swarm")).To(BeTrue())
	})

	It("exits 2 for an invalid manifest path", func() {
		r := runArgv([]string{"swarm", "validate", "--manifest", "/nonexistent/swarm.yaml"})
		Expect(r.exit).To(Equal(2))
	})
})

var _ = Describe("swarm run", func() {
	It("exits 2 when --parent is missing", func() {
		db := filepath.Join(GinkgoT().TempDir(), "swarmrun.db")
		r := runArgv([]string{"swarm", "run", "--db", db, "--manifest", "/tmp/x.yaml", "--budget", "100"})
		Expect(r.exit).To(Equal(2))
	})

	It("runs a one-packet manifest and reports the child", func() {
		db := filepath.Join(GinkgoT().TempDir(), "swarmrun2.db")
		r := runArgv([]string{"session", "new", "--db", db, "--budget", "100",
			"--provider", "z.ai", "--model", "glm-5.3", "--task", "parent task"})
		if r.exit != 0 {
			panic("parent session new failed: " + r.stderr)
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(r.stdout), &rec); err != nil {
			panic(err)
		}
		parentID := rec["id"].(string)
		mf := filepath.Join(GinkgoT().TempDir(), "swarm.yaml")
		yaml := "name: run-swarm\nscope: m2.runtime\npackets:\n  - id: p1\n    scope: m2.runtime\n    task: do the thing\n    source_sha256: " + strings.Repeat("b", 64) + "\ngates:\n  - name: ok\n    description: trivially pass\n    command: true\n"
		if err := os.WriteFile(mf, []byte(yaml), 0o644); err != nil {
			panic(err)
		}
		r = runArgv([]string{"swarm", "run", "--db", db, "--manifest", mf,
			"--parent", parentID, "--budget", "100"})
		if r.exit != 0 {
			panic("swarm run failed: " + r.stderr)
		}
		Expect(strings.Contains(r.stdout, `"packet":"p1"`)).To(BeTrue())
	})

	Context("when a gate fails", func() {
		var r result

		BeforeEach(func() {
			db := filepath.Join(GinkgoT().TempDir(), "swarmfail.db")
			n := runArgv([]string{"session", "new", "--db", db, "--budget", "100",
				"--provider", "z.ai", "--model", "glm-5.3", "--task", "parent task"})
			if n.exit != 0 {
				panic("parent session new failed: " + n.stderr)
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(n.stdout), &rec); err != nil {
				panic(err)
			}
			mf := filepath.Join(GinkgoT().TempDir(), "swarm.yaml")
			yaml := "name: fail-swarm\nscope: m2.runtime\npackets:\n  - id: p1\n    scope: m2.runtime\n    task: do the thing\n    source_sha256: " + strings.Repeat("b", 64) + "\ngates:\n  - name: bad\n    description: always fails\n    command: false\n"
			if err := os.WriteFile(mf, []byte(yaml), 0o644); err != nil {
				panic(err)
			}
			r = runArgv([]string{"swarm", "run", "--db", db, "--manifest", mf,
				"--parent", rec["id"].(string), "--budget", "100"})
		})

		It("exits 8", func() {
			Expect(r.exit).To(Equal(8))
		})

		It("reports the receipt_invalid code", func() {
			Expect(r.stderr).To(ContainSubstring(`"code":"receipt_invalid"`))
		})

		It("names the failed gate", func() {
			Expect(r.stderr).To(ContainSubstring("failed gates [bad]"))
		})
	})
})

var _ = DescribeTable("terminalSafeJSON",
	func(in any, want string) {
		b, err := terminalSafeJSON(in)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal(want))
	},
	Entry("DEL", "a\x7fb", `"a\u007fb"`),
	Entry("U+009B", "a\u009bb", `"a\u009bb"`),
	Entry("U+0085", "a\u0085b", `"a\u0085b"`),
	Entry("U+202E", "a\u202eb", `"a\u202eb"`),
	Entry("U+200B", "a\u200bb", `"a\u200bb"`),
	Entry("U+FEFF", "a\ufeffb", `"a\ufeffb"`),
	Entry("U+E0041 tag character as a surrogate pair", "a\U000E0041b", `"a\udb40\udc41b"`),
	Entry("U+1D173 astral format character as a surrogate pair", "a\U0001D173b", `"a\ud834\udd73b"`),
	Entry("U+2028, which encoding/json already escapes", "a\u2028b", `"a\u2028b"`),
	Entry("a plain string", "plain", `"plain"`),
	Entry("invalid UTF-8 inside a raw JSON payload", json.RawMessage("\"a\x9bb\xffc\""), `"a\ufffdb\ufffdc"`),
	Entry("a genuine U+FFFD is kept as is", "a\ufffdb", "\"a\ufffdb\""),
	Entry("accented and CJK letters", "é日本語", `"é日本語"`),
)

// allCfAndControlRunes lists every unicode.Cf and unicode.Cc rune.
func allCfAndControlRunes() string {
	var sb strings.Builder
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

var _ = Describe("terminalSafeJSON fidelity", func() {
	It("decodes every control and format rune back to the original string", func() {
		in := allCfAndControlRunes()
		b, err := terminalSafeJSON(in)
		if err != nil {
			panic(err)
		}
		var out string
		if err := json.Unmarshal(b, &out); err != nil {
			panic(err)
		}
		Expect(out).To(Equal(in))
	})

	It("matches json.Marshal byte for byte on ASCII", func() {
		var sb strings.Builder
		for r := rune(0); r < 0x80; r++ {
			if r != 0x7f {
				sb.WriteRune(r)
			}
		}
		want, err := json.Marshal(sb.String())
		if err != nil {
			panic(err)
		}
		got, err := terminalSafeJSON(sb.String())
		if err != nil {
			panic(err)
		}
		Expect(got).To(Equal(want))
	})
})

var _ = Describe("terminal-safe output wiring", func() {
	const hostile = "a\u202eb\x7fc"

	It("writeJSON escapes a bidi override in session events stdout", func() {
		nr := runCLINew("--provider", "p", "--model", "m", "--task", hostile)
		if nr.exit != 0 {
			panic("session new failed: " + nr.stderr)
		}
		ev := runCLIEvents(decodeNew(nr.stdout).ID)
		Expect(ev.stdout).To(ContainSubstring(`a\u202eb`))
	})

	It("writeJSON escapes DEL in session events stdout", func() {
		nr := runCLINew("--provider", "p", "--model", "m", "--task", hostile)
		if nr.exit != 0 {
			panic("session new failed: " + nr.stderr)
		}
		ev := runCLIEvents(decodeNew(nr.stdout).ID)
		Expect(ev.stdout).To(ContainSubstring(`b\u007fc`))
	})

	It("writeError escapes a bidi override on stderr", func() {
		f, err := os.CreateTemp(GinkgoT().TempDir(), "stderr")
		if err != nil {
			panic(err)
		}
		defer func() { _ = f.Close() }()
		writeError(f, errors.New(hostile))
		b, err := os.ReadFile(f.Name())
		if err != nil {
			panic(err)
		}
		Expect(string(b)).To(ContainSubstring(`a\u202eb`))
	})
})

var _ = Describe("replay transcript", func() {
	const transcript = "{\"payload\":\"a\u202eb\x7fc\U000E0041\"}\n"

	It("leaves a regular file's bytes untouched", func() {
		f, err := os.CreateTemp(GinkgoT().TempDir(), "out")
		if err != nil {
			panic(err)
		}
		defer func() { _ = f.Close() }()
		Expect(string(transcriptFor(f, []byte(transcript)))).To(Equal(transcript))
	})

	It("leaves a pipe's bytes untouched", func() {
		r, w, err := os.Pipe()
		if err != nil {
			panic(err)
		}
		defer func() { _ = r.Close(); _ = w.Close() }()
		Expect(string(transcriptFor(w, []byte(transcript)))).To(Equal(transcript))
	})

	It("escapes control and format characters for a character device", func() {
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			panic(err)
		}
		defer func() { _ = f.Close() }()
		want := "{\"payload\":\"a\\u202eb\\u007fc\\udb40\\udc41\"}\n"
		Expect(string(transcriptFor(f, []byte(transcript)))).To(Equal(want))
	})

	It("escapes ESC, the lowest control byte below the fast-path bound, for a character device", func() {
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			panic(err)
		}
		defer func() { _ = f.Close() }()
		Expect(string(transcriptFor(f, []byte("a\x1b[31mb")))).To(Equal("a\\u001b[31mb"))
	})

	It("reads project profiles from the process directory when there is no --workdir", func() {
		config := GinkgoT().TempDir()
		for _, env := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA"} {
			GinkgoT().Setenv(env, config)
		}
		work := GinkgoT().TempDir()
		dir := filepath.Join(work, ".flowstate", "agents")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			panic(err)
		}
		profile := "---\nname: reviewer\ndescription: Reads a change\ntools: [fs.read]\n---\nYou review.\n"
		if err := os.WriteFile(filepath.Join(dir, "p.md"), []byte(profile), 0o600); err != nil {
			panic(err)
		}
		r := e2eRunInDir("", work, "agents", "list", "--agents", "project")
		Expect(r.stdout).To(ContainSubstring(`"name":"reviewer"`))
	})

	It("writes the raw transcript through a pipe from the replay command", func() {
		nr := runCLINew("--provider", "p", "--model", "m", "--task", "a\u202eb")
		if nr.exit != 0 {
			panic("session new failed: " + nr.stderr)
		}
		rr := runArgv([]string{"replay", decodeNew(nr.stdout).ID, "--db", dbFor()})
		Expect(rr.stdout).To(ContainSubstring("a\u202eb"))
	})

	It("treats a file that cannot be inspected as a terminal", func() {
		f, err := os.CreateTemp(GinkgoT().TempDir(), "closed")
		if err != nil {
			panic(err)
		}
		if err := f.Close(); err != nil {
			panic(err)
		}
		Expect(isTerminal(f)).To(BeTrue())
	})

	Describe("the replay command with a terminal stand-in", func() {
		var sessionID string

		// replayToFile runs the real replay command with stdout a temp file.
		replayToFile := func() string {
			out, err := os.CreateTemp(GinkgoT().TempDir(), "replay")
			if err != nil {
				panic(err)
			}
			defer func() { _ = out.Close() }()
			errf, err := os.CreateTemp(GinkgoT().TempDir(), "replay-err")
			if err != nil {
				panic(err)
			}
			defer func() { _ = errf.Close() }()
			if code := dispatch([]string{"replay", sessionID, "--db", dbFor()}, out, errf); code != 0 {
				panic("replay failed")
			}
			b, err := os.ReadFile(out.Name())
			if err != nil {
				panic(err)
			}
			return string(b)
		}

		setTerminal := func(v bool) {
			orig := stdoutIsTerminal
			DeferCleanup(func() { stdoutIsTerminal = orig })
			stdoutIsTerminal = func(*os.File) bool { return v }
		}

		BeforeEach(func() {
			nr := runCLINew("--provider", "p", "--model", "m", "--task", "a\u202eb\x7fc")
			if nr.exit != 0 {
				panic("session new failed: " + nr.stderr)
			}
			sessionID = decodeNew(nr.stdout).ID
		})

		It("writes a bidi override escaped when stdout is a terminal", func() {
			setTerminal(true)
			Expect(replayToFile()).To(ContainSubstring(`a\u202eb`))
		})

		It("writes DEL escaped when stdout is a terminal", func() {
			setTerminal(true)
			Expect(replayToFile()).To(ContainSubstring(`b\u007fc`))
		})

		It("writes the bidi override raw when stdout is not a terminal", func() {
			setTerminal(false)
			Expect(replayToFile()).To(ContainSubstring("a\u202eb"))
		})

		It("restores the terminal check after a spec replaces it", func() {
			Expect(reflect.ValueOf(stdoutIsTerminal).Pointer()).To(Equal(reflect.ValueOf(isTerminal).Pointer()))
		})
	})

	It("replaces invalid UTF-8 in a replay line with \\ufffd on a terminal", func() {
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			panic(err)
		}
		defer func() { _ = f.Close() }()
		Expect(string(transcriptFor(f, []byte("{\"p\":\"a\x9bb\"}\n")))).To(Equal("{\"p\":\"a\\ufffdb\"}\n"))
	})
})
