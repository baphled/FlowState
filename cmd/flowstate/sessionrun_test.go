package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/zai"
	"github.com/baphled/flowstate/internal/store"
)

// fakeZaiServer serves an SSE stream of the given chunks.
func fakeZaiServer(chunks ...string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
			fl.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	DeferCleanup(srv.Close)
	return srv
}

// withFakeZai swaps the CLI provider resolver for an httptest-backed
// zai adapter and restores it afterwards.
func withFakeZai(srv *httptest.Server) {
	old := providerResolver
	providerResolver = func(name string) (provider.Provider, error) {
		return zai.New("test-key", zai.WithBaseURL(srv.URL), zai.WithCoding()), nil
	}
	DeferCleanup(func() { providerResolver = old })
}

// scriptedStreamer is a fake streaming provider that emits its events
// in order.
type scriptedStreamer struct {
	events []provider.StreamEvent
}

func (s *scriptedStreamer) Name() string     { return "scripted" }
func (s *scriptedStreamer) Models() []string { return []string{"scripted-model"} }
func (s *scriptedStreamer) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{}, nil
}

func (s *scriptedStreamer) Stream(context.Context, *provider.ChatRequest) <-chan provider.StreamEvent {
	out := make(chan provider.StreamEvent, len(s.events))
	for _, e := range s.events {
		out <- e
	}
	close(out)
	return out
}

// plainProvider satisfies provider.Provider without streaming support.
type plainProvider struct{}

func (plainProvider) Name() string     { return "plain" }
func (plainProvider) Models() []string { return nil }
func (plainProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{}, nil
}

// withProvider swaps the resolver for one fixed provider.
func withProvider(p provider.Provider) {
	old := providerResolver
	providerResolver = func(string) (provider.Provider, error) { return p, nil }
	DeferCleanup(func() { providerResolver = old })
}

