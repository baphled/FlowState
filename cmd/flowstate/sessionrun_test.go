package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/zai"
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
		return zai.New("test-key", zai.WithBaseURL(srv.URL)), nil
	}
	DeferCleanup(func() { providerResolver = old })
}

// runCLIRun runs `session run` with the given id args against the
// per-spec temp DB.
func runCLIRun(ids ...string) result {
	return runArgv(append([]string{"session", "run", "--db", dbFor()}, ids...))
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

			created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
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

			created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
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

			created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
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

			created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
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

			created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
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

			created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
			if created.exit != 0 {
				panic("session new failed")
			}
			rec := decodeNew(created.stdout)
			run := runArgv([]string{"session", "run", "--db", dbFor(), "--max-tokens", "42", rec.ID})
			if run.exit != 0 {
				panic("session run failed")
			}
		})
		It("sends max_tokens in the request body", func() { Expect(observedMaxTokens).To(Equal(42.0)) })
	})

	Context("rejects a negative budget with usage", func() {
		var observedExit int
		var observedCode string
		BeforeEach(func() {
			created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
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
			created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
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
			created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
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
