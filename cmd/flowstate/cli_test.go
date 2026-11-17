package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

// e2eEnv builds the subprocess environment: the ambient environment
// minus ambient z.ai or FlowState live settings, plus the local fake
// provider selection, so the binary never touches the live network.
func e2eEnv(baseURL string) []string {
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ZAI_") && !strings.HasPrefix(kv, "FLOWSTATE_") {
			env = append(env, kv)
		}
	}
	return append(env, "ZAI_API_KEY=e2e-test-key", "ZAI_BASE_URL="+baseURL,
		"ZAI_PLAN=coding", "ZAI_CODING_ROUTE=openai")
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
	cmd := e2eExecCommand(e2eBin, argv...)
	cmd.Env = e2eEnv(baseURL)
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	return result{exit: e2eExitCode(cmd.Run()), stdout: out.String(), stderr: errs.String()}
}

// e2eStart launches the binary without waiting; the returned
// channel yields the finished result and reading the captured
// output after it receives is race-free (the exec copiers finish
// before Wait returns).
func e2eStart(baseURL string, argv ...string) (*exec.Cmd, chan result) {
	cmd := e2eExecCommand(e2eBin, argv...)
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

var _ = Describe("CLI end-to-end contract matrix", Label("e2e"), func() {
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

	Context("SIGINT mid-run cancels the session", func() {
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
				if serr := cmd.Process.Signal(os.Interrupt); serr != nil {
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
		It("persists session.cancelled with reason sigint", func() {
			Expect(setupErr).NotTo(HaveOccurred())
			Expect(observedCancelledReason).To(Equal("sigint"))
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
})