// failDeltaAppends injects a storage fault without a production seam:
// a trigger on a second connection makes SQLite reject every
// assistant.delta row, as a full disk would, while other rows still
// insert. The streamer then drives the turn into that failed append.
func failDeltaAppends(db string) *scriptedStreamer {
	conn, err := sql.Open("sqlite", db)
	if err != nil {
		panic(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Exec(`CREATE TRIGGER fail_delta BEFORE INSERT ON events WHEN NEW.type = 'assistant.delta' ` +
		`BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		panic(err)
	}
	return &scriptedStreamer{events: []provider.StreamEvent{{Content: "hello"}}}
}

// stderrMessage decodes the first stderr line's error message.
func stderrMessage(out string) string {
	var ec struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(firstLine(out)), &ec); err != nil {
		panic(err)
	}
	return ec.Message
}

// isolatedProviderConfig clears every ambient provider setting, so no live
// provider or credential is reachable, then applies env (name=value pairs).
func isolatedProviderConfig(env ...string) {
	for _, name := range ambientProviderVars {
		GinkgoT().Setenv(name, "")
	}
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		GinkgoT().Setenv(name, value)
	}
}

// finishRunOutcome runs finishRun on err and returns its exit status
// and the stderr JSON line.
func finishRunOutcome(err error) (int, string) {
	f, ferr := os.Create(filepath.Join(GinkgoT().TempDir(), "stderr"))
	if ferr != nil {
		panic(ferr)
	}
	defer func() { _ = f.Close() }()
	exit := finishRun(err, nil, &flags{}, f)
	data, rerr := os.ReadFile(f.Name())
	if rerr != nil {
		panic(rerr)
	}
	return exit, string(data)
}

// runCLIRun runs `session run` with the given id args against the
// per-spec temp DB.
func runCLIRun(ids ...string) result {
	return runArgv(append([]string{"session", "run", "--db", dbFor()}, ids...))
}

// runCLILoop runs `session run <id>` carrying tool-surface loop flags.
func runCLILoop(id string, loopFlags ...string) result {
	argv := []string{"session", "run", "--db", dbFor()}
	argv = append(argv, loopFlags...)
	return runArgv(append(argv, id))
}

// newLoopSession creates a session for loop specs via the CLI.
func newLoopSession() string {
	created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "greet")
	if created.exit != 0 {
		panic("session new failed")
	}
	return decodeNew(created.stdout).ID
}

// stderrCode decodes the first stderr line's contract error code.
func stderrCode(out string) string {
	var ec struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(firstLine(out)), &ec); err != nil {
		panic(err)
	}
	return ec.Code
}

// stdoutEventTypes decodes the projected stdout event types in order.
func stdoutEventTypes(out string) []string {
	types := []string{}
	for _, l := range lines(out) {
		var e struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			panic(err)
		}
		types = append(types, e.Type)
	}
	return types
}

// sessionStatusViaList reads one session's status via `session list`.
func sessionStatusViaList(id string) string {
	list := runArgv([]string{"session", "list", "--db", dbFor(), "--json"})
	status := ""
	for _, l := range lines(list.stdout) {
		var s struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(l), &s); err != nil {
			panic(err)
		}
		if s.ID == id {
			status = s.Status
		}
	}
	return status
}

// nonEmpty drops empty split fragments.
func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

var _ = Describe("Session run", func() {
	Context("streams events and completes for a new session", func() {
		var observedExit int
		var observedResultCount int
		var observedLastType string
		var observedLastActor string
		var observedCompleted bool
		BeforeEach(func() {
			srv := fakeZaiServer(
				`{"choices":[{"delta":{"content":"he"}}]}`,
				`{"choices":[{"delta":{"content":"y"}}]}`,
			)
			withFakeZai(srv)

			created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "greet")
			if created.exit != 0 {
				panic("session new failed")
			}
			rec := decodeNew(created.stdout)

			run := runCLIRun(rec.ID)
			got := nonEmpty(strings.Split(run.stdout, "\n"))
			var last struct {
				Type    string          `json:"type"`
				Actor   string          `json:"actor"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal([]byte(got[len(got)-1]), &last); err != nil {
				panic(err)
			}
			observedExit = run.exit
			observedResultCount = len(got)
			observedLastType = last.Type
			observedLastActor = last.Actor
			observedCompleted = strings.Contains(string(last.Payload), `"status":"completed"`)
		})
		It("returns the expected exit status", func() { Expect(observedExit).To(Equal(0)) })
		It("returns one matching session", func() { Expect(observedResultCount).To(Equal(6)) })
		It("emits the expected final event type", func() { Expect(observedLastType).To(Equal("turn.end")) })
		It("attributes the final event to the engine", func() { Expect(observedLastActor).To(Equal("engine")) })
		It("records completed status in the final payload", func() { Expect(observedCompleted).To(BeTrue()) })
	})

	Context("persists the streamed events in order", func() {
		var observedExit int
		var observedEventOrder string
		var observedExpectedOrder string
		BeforeEach(func() {
			srv := fakeZaiServer(
				`{"choices":[{"delta":{"content":"he"}}]}`,
				`{"choices":[{"delta":{"content":"y"}}]}`,
			)
			withFakeZai(srv)

			created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "greet")
			if created.exit != 0 {
				panic("session new failed")
			}
			rec := decodeNew(created.stdout)
			run := runCLIRun(rec.ID)
			if run.exit != 0 {
				panic("session run failed")
			}

			evts := runCLIEvents(rec.ID)
			wantOrder := "turn.start,user.message,assistant.delta,assistant.delta,assistant.message,turn.end"
			gotTypes := []string{}
			for _, l := range nonEmpty(strings.Split(evts.stdout, "\n")) {
				var e struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal([]byte(l), &e); err != nil {
					panic(err)
				}
				gotTypes = append(gotTypes, e.Type)
			}
			observedExit = evts.exit
			observedEventOrder = strings.Join(gotTypes, ",")
			observedExpectedOrder = wantOrder
		})
		It("lists events successfully", func() { Expect(observedExit).To(Equal(0)) })
		It("persists the complete event sequence in order", func() { Expect(observedEventOrder).To(Equal(observedExpectedOrder)) })
	})

	Context("exits 3 and emits a provider.error event on provider failure", func() {
		var observedExit int
		var observedLastType string
		BeforeEach(func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"error":{"code":"502","message":"down"}}`))
			}))
			DeferCleanup(srv.Close)
			withFakeZai(srv)

			created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "greet")
			if created.exit != 0 {
				panic("session new failed")
			}
			var rec struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(strings.SplitN(created.stdout, "\n", 2)[0]), &rec); err != nil {
				panic(err)
			}

			run := runCLIRun(rec.ID)
			got := nonEmpty(strings.Split(run.stdout, "\n"))
			lastType := ""
			for _, l := range got {
				var e struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal([]byte(l), &e); err != nil || e.Type == "" {
					continue
				}
				lastType = e.Type
			}
			observedExit = run.exit
			observedLastType = lastType
		})
		It("returns the expected exit status", func() { Expect(observedExit).To(Equal(3)) })
		It("emits provider.error last", func() { Expect(observedLastType).To(Equal("provider.error")) })
	})

	It("exits 2 with usage when the session id is missing", func() {
		Expect(runCLIRun().exit).To(Equal(2))
	})

	Context("exits 5 and pauses the session when the budget is exceeded", func() {
		var observedExit int
		var observedCode string
		var observedLastType string
		var observedPausedPayload bool
		var observedStatus string
		BeforeEach(func() {
			srv := fakeZaiServer(
				`{"choices":[{"delta":{"content":"hello world"}}]}`,
				`{"choices":[{"delta":{"content":" again"}}]}`,
			)
			withFakeZai(srv)

			created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "greet")
			if created.exit != 0 {
				panic("session new failed")
			}
			rec := decodeNew(created.stdout)

			run := runArgv([]string{"session", "run", "--db", dbFor(), "--budget", "5", rec.ID})
			var ec struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal([]byte(strings.SplitN(run.stderr, "\n", 2)[0]), &ec); err != nil {
				panic(err)
			}
			got := nonEmpty(strings.Split(run.stdout, "\n"))
			var last struct {
				Type    string          `json:"type"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal([]byte(got[len(got)-1]), &last); err != nil {
				panic(err)
			}
			observedExit = run.exit
			observedCode = ec.Code
			observedLastType = last.Type
			observedPausedPayload = strings.Contains(string(last.Payload), "budget_exceeded")

			list := runArgv([]string{"session", "list", "--db", dbFor(), "--json"})
			for _, l := range nonEmpty(strings.Split(list.stdout, "\n")) {
				var s struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				}
				if err := json.Unmarshal([]byte(l), &s); err != nil {
					panic(err)
				}
				if s.ID == rec.ID {
					observedStatus = s.Status
				}
			}
		})
		It("returns the budget exit status", func() { Expect(observedExit).To(Equal(5)) })
		It("emits budget_exceeded on stderr", func() { Expect(observedCode).To(Equal("budget_exceeded")) })
		It("emits session.paused as the final stdout event", func() { Expect(observedLastType).To(Equal("session.paused")) })
		It("records reason budget_exceeded in the paused payload", func() { Expect(observedPausedPayload).To(BeTrue()) })
		It("marks the session paused", func() { Expect(observedStatus).To(Equal("paused")) })
	})

	Context("completes normally when the budget is not reached", func() {
		var observedExit int
		var observedLastType string
		BeforeEach(func() {
			srv := fakeZaiServer(
				`{"choices":[{"delta":{"content":"he"}}]}`,
				`{"choices":[{"delta":{"content":"y"}}]}`,
			)
			withFakeZai(srv)

			created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "greet")
			if created.exit != 0 {
				panic("session new failed")
			}
			rec := decodeNew(created.stdout)
			run := runArgv([]string{"session", "run", "--db", dbFor(), "--budget", "1000", rec.ID})
			got := nonEmpty(strings.Split(run.stdout, "\n"))
			var last struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(got[len(got)-1]), &last); err != nil {
				panic(err)
			}
			observedExit = run.exit
			observedLastType = last.Type
		})
		It("returns the expected exit status", func() { Expect(observedExit).To(Equal(0)) })
		It("completes the turn", func() { Expect(observedLastType).To(Equal("turn.end")) })
	})

	Context("passes --max-tokens through to the provider request", func() {
		var observedMaxTokens float64
		BeforeEach(func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					panic(err)
				}
				var req struct {
					MaxTokens float64 `json:"max_tokens"`
				}
				if err := json.Unmarshal(body, &req); err != nil {
					panic(err)
				}
				observedMaxTokens = req.MaxTokens
				w.Header().Set("Content-Type", "text/event-stream")
				fl := w.(http.Flusher)
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				fl.Flush()
			}))
			DeferCleanup(srv.Close)
			withFakeZai(srv)

			created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "greet")
			if created.exit != 0 {
				panic("session new failed")
			}
			rec := decodeNew(created.stdout)
			// 9000 sits above the coding-route ClampMaxTokens floor
			// (8192) so the configured value reaches the wire intact.
			run := runArgv([]string{"session", "run", "--db", dbFor(), "--max-tokens", "9000", rec.ID})
			if run.exit != 0 {
				panic("session run failed")
			}
		})
		It("sends max_tokens in the request body", func() { Expect(observedMaxTokens).To(Equal(9000.0)) })
	})

	Context("rejects a negative budget with usage", func() {
		var observedExit int
		var observedCode string
		BeforeEach(func() {
			created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "greet")
			if created.exit != 0 {
				panic("session new failed")
			}
			rec := decodeNew(created.stdout)
			run := runArgv([]string{"session", "run", "--db", dbFor(), "--budget", "-3", rec.ID})
			var ec struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal([]byte(strings.SplitN(run.stderr, "\n", 2)[0]), &ec); err != nil {
				panic(err)
			}
			observedExit = run.exit
			observedCode = ec.Code
		})
		It("rejects with the usage exit status", func() { Expect(observedExit).To(Equal(2)) })
		It("rejects with the usage code", func() { Expect(observedCode).To(Equal("usage")) })
	})

	Context("rejects a non-numeric budget with usage", func() {
		var observedExit int
		var observedCode string
		BeforeEach(func() {
			created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "greet")
			if created.exit != 0 {
				panic("session new failed")
			}
			rec := decodeNew(created.stdout)
			run := runArgv([]string{"session", "run", "--db", dbFor(), "--budget", "abc", rec.ID})
			var ec struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal([]byte(strings.SplitN(run.stderr, "\n", 2)[0]), &ec); err != nil {
				panic(err)
			}
			observedExit = run.exit
			observedCode = ec.Code
		})
		It("rejects with the usage exit status", func() { Expect(observedExit).To(Equal(2)) })
		It("rejects with the usage code", func() { Expect(observedCode).To(Equal("usage")) })
	})

	It("exits 1 for an unknown session", func() {
		Expect(runCLIRun("nope").exit).To(Equal(1))
	})

	Context("exits 6 when running an already-terminal session", func() {
		var observedFirstExit int
		var observedSecondExit int
		BeforeEach(func() {
			srv := fakeZaiServer(`{"choices":[{"delta":{"content":"x"}}]}`)
			withFakeZai(srv)
			created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "greet")
			var rec struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(strings.SplitN(created.stdout, "\n", 2)[0]), &rec); err != nil {
				panic(err)
			}
			first := runCLIRun(rec.ID)
			second := runCLIRun(rec.ID)
			observedFirstExit = first.exit
			observedSecondExit = second.exit
		})
		It("completes the initial run", func() { Expect(observedFirstExit).To(Equal(0)) })
		It("rejects rerunning the terminal session", func() { Expect(observedSecondExit).To(Equal(6)) })
	})
})

// firstIndexOf returns the first index of want in list, or -1.
func firstIndexOf(list []string, want string) int {
	for i, s := range list {
		if s == want {
			return i
		}
	}
	return -1
}

// lastIndexOf returns the last index of want in list, or -1.
func lastIndexOf(list []string, want string) int {
	idx := -1
	for i, s := range list {
		if s == want {
			idx = i
		}
	}
	return idx
}

// newPausedSession creates a session and pauses it via a budget-5
// run, failing the spec when the pause setup itself breaks.
func newPausedSession() string {
	srv := fakeZaiServer(
		`{"choices":[{"delta":{"content":"hello world"}}]}`,
		`{"choices":[{"delta":{"content":" again"}}]}`,
	)
	withFakeZai(srv)
	created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "greet")
	if created.exit != 0 {
		panic("session new failed")
	}
	id := decodeNew(created.stdout).ID
	if pause := runArgv([]string{"session", "run", "--db", dbFor(), "--budget", "5", id}); pause.exit != 5 {
		panic("session pause setup failed")
	}
	return id
}

var _ = Describe("Session run explicit resume", func() {
	Context("rerunning a budget-paused session", func() {
		var observedExit int
		var observedResumedBeforeWork bool
		var observedLastType string
		var observedStatus string
		BeforeEach(func() {
			id := newPausedSession()
			// 20 exceeds the 17 the resumed stream consumes, so a
			// fresh limit completes while a carried-over pre-pause
			// budget would stop the run again.
			run := runArgv([]string{"session", "run", "--db", dbFor(), "--budget", "20", id})
			types := stdoutEventTypes(run.stdout)
			resumed, work := firstIndexOf(types, "session.resumed"), lastIndexOf(types, "user.message")
			observedExit = run.exit
			observedResumedBeforeWork = resumed >= 0 && work >= 0 && resumed < work
			observedLastType = types[len(types)-1]
			observedStatus = sessionStatusViaList(id)
		})
		It("exits 0 on the resumed run", func() { Expect(observedExit).To(Equal(0)) })
		It("emits session.resumed before the resumed turn work", func() {
			Expect(observedResumedBeforeWork).To(BeTrue())
		})
		It("completes the resumed run under the fresh budget", func() {
			Expect(observedLastType).To(Equal("turn.end"))
		})
		It("marks the resumed session completed", func() {
			Expect(observedStatus).To(Equal("completed"))
		})
	})

	Context("the resumed run gets a fresh bounded limit", func() {
		var observedExit int
		var observedCode string
		var observedStatus string
		var observedResumedCount int
		BeforeEach(func() {
			id := newPausedSession()
			run := runArgv([]string{"session", "run", "--db", dbFor(), "--budget", "5", id})
			observedExit = run.exit
			observedCode = stderrCode(run.stderr)
			observedStatus = sessionStatusViaList(id)
			resumed := 0
			for _, t := range stdoutEventTypes(runCLIEvents(id).stdout) {
				if t == "session.resumed" {
					resumed++
				}
			}
			observedResumedCount = resumed
		})
		It("stops the resumed run at the fresh budget", func() { Expect(observedExit).To(Equal(5)) })
		It("reports budget_exceeded again", func() { Expect(observedCode).To(Equal("budget_exceeded")) })
		It("re-pauses the session", func() { Expect(observedStatus).To(Equal("paused")) })
		It("resumes exactly once before the second stop", func() { Expect(observedResumedCount).To(Equal(1)) })
	})

	Context("rerunning a cancelled session", func() {
		var observedExit int
		var observedCode string
		BeforeEach(func() {
			srv := fakeZaiServer(`{"choices":[{"delta":{"content":"x"}}]}`)
			withFakeZai(srv)
			created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "greet")
			if created.exit != 0 {
				panic("session new failed")
			}
			id := decodeNew(created.stdout).ID
			if cancel := runArgv([]string{"session", "cancel", "--db", dbFor(), id}); cancel.exit != 0 {
				panic("session cancel setup failed")
			}
			run := runCLIRun(id)
			observedExit = run.exit
			observedCode = stderrCode(run.stderr)
		})
		It("rejects the rerun with the terminal exit status", func() { Expect(observedExit).To(Equal(6)) })
		It("reports session_terminal", func() { Expect(observedCode).To(Equal("session_terminal")) })
	})

	Context("a resume append failure blocks the rerun", func() {
		var observedExit int
		var observedCode string
		var observedStdoutLines int
		var observedStatus string
		var observedTypes []string
		BeforeEach(func() {
			id := newPausedSession()
			// Hold the session's single event-writer slot in this
			// process so the CLI's resume append is fenced off.
			st, err := store.Open(dbFor())
			if err != nil {
				panic(err)
			}
			DeferCleanup(func() { _ = st.Close() })
			held, err := st.NewEventWriter(context.Background(), id)
			if err != nil {
				panic(err)
			}
			DeferCleanup(func() { _ = held.Close() })
			run := runCLIRun(id)
			observedExit = run.exit
			observedCode = stderrCode(run.stderr)
			observedStdoutLines = len(lines(run.stdout))
			observedStatus = sessionStatusViaList(id)
			observedTypes = stdoutEventTypes(runCLIEvents(id).stdout)
		})
		It("exits with the internal failure status", func() { Expect(observedExit).To(Equal(1)) })
		It("surfaces the internal error code", func() { Expect(observedCode).To(Equal("internal")) })
		It("runs no turn work after the failed resume", func() {
			Expect(observedStdoutLines).To(Equal(0))
		})
		It("leaves the session paused and unresolved", func() {
			Expect(observedStatus).To(Equal("paused"))
		})
		It("persists no session.resumed event", func() {
			Expect(observedTypes).NotTo(ContainElement("session.resumed"))
		})
	})
})

var _ = Describe("Session run tool-surface loop", func() {
	Context("completes a loop run over an allowlisted tool", func() {
		var observedExit int
		var observedLastType string
		var observedCompleted bool
		var observedStatus string
		BeforeEach(func() {
			srv := fakeZaiServer(`{"choices":[{"delta":{"content":"loop done"},"finish_reason":"stop"}]}`)
			withFakeZai(srv)
			id := newLoopSession()
			run := runCLILoop(id, "--allow", "echo")
			got := lines(run.stdout)
			var last struct {
				Type    string          `json:"type"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal([]byte(got[len(got)-1]), &last); err != nil {
				panic(err)
			}
			observedExit = run.exit
			observedLastType = last.Type
			observedCompleted = strings.Contains(string(last.Payload), `"status":"completed"`)
			observedStatus = sessionStatusViaList(id)
		})
		It("returns the expected exit status", func() { Expect(observedExit).To(Equal(0)) })
		It("emits turn.end as the final event", func() { Expect(observedLastType).To(Equal("turn.end")) })
		It("records completed status in the final payload", func() { Expect(observedCompleted).To(BeTrue()) })
		It("marks the session completed", func() { Expect(observedStatus).To(Equal("completed")) })
	})

	Context("stops at the loop budget with exit 5 and a paused session", func() {
		var observedExit int
		var observedCode string
		var observedFinalTypes []string
		var observedStatus string
		BeforeEach(func() {
			srv := fakeZaiServer(
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"echo","arguments":"{}"}}]}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			)
			withFakeZai(srv)
			id := newLoopSession()
			run := runCLILoop(id, "--allow", "echo", "--budget", "1")
			types := stdoutEventTypes(run.stdout)
			observedExit = run.exit
			observedCode = stderrCode(run.stderr)
			observedFinalTypes = types[len(types)-2:]
			observedStatus = sessionStatusViaList(id)
		})
		It("returns the budget exit status", func() { Expect(observedExit).To(Equal(5)) })
		It("emits budget_exceeded on stderr", func() { Expect(observedCode).To(Equal("budget_exceeded")) })
		It("persists turn.end then session.paused as the final events", func() {
			Expect(observedFinalTypes).To(Equal([]string{"turn.end", "session.paused"}))
		})
		It("leaves the session paused", func() { Expect(observedStatus).To(Equal("paused")) })
	})

	Context("exits 3 with provider_error on a loop provider failure", func() {
		var observedExit int
		var observedCode string
		var observedStatus string
		BeforeEach(func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"error":{"code":"502","message":"down"}}`))
			}))
			DeferCleanup(srv.Close)
			withFakeZai(srv)
			id := newLoopSession()
			run := runCLILoop(id, "--allow", "echo")
			observedExit = run.exit
			observedCode = stderrCode(run.stderr)
			observedStatus = sessionStatusViaList(id)
		})
		It("returns the provider exit status", func() { Expect(observedExit).To(Equal(3)) })
		It("emits provider_error on stderr", func() { Expect(observedCode).To(Equal("provider_error")) })
		It("marks the session failed", func() { Expect(observedStatus).To(Equal("failed")) })
	})

	Context("routes --workdir onto the loop surface", func() {
		var observedExit int
		var observedLastType string
		var observedHasUserMessage bool
		BeforeEach(func() {
			srv := fakeZaiServer(`{"choices":[{"delta":{"content":"wired"}}]}`)
			withFakeZai(srv)
			id := newLoopSession()
			run := runCLILoop(id, "--workdir", GinkgoT().TempDir())
			types := stdoutEventTypes(run.stdout)
			observedExit = run.exit
			observedLastType = types[len(types)-1]
			for _, t := range types {
				if t == "user.message" {
					observedHasUserMessage = true
				}
			}
		})
		It("returns the expected exit status", func() { Expect(observedExit).To(Equal(0)) })
		It("emits turn.end as the final event", func() { Expect(observedLastType).To(Equal("turn.end")) })
		It("drives the loop surface without a user.message projection", func() { Expect(observedHasUserMessage).To(BeFalse()) })
	})

	Context("passes --max-tokens through on the loop surface", func() {
		var observedMaxTokens float64
		BeforeEach(func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					panic(err)
				}
				var req struct {
					MaxTokens float64 `json:"max_tokens"`
				}
				if err := json.Unmarshal(body, &req); err != nil {
					panic(err)
				}
				observedMaxTokens = req.MaxTokens
				w.Header().Set("Content-Type", "text/event-stream")
				fl := w.(http.Flusher)
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				fl.Flush()
			}))
			DeferCleanup(srv.Close)
			withFakeZai(srv)
			id := newLoopSession()
			// 9000 sits above the coding-route ClampMaxTokens floor
			// (8192) so the configured value reaches the wire intact.
			run := runCLILoop(id, "--allow", "echo", "--max-tokens", "9000")
			if run.exit != 0 {
				panic("session run failed")
			}
		})
		It("sends max_tokens in the loop request body", func() { Expect(observedMaxTokens).To(Equal(9000.0)) })
	})

	Context("rejects rerunning a terminal session via loop flags", func() {
		var observedSecondExit int
		BeforeEach(func() {
			srv := fakeZaiServer(`{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`)
			withFakeZai(srv)
			id := newLoopSession()
			if first := runCLILoop(id, "--allow", "echo"); first.exit != 0 {
				panic("initial loop run failed")
			}
			observedSecondExit = runCLILoop(id, "--allow", "echo").exit
		})
		It("rejects the rerun with the terminal exit status", func() { Expect(observedSecondExit).To(Equal(6)) })
	})
})

var _ = Describe("Session run failure classification", func() {
	newSession := func(providerName string) string {
		created := runCLINew("--provider", providerName, "--model", "glm-5.3", "--task", "greet")
		if created.exit != 0 {
			panic("session new failed")
		}
		return decodeNew(created.stdout).ID
	}

	Context("an unclassified error", func() {
		It("exits 1", func() { Expect(exitCodeFor(errors.New("boom"))).To(Equal(1)) })
		It("reports the internal code", func() {
			Expect(asCliError(errors.New("boom")).Code).To(Equal("internal"))
		})
	})

	Context("a storage failure while recording the turn", func() {
		var run result
		BeforeEach(func() {
			id := newSession("z.ai")
			withProvider(failDeltaAppends(dbFor()))
			run = runCLIRun(id)
		})
		It("exits with the internal failure status", func() { Expect(run.exit).To(Equal(1)) })
		It("reports the internal code", func() { Expect(stderrCode(run.stderr)).To(Equal("internal")) })
		It("does not blame the provider", func() {
			Expect(stderrMessage(run.stderr)).NotTo(ContainSubstring("provider_error"))
		})
		It("reports the storage cause", func() {
			Expect(stderrMessage(run.stderr)).To(ContainSubstring("disk full"))
		})
	})

	Context("a provider HTTP failure", func() {
		var run result
		BeforeEach(func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"code":"429","message":"slow down"}}`))
			}))
			DeferCleanup(srv.Close)
			withFakeZai(srv)
			run = runCLIRun(newSession("z.ai"))
		})
		It("exits 3", func() { Expect(run.exit).To(Equal(3)) })
		It("reports provider_error", func() { Expect(stderrCode(run.stderr)).To(Equal("provider_error")) })
		It("names provider_error once in the message", func() {
			Expect(strings.Count(stderrMessage(run.stderr), "provider_error")).To(Equal(1))
		})
	})

	Context("a provider tool-protocol failure", func() {
		var run result
		BeforeEach(func() {
			withProvider(&scriptedStreamer{events: []provider.StreamEvent{
				{Err: &zai.ToolProtocolError{Reason: "duplicate tool ID"}}}})
			run = runCLIRun(newSession("z.ai"))
		})
		It("exits 3", func() { Expect(run.exit).To(Equal(3)) })
		It("reports provider_error", func() { Expect(stderrCode(run.stderr)).To(Equal("provider_error")) })
	})

	Context("a provider without streaming support", func() {
		var run result
		BeforeEach(func() {
			withProvider(plainProvider{})
			run = runCLIRun(newSession("z.ai"))
		})
		It("exits 3", func() { Expect(run.exit).To(Equal(3)) })
		It("reports provider_error", func() { Expect(stderrCode(run.stderr)).To(Equal("provider_error")) })
	})

	Context("a missing provider key", func() {
		var run result
		BeforeEach(func() {
			id := newSession("z.ai")
			isolatedProviderConfig()
			run = runCLIRun(id)
		})
		It("exits 3", func() { Expect(run.exit).To(Equal(3)) })
		It("reports provider_error", func() { Expect(stderrCode(run.stderr)).To(Equal("provider_error")) })
		It("names provider_error once in the message", func() {
			Expect(strings.Count(stderrMessage(run.stderr), "provider_error")).To(Equal(1))
		})
	})

	Context("an unknown FLOWSTATE_ANTHROPIC_AUTH on the anthropic route", func() {
		var run result
		BeforeEach(func() {
			id := newSession("anthropic")
			isolatedProviderConfig("FLOWSTATE_ANTHROPIC_AUTH=magic")
			run = runCLIRun(id)
		})
		It("exits 3", func() { Expect(run.exit).To(Equal(3)) })
		It("reports provider_error", func() { Expect(stderrCode(run.stderr)).To(Equal("provider_error")) })
	})

	DescribeTable("finishRun exit status",
		func(err error, want int) {
			exit, _ := finishRunOutcome(err)
			Expect(exit).To(Equal(want))
		},
		Entry("persistence failure", errors.New("append event seq 3: disk full"), 1),
		Entry("lease fence", app.ErrExecLoopFenced, 1),
		Entry("wrapped provider failure", fmt.Errorf("%w: %w", app.ErrProvider, errors.New("zai: api error 429")), 3),
		Entry("budget", app.ErrBudgetExceeded, 5),
		Entry("usage", app.ErrUsage, 2),
		Entry("cancellation", app.ErrSessionCancelled, 130),
	)

	DescribeTable("finishRun error code",
		func(err error, want string) {
			_, stderr := finishRunOutcome(err)
			Expect(stderrCode(stderr)).To(Equal(want))
		},
		Entry("persistence failure", errors.New("append event seq 3: disk full"), "internal"),
		Entry("lease fence", app.ErrExecLoopFenced, "internal"),
		Entry("wrapped provider failure", fmt.Errorf("%w: %w", app.ErrProvider, errors.New("zai: api error 429")),
			"provider_error"),
	)

	Context("a cancelled run whose reservation could not be settled", func() {
		var (
			exit   int
			stderr string
		)

		BeforeEach(func() {
			exit, stderr = finishRunOutcome(fmt.Errorf("%w: %w: session s1 is cancelled: settle blocked",
				app.ErrSessionCancelled, app.ErrReservationSettle))
		})

		It("keeps exit 130", func() { Expect(exit).To(Equal(130)) })
		It("keeps the cancelled message", func() {
			Expect(stderr).To(ContainSubstring("interrupted; session cancelled"))
		})
		It("prints the settle failure naming the session", func() {
			Expect(stderr).To(ContainSubstring("reservation_settle_failed: session s1 is cancelled: settle blocked"))
		})
		It("prints one line more than a plain cancellation", func() {
			Expect(strings.Count(stderr, "\n")).To(Equal(2))
		})
	})

	It("prints no settle line for a plain cancellation", func() {
		_, stderr := finishRunOutcome(app.ErrSessionCancelled)
		Expect(stderr).NotTo(ContainSubstring("reservation_settle_failed"))
	})

	It("does not wrap a provider failure a second time", func() {
		_, stderr := finishRunOutcome(fmt.Errorf("%w: %w", app.ErrProvider, errors.New("zai: api error 429")))
		Expect(stderrMessage(stderr)).To(Equal("provider_error: zai: api error 429"))
	})
})

var _ = Describe("Session run truncation notice", func() {
	const noticeText = "cut off at the token limit"
	// runID creates a session and runs it against a scripted provider
	// whose stream ends with the given finish reason (none when empty),
	// on the single-turn path, or the loop path when loopFlags are given.
	// The real adapter emits no finish event on the tool-less path.
	runID := func(finish string, loopFlags ...string) (result, string) {
		events := []provider.StreamEvent{{Content: "partial"}}
		if finish != "" {
			events = append(events, provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: finish})
		}
		withProvider(&scriptedStreamer{events: events})
		id := newLoopSession()
		if len(loopFlags) == 0 {
			return runCLIRun(id), id
		}
		return runCLILoop(id, loopFlags...), id
	}
	run := func(finish string, loopFlags ...string) result {
		r, _ := runID(finish, loopFlags...)
		return r
	}

	DescribeTable("names the cut-off on stderr when the final turn ended at the limit",
		func(loopFlags ...string) {
			Expect(run("length", loopFlags...).stderr).To(ContainSubstring(noticeText))
		},
		Entry("single-turn path"),
		Entry("loop path", "--allow", "echo"),
	)

	DescribeTable("points at session doctor for the session",
		func(loopFlags ...string) {
			r, id := runID("length", loopFlags...)
			Expect(r.stderr).To(ContainSubstring("session doctor " + id))
		},
		Entry("single-turn path"),
		Entry("loop path", "--allow", "echo"),
	)

	DescribeTable("points at --max-tokens",
		func(loopFlags ...string) {
			Expect(run("length", loopFlags...).stderr).To(ContainSubstring("--max-tokens"))
		},
		Entry("single-turn path"),
		Entry("loop path", "--allow", "echo"),
	)

	DescribeTable("keeps exit 0 for a truncated answer",
		func(loopFlags ...string) {
			Expect(run("length", loopFlags...).exit).To(Equal(0))
		},
		Entry("single-turn path"),
		Entry("loop path", "--allow", "echo"),
	)

	DescribeTable("keeps the event stream ending in turn.end for a truncated answer",
		func(loopFlags ...string) {
			types := stdoutEventTypes(run("length", loopFlags...).stdout)
			Expect(types[len(types)-1]).To(Equal("turn.end"))
		},
		Entry("single-turn path"),
		Entry("loop path", "--allow", "echo"),
	)

	DescribeTable("stays silent for a stop finish",
		func(loopFlags ...string) {
			Expect(run("stop", loopFlags...).stderr).To(BeEmpty())
		},
		Entry("single-turn path"),
		Entry("loop path", "--allow", "echo"),
	)

	DescribeTable("stays silent when the provider reports no finish reason",
		func(loopFlags ...string) {
			Expect(run("", loopFlags...).stderr).To(BeEmpty())
		},
		Entry("single-turn path"),
		Entry("loop path", "--allow", "echo"),
	)

	It("copies a loop result's finish reason onto the turn result", func() {
		res := toTurnResult(&app.Session{ID: "s1"}, &app.ExecLoopResult{FinishReason: "length"})
		Expect(res.FinishReason).To(Equal("length"))
	})

	It("keeps an empty loop finish reason empty on the turn result", func() {
		res := toTurnResult(&app.Session{ID: "s1"}, &app.ExecLoopResult{})
		Expect(res.FinishReason).To(BeEmpty())
	})
})
